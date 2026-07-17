//go:build integration

package postgres

import (
	"context"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/audit"
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
	auditSource := projectAuditSource{query: repo.pool}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	insertGovernanceFixture(t, ctx, repo, start)
	projectRequest := func(asOf time.Time) contracts.AuditProjectRequest {
		return contracts.AuditProjectRequest{
			SchemaVersion: contracts.SchemaVersion, Kind: "request",
			Scope:             contracts.AuditScopeProject,
			Reference:         "f1000000-0000-0000-0000-000000000001",
			ConfiguredSources: []string{"codex_jsonl"},
			StartsAt:          start, EndsAt: start.Add(2 * time.Hour), AsOf: asOf,
			Consent: contracts.AuditConsentGranted,
		}
	}
	t.Run("single connection pool", func(t *testing.T) {
		limits := DefaultPoolConfig()
		limits.MaxConnections = 1
		limits.MinConnections = 1
		singleConnectionRepo, err := OpenRepository(ctx, dsn, limits)
		if err != nil {
			t.Fatal(err)
		}
		defer singleConnectionRepo.Close()
		result, err := singleConnectionRepo.AuditProject(ctx, projectRequest(start.Add(time.Hour)))
		if err != nil {
			t.Fatal(err)
		}
		if result.Window.Revision != 1 {
			t.Fatalf("unexpected audit revision: %d", result.Window.Revision)
		}
	})
	result, err := repo.AuditProject(ctx, projectRequest(start.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage != contracts.CoverageStateComplete || len(result.RevisionHash) != 64 {
		t.Fatalf("unexpected audit identity: %#v", result)
	}
	if result.ReportFacts == nil || len(result.ReportFacts.Sources) != 1 ||
		result.ReportFacts.Sources[0].SourceKind != "codex_jsonl" ||
		result.ReportFacts.Sources[0].Version == "1" ||
		result.ReportFacts.Sources[0].Freshness != "current" ||
		result.ReportFacts.Sources[0].RedactionState != "complete" ||
		result.ReportFacts.Sources[0].KnowledgeState != "observed" ||
		result.ReportFacts.ScopeCounts.IncludedTrajectories != 1 ||
		result.ReportFacts.ScopeCounts.IncludedTurns != 1 ||
		result.ReportFacts.ScopeCounts.IncludedEvidence == 0 ||
		result.ReportFacts.ScopeCounts.ExcludedTrajectories == nil ||
		*result.ReportFacts.ScopeCounts.ExcludedTrajectories != 0 {
		t.Fatalf("normalized report source handoff incomplete: %#v", result.ReportFacts)
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
	if taskResult.ReportFacts == nil || !taskResult.ReportFacts.PrivacySuppressed ||
		taskResult.ReportFacts.DisplayIdentity == "f5000000-0000-0000-0000-000000000001" ||
		taskResult.ReportFacts.ScopeCounts.ExcludedTrajectories != nil {
		t.Fatalf("task report suppression unavailable: %#v", taskResult.ReportFacts)
	}
	if _, err := repo.AuditProject(ctx, projectRequest(start.Add(time.Hour))); err != nil {
		t.Fatalf("idempotent project re-audit failed: %v", err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE prompt_better.recommendations
		SET lifecycle_state='dismissed',cooldown_until=$1,updated_at=$2
		WHERE action_code='wait_on_state_change'`,
		start.Add(24*time.Hour), start.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `INSERT INTO prompt_better.evidence_artifacts
		(evidence_artifact_id,source_id,project_id,session_id,schema_version,
		 content_hash,content_length,classification,redaction_state,coverage_state,
		 provenance,product_surface,observed_at)
		VALUES ('f9000000-0000-0000-0000-000000000002',
		 'f2000000-0000-0000-0000-000000000001',
		 'f1000000-0000-0000-0000-000000000001',
		 'f3000000-0000-0000-0000-000000000001',
		 '1.0.0',decode(repeat('34',32),'hex'),10,'internal','not_needed',
		 'complete','runtime_observed','local',$1)`,
		start.Add(90*time.Minute)); err != nil {
		t.Fatal(err)
	}
	late, err := repo.AuditProject(ctx, projectRequest(start.Add(2*time.Hour)))
	if err != nil || !late.Window.LateEvidence || late.Window.Revision != 2 {
		t.Fatalf("late revision=%#v error=%v", late.Window, err)
	}
	if !hasRecommendation(late.Recommendations, "wait_on_state_change") {
		t.Fatalf("dismissed recommendation not reactivated by new evidence: %#v", late.Recommendations)
	}
	noNewEvidence, err := repo.AuditProject(ctx, projectRequest(start.Add(3*time.Hour)))
	if err != nil || noNewEvidence.Window.LateEvidence {
		t.Fatalf("watermark-only revision marked late=%#v error=%v", noNewEvidence.Window, err)
	}
	historicalRequest := projectRequest(start.Add(time.Hour))
	historical, err := repo.AuditProject(ctx, historicalRequest)
	if err != nil ||
		historical.Window.Revision != result.Window.Revision ||
		historical.RevisionHash != result.RevisionHash ||
		historical.Window.LateEvidence {
		t.Fatalf("historical replay revision=%#v error=%v", historical.Window, err)
	}
	repeatedHistorical, err := repo.AuditProject(ctx, historicalRequest)
	if err != nil ||
		repeatedHistorical.Window.Revision != historical.Window.Revision ||
		repeatedHistorical.Window.LateEvidence ||
		repeatedHistorical.RevisionHash != historical.RevisionHash {
		t.Fatalf("repeated historical revision=%#v error=%v", repeatedHistorical.Window, err)
	}
	var persistedIdentity int
	if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.audit_revisions
		WHERE audit_window_id=$1 AND revision_number=$2 AND encode(revision_hash,'hex')=$3
		  AND engine_version=$4`,
		collectionBatchUUID(auditWindowKey(historicalRequest)),
		historical.Window.Revision,
		historical.RevisionHash,
		audit.EngineVersion,
	).Scan(&persistedIdentity); err != nil || persistedIdentity != 1 {
		t.Fatalf("persisted historical identity=%d error=%v", persistedIdentity, err)
	}
	var linkedPrior int
	if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.audit_revisions
		WHERE prior_revision_id IS NOT NULL`).Scan(&linkedPrior); err != nil || linkedPrior != 2 {
		t.Fatalf("late revision linkage=%d error=%v", linkedPrior, err)
	}
	assertGovernanceCount(t, ctx, repo, "metric_definitions", len(metrics.Definitions()))
	assertGovernanceMinimumCount(t, ctx, repo, "audit_windows", 3)
	assertGovernanceCount(t, ctx, repo, "audit_revisions", 5)
	assertGovernanceCount(t, ctx, repo, "metric_results", len(metrics.Definitions())*4)
	assertGovernanceCount(t, ctx, repo, "evaluation_runs", 4)
	assertGovernanceCount(t, ctx, repo, "findings", 8)
	assertGovernanceCount(t, ctx, repo, "recommendations", 7)
	var noActionCount int
	if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.recommendations
		WHERE action_code='no_action'`).Scan(&noActionCount); err != nil || noActionCount == 0 {
		t.Fatalf("persisted no_action recommendations=%d error=%v", noActionCount, err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE prompt_better.recommendations recommendation
		SET action_code='wait_on_state_change',
			recommendation_kind='wait_on_state_change',lifecycle_state='dismissed',
			updated_at=$1,cooldown_until=$2
		FROM prompt_better.findings finding
		JOIN prompt_better.audit_revisions revision USING (audit_revision_id)
		JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
		WHERE recommendation.finding_id=finding.finding_id
		  AND audit_window.window_kind='task'
		  AND recommendation.action_code='no_action'`,
		start.Add(4*time.Hour), start.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	feedbackRequest := projectRequest(start.Add(5 * time.Hour))
	projectFeedback, err := auditSource.recommendationContext(ctx, feedbackRequest)
	if err != nil {
		t.Fatal(err)
	}
	if projectFeedback.Feedback["passive_polling"].State == "dismissed" {
		t.Fatalf("task recommendation leaked into project feedback: %#v", projectFeedback)
	}
	if _, err := repo.pool.Exec(ctx, `WITH ranked AS (
		SELECT recommendation.recommendation_id,
			row_number() OVER (ORDER BY audit_window.as_of DESC,
				revision.revision_number DESC,recommendation.recommendation_id DESC) AS position
		FROM prompt_better.recommendations recommendation
		JOIN prompt_better.findings finding USING (finding_id)
		JOIN prompt_better.audit_revisions revision USING (audit_revision_id)
		JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
		WHERE recommendation.action_code='wait_on_state_change'
		  AND audit_window.window_kind='project'
	)
	UPDATE prompt_better.recommendations recommendation
	SET lifecycle_state=CASE WHEN ranked.position=1 THEN 'dismissed' ELSE 'accepted' END,
		updated_at=$1,cooldown_until=$2
	FROM ranked WHERE ranked.recommendation_id=recommendation.recommendation_id`,
		start.Add(4*time.Hour), start.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	projectFeedback, err = auditSource.recommendationContext(ctx, feedbackRequest)
	if err != nil || projectFeedback.Feedback["passive_polling"].State != "dismissed" {
		t.Fatalf("latest tied project feedback not deterministic: %#v error=%v", projectFeedback, err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE prompt_better.recommendations recommendation
		SET action_code='no_action',recommendation_kind='no_action'
		WHERE recommendation.recommendation_id=(
			SELECT candidate.recommendation_id
			FROM prompt_better.recommendations candidate
			JOIN prompt_better.findings finding USING (finding_id)
			JOIN prompt_better.audit_revisions revision USING (audit_revision_id)
			JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
			WHERE candidate.action_code='wait_on_state_change'
			  AND audit_window.window_kind='project'
			ORDER BY audit_window.as_of DESC,revision.revision_number DESC
			LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `DELETE FROM prompt_better.recommendations
		WHERE action_code<>'no_action'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE prompt_better.recommendations
		SET action_code=NULL WHERE recommendation_kind='no_action'`); err != nil {
		t.Fatal(err)
	}
	feedback, err := auditSource.recommendationContext(ctx, feedbackRequest)
	if err != nil || len(feedback.Feedback) != 0 || len(feedback.ExistingRuleCoverage) != 0 {
		t.Fatalf("no_action leaked into feedback: %#v error=%v", feedback, err)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE prompt_better.recommendations
		SET recommendation_kind='wait_on_state_change'
		WHERE recommendation_kind='no_action'`); err != nil {
		t.Fatal(err)
	}
	feedback, err = auditSource.recommendationContext(ctx, feedbackRequest)
	if err != nil || len(feedback.Feedback) != 1 {
		t.Fatalf("legacy actionable feedback missing: %#v error=%v", feedback, err)
	}
	if _, err := repo.pool.Exec(ctx, `INSERT INTO prompt_better.trajectories
		(trajectory_id,session_id,source_id,started_at,knowledge_state)
		VALUES ('f4000000-0000-0000-0000-000000000099',
		'f3000000-0000-0000-0000-000000000001',
		'f2000000-0000-0000-0000-000000000001',$1,'observed')`,
		start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `INSERT INTO prompt_better.evidence_artifacts
		(evidence_artifact_id,source_id,project_id,session_id,schema_version,
		 content_hash,content_length,classification,redaction_state,coverage_state,
		 provenance,product_surface,observed_at)
		VALUES ('f9000000-0000-0000-0000-000000000099',
		'f2000000-0000-0000-0000-000000000001',
		'f1000000-0000-0000-0000-000000000001',
		'f3000000-0000-0000-0000-000000000001','1.0.0',
		decode(repeat('99',32),'hex'),10,'internal','unknown','complete',
		'runtime_observed','local',$1)`, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `INSERT INTO prompt_better.evidence_links
		(evidence_link_id,evidence_artifact_id,target_kind,target_id,link_kind,created_at)
		VALUES ('f9100000-0000-0000-0000-000000000099',
		'f9000000-0000-0000-0000-000000000099','trajectory',
		'f4000000-0000-0000-0000-000000000099','observed_in',$1)`,
		start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `INSERT INTO prompt_better.governance_overhead
		(governance_overhead_id,audit_revision_id,trajectory_id,overhead_kind,
		 native_value,native_unit,required_state,observed_at)
		VALUES ('fd000000-0000-0000-0000-000000000099',
		'fc000000-0000-0000-0000-000000000001',
		'f4000000-0000-0000-0000-000000000099','audit',90,'tokens','necessary',$1)`,
		start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	taskRequest := projectRequest(start.Add(time.Hour))
	taskRequest.Scope = contracts.AuditScopeTask
	taskRequest.Reference = "f5000000-0000-0000-0000-000000000001"
	filter, turnFilter, args, err := auditFilter(taskRequest)
	if err != nil {
		t.Fatal(err)
	}
	overhead, err := auditSource.overhead(ctx, filter, args)
	if err != nil {
		t.Fatal(err)
	}
	if overhead.TotalTokens != 10 || overhead.CompletedTurns != 1 {
		t.Fatalf("task overhead leaked sibling trajectory: %#v", overhead)
	}
	if _, err := repo.pool.Exec(ctx, `INSERT INTO prompt_better.tool_calls
		(tool_call_id,response_id,source_id,external_alias_id,caller_alias_id,
		 call_path,tool_kind,started_at,outcome,knowledge_state)
		VALUES ('f8000000-0000-0000-0000-000000000099',
		'f7000000-0000-0000-0000-000000000001',
		'f2000000-0000-0000-0000-000000000001',
		'f8100000-0000-0000-0000-000000000099',
		'f8100000-0000-0000-0000-000000000098',
		'programmatic','synthetic',$1,'success','observed')`,
		start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	counts, err := auditSource.invocationCounts(
		ctx, filter, turnFilter, args,
	)
	if err != nil {
		t.Fatal(err)
	}
	if counts.HostCalls != 1 || counts.LeafCalls != 1 ||
		counts.HostResults != 1 || counts.LeafResults != 1 || counts.UnknownCalls != 6 {
		t.Fatalf("bounded orphan-parent invocation counts: %#v", counts)
	}
	taskMetrics, err := auditSource.metrics(ctx, filter, turnFilter, args)
	if err != nil {
		t.Fatal(err)
	}
	if len(taskMetrics.EvidenceRefs) != 1 ||
		taskMetrics.EvidenceRefs[0] != "f9000000-0000-0000-0000-000000000001" {
		t.Fatalf("task evidence leaked sibling trajectory: %#v", taskMetrics.EvidenceRefs)
	}
	taskSources, err := auditSource.reportSources(ctx, taskRequest, filter, args)
	if err != nil || len(taskSources) != 1 || taskSources[0].RedactionState != "complete" {
		t.Fatalf("task source facts leaked sibling evidence: %#v error=%v", taskSources, err)
	}
	concurrentRequests := []contracts.AuditProjectRequest{
		projectRequest(start.Add(4 * time.Hour)),
		projectRequest(start.Add(5 * time.Hour)),
	}
	results := make([]contracts.AuditProjectResult, 2)
	auditErrors := make([]error, 2)
	var group sync.WaitGroup
	for index := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			results[index], auditErrors[index] = repo.AuditProject(ctx, concurrentRequests[index])
		}()
	}
	group.Wait()
	for index, auditErr := range auditErrors {
		if auditErr != nil {
			t.Fatalf("concurrent audit %d: %v", index, auditErr)
		}
	}
	revisionDelta := results[0].Window.Revision - results[1].Window.Revision
	if results[0].RevisionHash == results[1].RevisionHash ||
		(revisionDelta != 1 && revisionDelta != -1) {
		t.Fatalf("concurrent audit revisions not serialized: %#v %#v", results[0].Window, results[1].Window)
	}

	tag, err := repo.pool.Exec(ctx, `UPDATE prompt_better.metric_definitions
		SET definition_hash=decode(repeat('ff',32),'hex')
		WHERE metric_name='scope_attribution_coverage' AND metric_version=$1`,
		metrics.DefinitionVersion)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("mutated metric definitions=%d", tag.RowsAffected())
	}
	_, err = repo.AuditProject(ctx, projectRequest(start.Add(time.Hour)))
	if err == nil || err.Error() != "metric definition version conflicts with persisted content" {
		t.Fatalf("definition drift error=%v", err)
	}
	failedConn, err := repo.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var backendPID int
	if err := failedConn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID); err != nil {
		failedConn.Release()
		t.Fatal(err)
	}
	failedLockKey := "synthetic-failed-unlock"
	if _, err := failedConn.Exec(ctx,
		`SELECT pg_advisory_lock(hashtextextended($1,0))`, failedLockKey,
	); err != nil {
		failedConn.Release()
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, `SELECT pg_terminate_backend($1)`, backendPID); err != nil {
		failedConn.Release()
		t.Fatal(err)
	}
	releaseAuditConnection(ctx, failedConn, failedLockKey)
	healthyConn, err := repo.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("pool did not replace failed audit connection: %v", err)
	}
	defer healthyConn.Release()
	if err := healthyConn.QueryRow(ctx, `SELECT 1`).Scan(new(int)); err != nil {
		t.Fatalf("replacement audit connection unusable: %v", err)
	}
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
		{`INSERT INTO prompt_better.evidence_links
			(evidence_link_id,evidence_artifact_id,target_kind,target_id,link_kind,created_at)
			VALUES ($1,$2,'trajectory',$3,'observed_in',$4)`,
			[]any{
				"f9100000-0000-0000-0000-000000000001",
				"f9000000-0000-0000-0000-000000000001",
				"f4000000-0000-0000-0000-000000000001",
				start.Add(time.Minute),
			}},
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

func hasRecommendation(recommendations []contracts.AuditRecommendation, code string) bool {
	for _, recommendation := range recommendations {
		if recommendation.Code == code {
			return true
		}
	}
	return false
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
