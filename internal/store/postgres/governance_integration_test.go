//go:build integration

package postgres

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/metrics"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestAuditProjectIntegration_RawRatiosUnknownAndOverheadIsolation(t *testing.T) {
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

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	insertGovernanceFixture(t, ctx, repo, start)
	result, err := repo.AuditProject(ctx, contracts.AuditProjectRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request",
		Scope:             contracts.AuditScopeProject,
		Reference:         "f1000000-0000-0000-0000-000000000001",
		ConfiguredSources: []string{"codex_jsonl"},
		StartsAt:          start, EndsAt: start.Add(time.Hour), AsOf: start.Add(time.Hour),
		Consent: contracts.AuditConsentGranted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage != contracts.CoverageStateComplete || len(result.RevisionHash) != 64 {
		t.Fatalf("unexpected audit identity: %#v", result)
	}
	if len(result.Contributions) != 1 || result.Contributions[0].Contribution != nil ||
		result.Contributions[0].AttributionState != "privacy_suppressed" ||
		len(result.Guardrails) != 3 ||
		len(result.Window.SourceVersions) < 1 ||
		len(result.WorkloadEffects) != 1 ||
		result.InvocationCounts.UnknownCalls != 6 {
		t.Fatalf("explainability payload incomplete: %#v", result)
	}
	passive := metricByName(t, result.Metrics, "passive_waits_per_100_tool_calls")
	if passive.NativeValue == nil || math.Abs(*passive.NativeValue-(5.0/6.0*100)) > 0.0001 {
		t.Fatalf("raw passive ratio changed: %#v", passive)
	}
	tokens := metricByName(t, result.Metrics, "tokens_per_turn")
	if tokens.NativeValue == nil || *tokens.NativeValue != 100 {
		t.Fatalf("raw token ratio changed: %#v", tokens)
	}
	if len(result.GovernanceOverhead) == 0 ||
		metricByName(t, result.GovernanceOverhead, "tokens_per_turn").NativeValue == nil {
		t.Fatalf("governance overhead not isolated: %#v", result.GovernanceOverhead)
	}
	if len(result.Findings) != 2 || len(result.Recommendations) != 2 {
		t.Fatalf("detectors/recommendations missing: %#v %#v", result.Findings, result.Recommendations)
	}
	taskResult, err := repo.AuditProject(ctx, contracts.AuditProjectRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request",
		Scope:             contracts.AuditScopeTask,
		Reference:         "f5000000-0000-0000-0000-000000000001",
		ConfiguredSources: []string{"codex_jsonl"},
		StartsAt:          start, EndsAt: start.Add(time.Hour), AsOf: start.Add(time.Hour),
		Consent: contracts.AuditConsentGranted,
	})
	if err != nil || metricByName(t, taskResult.Metrics, "tokens_per_turn").NativeValue != nil ||
		taskResult.Coverage != contracts.CoverageStatePartial {
		t.Fatalf("task audit unavailable: %#v error=%v", taskResult, err)
	}
	if _, err := repo.AuditProject(ctx, contracts.AuditProjectRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request",
		Scope:             contracts.AuditScopeProject,
		Reference:         "f1000000-0000-0000-0000-000000000001",
		ConfiguredSources: []string{"codex_jsonl"},
		StartsAt:          start, EndsAt: start.Add(time.Hour), AsOf: start.Add(time.Hour),
		Consent: contracts.AuditConsentGranted,
	}); err != nil {
		t.Fatalf("idempotent project re-audit failed: %v", err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE prompt_better.recommendations
		SET lifecycle_state='dismissed',cooldown_until=$1
		WHERE action_code='wait_on_state_change'`, start.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	late, err := repo.AuditProject(ctx, contracts.AuditProjectRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request",
		Scope:             contracts.AuditScopeProject,
		Reference:         "f1000000-0000-0000-0000-000000000001",
		ConfiguredSources: []string{"codex_jsonl"},
		StartsAt:          start, EndsAt: start.Add(time.Hour), AsOf: start.Add(2 * time.Hour),
		Consent: contracts.AuditConsentGranted,
	})
	if err != nil || !late.Window.LateEvidence || late.Window.Revision != 2 {
		t.Fatalf("late revision=%#v error=%v", late.Window, err)
	}
	for _, item := range late.Recommendations {
		if item.Code == "wait_on_state_change" {
			t.Fatalf("dismissed recommendation reactivated without new evidence: %#v", late.Recommendations)
		}
	}
	var linkedPrior int
	if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.audit_revisions
		WHERE prior_revision_id IS NOT NULL`).Scan(&linkedPrior); err != nil || linkedPrior != 1 {
		t.Fatalf("late revision linkage=%d error=%v", linkedPrior, err)
	}
	assertGovernanceCount(t, ctx, repo, "metric_definitions", len(metrics.Definitions()))
	assertGovernanceMinimumCount(t, ctx, repo, "audit_windows", 3)
	assertGovernanceCount(t, ctx, repo, "audit_revisions", 4)
	assertGovernanceCount(t, ctx, repo, "metric_results", len(metrics.Definitions())*3)
	assertGovernanceCount(t, ctx, repo, "evaluation_runs", 3)
	assertGovernanceCount(t, ctx, repo, "findings", 6)
	assertGovernanceCount(t, ctx, repo, "recommendations", 4)
}

