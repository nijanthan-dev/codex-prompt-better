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
	for version := 2; version <= 100; version++ {
		if err := repo.PutProject(ctx, Project{
			ID: projectID, VersionID: uuidFor(0x60010000, version), CreatedAt: base,
			EffectiveAt:    base.Add(time.Duration(version) * time.Hour),
			Classification: []string{"internal", "confidential"}[version%2], LifecycleState: "active",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.pool.Exec(ctx, `INSERT INTO prompt_better.projects (project_id, created_at)
        SELECT md5('plan-project-' || project_no)::uuid, $1
		FROM generate_series(1,100) project_no`, base); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `INSERT INTO prompt_better.project_versions
          (project_version_id, project_id, version_number, classification,
           lifecycle_state, version_hash, valid_from, valid_to)
        SELECT md5('plan-version-' || project_no || '-' || version_no)::uuid,
          md5('plan-project-' || project_no)::uuid, version_no, 'internal', 'active',
		  decode(repeat('00',32),'hex'), $1::timestamptz + version_no * interval '1 hour',
		  CASE WHEN version_no=100 THEN NULL ELSE $1::timestamptz + (version_no+1) * interval '1 hour' END
        FROM generate_series(1,100) project_no
		CROSS JOIN generate_series(1,100) version_no`, base); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `ANALYZE prompt_better.project_versions`); err != nil {
		t.Fatal(err)
	}
	var asOfPlan, currentPlan string
	if err := repo.pool.QueryRow(ctx, `EXPLAIN (FORMAT JSON) SELECT project_version_id
        FROM prompt_better.project_versions WHERE project_id=$1 AND valid_from <= $2
        AND (valid_to > $2 OR valid_to IS NULL)`, projectID, base.Add(50*time.Hour)).Scan(&asOfPlan); err != nil {
		t.Fatal(err)
	}
	if err := repo.pool.QueryRow(ctx, `EXPLAIN (FORMAT JSON) SELECT project_version_id
        FROM prompt_better.project_versions WHERE project_id=$1 AND valid_to IS NULL`, projectID).Scan(&currentPlan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asOfPlan, "project_versions_asof_idx") || !strings.Contains(currentPlan, "project_versions_one_current_uq") {
		t.Fatalf("SCD2 query plans missing index: as_of=%q current=%q", asOfPlan, currentPlan)
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
		sourceVersion := "synthetic-v1"
		for i := range observations {
			n := batch*maxUsageBatch + i
			trajectoryID := trajectoryIDs[n%len(trajectoryIDs)]
			value := float64(n % 1000)
			observations[i] = UsageObservation{
				ID: uuidFor(0x62000000, n+1), TrajectoryID: &trajectoryID,
				MetricKind: "input_tokens", Value: &value, UsageUnit: "token",
				ProductSurface: "local", AccountingRegime: "local",
				Provenance: "runtime_observed", SourceAdapter: "synthetic",
				SourceVersion:  &sourceVersion,
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
	if _, err := repo.pool.Exec(ctx, "ANALYZE prompt_better.observations"); err != nil {
		t.Fatal(err)
	}

	var plan []byte
	if err := repo.pool.QueryRow(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
		SELECT observation_id, value_numeric
		FROM prompt_better.observations
		WHERE observation_kind='usage' AND trajectory_id=$1 AND metric_kind='input_tokens'
        ORDER BY observed_at LIMIT 100`, trajectoryIDs[0]).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan), "observations_trajectory_metric_idx") {
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
