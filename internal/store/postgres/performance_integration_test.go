//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSyntheticScalePlansAndThroughput(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prepareDatabase(t, ctx, dsn)
	repo, err := OpenRepository(ctx, dsn, DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	projectID := uuidFor(0x60000000, 1)
	sourceID := uuidFor(0x60000000, 2)
	if err := repo.PutProject(ctx, Project{
		ID: projectID, VersionID: uuidFor(0x60000000, 3), CreatedAt: base,
		EffectiveAt: base, Classification: "internal", LifecycleState: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutSource(ctx, Source{
		ID: sourceID, VersionID: uuidFor(0x60000000, 4), Kind: "synthetic_jsonl",
		ProductSurface: "local", CoverageState: "complete", Enabled: true,
		CreatedAt: base, EffectiveAt: base,
	}); err != nil {
		t.Fatal(err)
	}
	sessionID := uuidFor(0x60000000, 5)
	if _, err := repo.pool.Exec(ctx, `INSERT INTO prompt_better.sessions
        (session_id, project_id, source_id, started_at, coverage_state, knowledge_state)
        VALUES ($1,$2,$3,$4,'complete','observed')`, sessionID, projectID,
		sourceID, base); err != nil {
		t.Fatal(err)
	}
	trajectoryIDs := make([]string, 100)
	for i := range trajectoryIDs {
		trajectoryIDs[i] = uuidFor(0x61000000, i+1)
		if _, err := repo.pool.Exec(ctx, `INSERT INTO prompt_better.trajectories
            (trajectory_id, session_id, source_id, started_at, knowledge_state)
            VALUES ($1,$2,$3,$4,'observed')`, trajectoryIDs[i], sessionID,
			sourceID, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	started := time.Now()
	for batch := 0; batch < 2; batch++ {
		observations := make([]UsageObservation, maxUsageBatch)
		for i := range observations {
			n := batch*maxUsageBatch + i
			trajectoryID := trajectoryIDs[n%len(trajectoryIDs)]
			value := float64(n % 1000)
			observations[i] = UsageObservation{
				ID: uuidFor(0x62000000, n+1), TrajectoryID: &trajectoryID,
				MetricKind: "input_tokens", Value: &value, UsageUnit: "token",
				ProductSurface: "local", AccountingRegime: "local",
				Provenance: "runtime_observed", SourceAdapter: "synthetic",
				ObservedAt:     base.Add(time.Duration(n) * time.Millisecond),
				KnowledgeState: "observed",
			}
		}
		if err := repo.PutUsageBatch(ctx, observations); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("10k native usage insert exceeded 5s diagnostic budget: %s", elapsed)
	}
	if _, err := repo.pool.Exec(ctx, "ANALYZE prompt_better.usage_observations"); err != nil {
		t.Fatal(err)
	}

	var plan []byte
	if err := repo.pool.QueryRow(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
        SELECT usage_observation_id, value_numeric
        FROM prompt_better.usage_observations
        WHERE trajectory_id=$1 AND metric_kind='input_tokens'
        ORDER BY observed_at LIMIT 100`, trajectoryIDs[0]).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan), "usage_observations_trajectory_metric_idx") {
		t.Fatalf("selective usage plan missed owned index: %s", plan)
	}

	started = time.Now()
	var total float64
	if err := repo.pool.QueryRow(ctx, `SELECT COALESCE(sum(known_value), 0)
        FROM prompt_better.trajectory_usage WHERE trajectory_id=$1`,
		trajectoryIDs[0]).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("trajectory analytics exceeded 2s diagnostic budget")
	}
	if total <= 0 {
		t.Fatal("trajectory analytics returned no synthetic usage")
	}
}

func uuidFor(prefix, value int) string {
	return fmt.Sprintf("%08x-0000-0000-0000-%012x", prefix, value)
}