func TestPersistenceStateRequiresConsecutiveMovement(t *testing.T) {
	t.Parallel()
	definition := metrics.Definitions()[0]
	metric := func(value float64) contracts.MetricResult {
		return contracts.MetricResult{
			Version: metrics.DefinitionVersion, NativeValue: &value,
			Coverage: contracts.CoverageStateComplete,
		}
	}
	if count, hysteresis := persistenceState(metric(80), metric(100), metric(100), definition); count != 1 || hysteresis {
		t.Fatalf("single-window spike count=%d hysteresis=%t", count, hysteresis)
	}
	if count, hysteresis := persistenceState(metric(80), metric(90), metric(100), definition); count != 2 || hysteresis {
		t.Fatalf("persistent movement count=%d hysteresis=%t", count, hysteresis)
	}
	if count, hysteresis := persistenceState(metric(110), metric(90), metric(100), definition); count != 1 || !hysteresis {
		t.Fatalf("reversal count=%d hysteresis=%t", count, hysteresis)
	}
}

func insertGovernanceFixture(t *testing.T, ctx context.Context, repo *Repository, start time.Time) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO prompt_better.projects (project_id,created_at) VALUES ($1,$2)`,
			[]any{"f1000000-0000-0000-0000-000000000001", start}},
		{`INSERT INTO prompt_better.project_versions
			(project_version_id,project_id,version_number,classification,lifecycle_state,version_hash,valid_from)
			VALUES ($1,$2,1,'internal','active',decode(repeat('11',32),'hex'),$3)`,
			[]any{"f1000000-0000-0000-0000-000000000011", "f1000000-0000-0000-0000-000000000001", start}},
		{`INSERT INTO prompt_better.sources (source_id,source_kind,created_at) VALUES ($1,'codex_jsonl',$2)`,
			[]any{"f2000000-0000-0000-0000-000000000001", start}},
		{`INSERT INTO prompt_better.source_versions
			(source_version_id,source_id,version_number,adapter_version,product_surface,coverage_state,enabled,version_hash,valid_from)
			VALUES ($1,$2,1,'1','local','complete',true,decode(repeat('22',32),'hex'),$3)`,
			[]any{"f2000000-0000-0000-0000-000000000011", "f2000000-0000-0000-0000-000000000001", start}},
		{`INSERT INTO prompt_better.sessions
			(session_id,project_id,source_id,started_at,coverage_state,knowledge_state)
			VALUES ($1,$2,$3,$4,'complete','observed')`,
			[]any{"f3000000-0000-0000-0000-000000000001", "f1000000-0000-0000-0000-000000000001", "f2000000-0000-0000-0000-000000000001", start}},
		{`INSERT INTO prompt_better.trajectories
			(trajectory_id,session_id,source_id,started_at,knowledge_state)
			VALUES ($1,$2,$3,$4,'observed')`,
			[]any{"f4000000-0000-0000-0000-000000000001", "f3000000-0000-0000-0000-000000000001", "f2000000-0000-0000-0000-000000000001", start}},
		{`INSERT INTO prompt_better.tasks
			(task_id,project_id,source_id,task_kind,attribution_state,
			 algorithm_version,observed_at,knowledge_state)
			VALUES ($1,$2,$3,'codex_task','attributed','attribution-v1',$4,'observed')`,
			[]any{"f5000000-0000-0000-0000-000000000001", "f1000000-0000-0000-0000-000000000001", "f2000000-0000-0000-0000-000000000001", start}},
		{`INSERT INTO prompt_better.turns
			(turn_id,trajectory_id,source_id,task_id,ordinal,observed_at,knowledge_state)
			VALUES ($1,$2,$3,$4,1,$5,'observed')`,
			[]any{"f6000000-0000-0000-0000-000000000001", "f4000000-0000-0000-0000-000000000001", "f2000000-0000-0000-0000-000000000001", "f5000000-0000-0000-0000-000000000001", start.Add(time.Minute)}},
		{`INSERT INTO prompt_better.responses
			(response_id,turn_id,source_id,started_at,completed_at,knowledge_state)
			VALUES ($1,$2,$3,$4,$5,'observed')`,
			[]any{"f7000000-0000-0000-0000-000000000001", "f6000000-0000-0000-0000-000000000001", "f2000000-0000-0000-0000-000000000001", start.Add(time.Minute), start.Add(2 * time.Minute)}},
		{`INSERT INTO prompt_better.tool_calls
			(tool_call_id,response_id,source_id,call_path,tool_kind,started_at,outcome,knowledge_state)
			VALUES ($1,$2,$3,'direct','passive_wait',$4,'repeated_unchanged','observed')`,
			[]any{"f8000000-0000-0000-0000-000000000001", "f7000000-0000-0000-0000-000000000001", "f2000000-0000-0000-0000-000000000001", start.Add(time.Minute)}},
		{`INSERT INTO prompt_better.tool_calls
			(tool_call_id,response_id,source_id,call_path,tool_kind,started_at,outcome,knowledge_state)
			SELECT id,$1,$2,'direct','passive_wait',$3,'repeated_unchanged','observed'
			FROM unnest(ARRAY[
				'f8000000-0000-0000-0000-000000000002'::uuid,
				'f8000000-0000-0000-0000-000000000003'::uuid,
				'f8000000-0000-0000-0000-000000000004'::uuid,
				'f8000000-0000-0000-0000-000000000005'::uuid]) id`,
			[]any{"f7000000-0000-0000-0000-000000000001", "f2000000-0000-0000-0000-000000000001", start.Add(time.Minute)}},
		{`INSERT INTO prompt_better.state_epochs
			(state_epoch_id,trajectory_id,source_id,state_hash,mutation_state,started_at,knowledge_state)
			VALUES ($1,$2,$3,decode(repeat('55',32),'hex'),'mutated',$4,'observed')`,
			[]any{"f8500000-0000-0000-0000-000000000001", "f4000000-0000-0000-0000-000000000001", "f2000000-0000-0000-0000-000000000001", start.Add(time.Minute)}},
		{`INSERT INTO prompt_better.tool_calls
			(tool_call_id,response_id,source_id,call_path,tool_kind,started_at,outcome,
			 knowledge_state,state_epoch_id)
			VALUES ($1,$2,$3,'direct','validation',$4,'success','observed',$5)`,
			[]any{"f8600000-0000-0000-0000-000000000001", "f7000000-0000-0000-0000-000000000001", "f2000000-0000-0000-0000-000000000001", start.Add(time.Minute), "f8500000-0000-0000-0000-000000000001"}},
		{`INSERT INTO prompt_better.evidence_artifacts
			(evidence_artifact_id,source_id,project_id,session_id,schema_version,content_hash,content_length,classification,redaction_state,coverage_state,provenance,product_surface,observed_at)
			VALUES ($1,$2,$3,$4,'1.0.0',decode(repeat('33',32),'hex'),10,'internal','not_needed','complete','runtime_observed','local',$5)`,
			[]any{"f9000000-0000-0000-0000-000000000001", "f2000000-0000-0000-0000-000000000001", "f1000000-0000-0000-0000-000000000001", "f3000000-0000-0000-0000-000000000001", start.Add(time.Minute)}},
		{`INSERT INTO prompt_better.usage_observations
			(usage_observation_id,trajectory_id,evidence_artifact_id,metric_kind,value_numeric,usage_unit,product_surface,accounting_regime,provenance,source_adapter,observed_at,knowledge_state)
			VALUES ($1,$2,$3,'total_tokens',100,'tokens','local','native','runtime_observed','synthetic',$4,'observed')`,
			[]any{"fa000000-0000-0000-0000-000000000001", "f4000000-0000-0000-0000-000000000001", "f9000000-0000-0000-0000-000000000001", start.Add(time.Minute)}},
		{`INSERT INTO prompt_better.audit_windows
			(audit_window_id,project_id,window_kind,starts_at,ends_at,as_of,timezone_name,immutable_since)
			VALUES ($1,$2,'synthetic',$3,$4,$4,'UTC',$4)`,
			[]any{"fb000000-0000-0000-0000-000000000001", "f1000000-0000-0000-0000-000000000001", start, start.Add(time.Hour)}},
		{`INSERT INTO prompt_better.audit_revisions
			(audit_revision_id,audit_window_id,revision_number,source_watermark_at,coverage_state,revision_hash,created_at)
			VALUES ($1,$2,1,$3,'complete',decode(repeat('44',32),'hex'),$3)`,
			[]any{"fc000000-0000-0000-0000-000000000001", "fb000000-0000-0000-0000-000000000001", start.Add(time.Hour)}},
		{`INSERT INTO prompt_better.governance_overhead
			(governance_overhead_id,audit_revision_id,trajectory_id,overhead_kind,native_value,native_unit,required_state,observed_at)
			VALUES ($1,$2,$3,'audit',10,'tokens','necessary',$4)`,
			[]any{"fd000000-0000-0000-0000-000000000001", "fc000000-0000-0000-0000-000000000001", "f4000000-0000-0000-0000-000000000001", start.Add(time.Minute)}},
	}
	for _, statement := range statements {
		if _, err := repo.pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("fixture insert failed: %v", err)
		}
	}
}

func metricByName(t *testing.T, metrics []contracts.MetricResult, name string) contracts.MetricResult {
	t.Helper()
	for _, metric := range metrics {
		if metric.Name == name {
			return metric
		}
	}
	t.Fatalf("metric %q missing", name)
	return contracts.MetricResult{}
}

func assertGovernanceCount(t *testing.T, ctx context.Context, repo *Repository,
	table string, want int,
) {
	t.Helper()
	var got int
	if err := repo.pool.QueryRow(ctx,
		`SELECT count(*) FROM prompt_better.`+table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count=%d want=%d", table, got, want)
	}
}

func assertGovernanceMinimumCount(t *testing.T, ctx context.Context, repo *Repository,
	table string, want int,
) {
	t.Helper()
	var got int
	if err := repo.pool.QueryRow(ctx,
		`SELECT count(*) FROM prompt_better.`+table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got < want {
		t.Fatalf("%s count=%d want-at-least=%d", table, got, want)
	}
}
