//go:build integration

package retention

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	store "github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

func TestThirtyDayArchiveGatedRetention(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.OpenMigrationDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.BootstrapRoles(ctx, db); err != nil {
		t.Fatal(err)
	}
	runner, err := store.NewRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	repo, err := store.OpenRepository(ctx, dsn, store.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	asOf := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	projectID := "80000000-0000-0000-0000-000000000001"
	sourceID := "80000000-0000-0000-0000-000000000002"
	policyID := "80000000-0000-0000-0000-000000000003"
	if err := repo.PutProject(ctx, store.Project{
		ID: projectID, VersionID: "80000000-0000-0000-0000-000000000004",
		CreatedAt: asOf.Add(-60 * 24 * time.Hour), EffectiveAt: asOf.Add(-60 * 24 * time.Hour),
		Classification: "internal", LifecycleState: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutSource(ctx, store.Source{
		ID: sourceID, VersionID: "80000000-0000-0000-0000-000000000005",
		Kind: "synthetic_jsonl", ProductSurface: "local", CoverageState: "complete",
		Enabled: true, CreatedAt: asOf.Add(-60 * 24 * time.Hour),
		EffectiveAt: asOf.Add(-60 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutRetentionPolicy(ctx, store.RetentionPolicy{
		ID: policyID, ProjectID: projectID, Version: "1.0.0",
		Classification: "internal", RetainFor: store.DefaultRetention,
		CreatedAt: asOf.Add(-60 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	oldID := "80000000-0000-0000-0000-000000000010"
	youngID := "80000000-0000-0000-0000-000000000011"
	sessionID := "82000000-0000-0000-0000-000000000001"
	putRetentionSession(t, ctx, db, projectID, sourceID, sessionID, asOf)
	putRetentionEvidence(t, ctx, repo, projectID, sourceID, sessionID, oldID,
		asOf.Add(-31*24*time.Hour), "old")
	putRetentionEvidence(t, ctx, repo, projectID, sourceID, sessionID, youngID,
		asOf.Add(-29*24*time.Hour), "young")
	putDerivedRetentionRows(t, ctx, db, projectID, oldID, youngID, asOf)
	putNormalizedRetentionRows(t, ctx, db, projectID, sourceID, oldID, youngID, asOf)
	if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.key_versions
        (key_version_id,key_reference,algorithm,state,created_at)
        VALUES ('80000000-0000-0000-0000-000000000020','synthetic-key-v1',
                'hmac-sha256','active',$1)`, asOf.Add(-60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.project_aliases
        (project_alias_id,project_id,source_id,key_version_id,alias_kind,
         alias_digest,created_at) VALUES
        ('80000000-0000-0000-0000-000000000021',$1,$2,
         '80000000-0000-0000-0000-000000000020','repository',
         decode(repeat('ab',32),'hex'),$3)`, projectID, sourceID,
		asOf.Add(-60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	plan, err := repo.PlanRetention(ctx, policyID, asOf, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Records) != 1 || plan.Records[0].EvidenceID != oldID {
		t.Fatalf("30-day plan = %+v", plan.Records)
	}
	archiver, err := NewFileArchiver(t.TempDir(), "archive-key-v1", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := archiver.Archive(ctx,
		"80000000-0000-0000-0000-000000000030", plan)
	if err != nil {
		t.Fatal(err)
	}
	staleApply := store.RetentionApply{
		ActionID:       "80000000-0000-0000-0000-000000000031",
		ActionEntityID: "80000000-0000-0000-0000-000000000033",
		Plan:           plan, Receipt: receipt,
	}
	if _, err := db.ExecContext(ctx, `UPDATE prompt_better.evidence_artifacts
		SET observed_at=observed_at+interval '1 minute' WHERE evidence_artifact_id=$1`, oldID); err != nil {
		t.Fatal(err)
	}
	if applied, err := repo.ApplyRetention(ctx, staleApply); err == nil || applied != 0 {
		t.Fatalf("stale archived metadata applied=%d error=%v", applied, err)
	}
	plan, err = repo.PlanRetention(ctx, policyID, asOf, 100)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err = archiver.Archive(ctx,
		"80000000-0000-0000-0000-000000000034", plan)
	if err != nil {
		t.Fatal(err)
	}
	apply := store.RetentionApply{
		ActionID:       "80000000-0000-0000-0000-000000000031",
		ActionEntityID: "80000000-0000-0000-0000-000000000033",
		Plan:           plan, Receipt: receipt,
	}
	mutated := apply
	mutated.Plan.Records = append([]store.ArchiveRecord{}, plan.Records...)
	mutated.Plan.Records[0].ContentLength++
	if applied, err := repo.ApplyRetention(ctx, mutated); err == nil || applied != 0 {
		t.Fatalf("mutated archived plan applied=%d error=%v", applied, err)
	}
	forgedReference := apply
	forgedReference.Receipt.ArchiveReference = "sha256:" + strings.Repeat("00", sha256.Size)
	if applied, err := repo.ApplyRetention(ctx, forgedReference); err == nil || applied != 0 {
		t.Fatalf("forged archive reference applied=%d error=%v", applied, err)
	}
	counts := make([]int64, 2)
	errorsByApply := make([]error, 2)
	var group sync.WaitGroup
	for index := range counts {
		group.Add(1)
		go func() {
			defer group.Done()
			counts[index], errorsByApply[index] = repo.ApplyRetention(ctx, apply)
		}()
	}
	group.Wait()
	for index := range counts {
		if errorsByApply[index] != nil || counts[index] != 1 {
			t.Fatalf("concurrent idempotent apply %d count=%d error=%v", index, counts[index], errorsByApply[index])
		}
	}
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.evidence_artifacts
        WHERE project_id=$1`, projectID, 1)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.project_aliases
        WHERE project_id=$1`, projectID, 1)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.audit_revisions revision
		JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
		WHERE audit_window.project_id=$1`, projectID, 2)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.metric_results result
		JOIN prompt_better.audit_revisions revision USING (audit_revision_id)
		JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
		WHERE audit_window.project_id=$1`, projectID, 1)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.findings finding
		JOIN prompt_better.audit_revisions revision USING (audit_revision_id)
		JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
		WHERE audit_window.project_id=$1`, projectID, 1)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.recommendations
		WHERE project_id=$1`, projectID, 2)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.recommendation_events event
		JOIN prompt_better.recommendations recommendation USING (recommendation_id)
		WHERE recommendation.project_id=$1`, projectID, 1)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.observations observation
		JOIN prompt_better.audit_revisions revision USING (audit_revision_id)
		JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
		WHERE audit_window.project_id=$1`, projectID, 1)
	assertNormalizedLineageCount(t, ctx, db, projectID, 1)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.execution_events
		WHERE project_id=$1`, projectID, 1)

	if err := repo.PutRetentionPolicy(ctx, store.RetentionPolicy{
		ID: policyID, ProjectID: projectID, Version: "1.0.1", Classification: "internal",
		RetainFor: store.DefaultRetention, LegalHold: true, CreatedAt: asOf,
	}); err != nil {
		t.Fatal(err)
	}
	heldPlan, err := repo.PlanRetention(ctx, policyID, asOf.Add(2*24*time.Hour), 100)
	if err != nil || len(heldPlan.Records) != 0 {
		t.Fatalf("legal-hold plan=%+v error=%v", heldPlan.Records, err)
	}
	if err := repo.PutRetentionPolicy(ctx, store.RetentionPolicy{
		ID: policyID, ProjectID: projectID, Version: "1.0.2", Classification: "internal",
		RetainFor: store.DefaultRetention, CreatedAt: asOf,
	}); err != nil {
		t.Fatal(err)
	}
	secondPlan, err := repo.PlanRetention(ctx, policyID, asOf.Add(2*24*time.Hour), 100)
	if err != nil || len(secondPlan.Records) != 1 || secondPlan.Records[0].EvidenceID != youngID {
		t.Fatalf("second plan=%+v error=%v", secondPlan.Records, err)
	}
	secondReceipt, err := archiver.Archive(ctx,
		"80000000-0000-0000-0000-000000000040", secondPlan)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := repo.ApplyRetention(ctx, store.RetentionApply{
		ActionID:       "80000000-0000-0000-0000-000000000041",
		ActionEntityID: "80000000-0000-0000-0000-000000000043",
		Plan:           secondPlan, Receipt: secondReceipt,
	}); err != nil || applied != 1 {
		t.Fatalf("second apply count=%d error=%v", applied, err)
	}
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.evidence_artifacts
        WHERE project_id=$1`, projectID, 0)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.project_aliases
        WHERE project_id=$1`, projectID, 0)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.audit_revisions revision
		JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
		WHERE audit_window.project_id=$1`, projectID, 0)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.metric_results result
		JOIN prompt_better.audit_revisions revision USING (audit_revision_id)
		JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
		WHERE audit_window.project_id=$1`, projectID, 0)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.findings finding
		JOIN prompt_better.audit_revisions revision USING (audit_revision_id)
		JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
		WHERE audit_window.project_id=$1`, projectID, 0)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.recommendations
		WHERE project_id=$1`, projectID, 0)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.recommendation_events event
		JOIN prompt_better.recommendations recommendation USING (recommendation_id)
		WHERE recommendation.project_id=$1`, projectID, 0)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.observations observation
		JOIN prompt_better.audit_revisions revision USING (audit_revision_id)
		JOIN prompt_better.audit_windows audit_window USING (audit_window_id)
		WHERE audit_window.project_id=$1`, projectID, 0)
	assertNormalizedLineageCount(t, ctx, db, projectID, 0)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.execution_events
		WHERE project_id=$1`, projectID, 0)
	if err := repo.MaintainAfterRetention(ctx); err != nil {
		t.Fatal(err)
	}
	var maintainedTables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_user_tables
		WHERE schemaname='prompt_better'
		  AND relname IN ('evidence_artifacts','evidence_links','observations')
		  AND last_vacuum IS NOT NULL`).Scan(&maintainedTables); err != nil {
		t.Fatal(err)
	}
	if maintainedTables != 3 {
		t.Fatalf("maintained tables = %d, want 3", maintainedTables)
	}
}

func putRetentionSession(t *testing.T, ctx context.Context, db *sql.DB,
	projectID, sourceID, sessionID string, asOf time.Time) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.sessions
		(session_id,project_id,source_id,started_at,coverage_state,knowledge_state)
		VALUES ($1,$2,$3,$4,'complete','observed')`, sessionID, projectID, sourceID,
		asOf.Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func putNormalizedRetentionRows(t *testing.T, ctx context.Context, db *sql.DB,
	projectID, sourceID, expiredEvidenceID, retainedEvidenceID string, asOf time.Time) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO prompt_better.trajectories
			(trajectory_id,session_id,source_id,started_at,knowledge_state)
			VALUES ('82000000-0000-0000-0000-000000000002',
			 '82000000-0000-0000-0000-000000000001',$1,$2,'observed')`,
			[]any{sourceID, asOf.Add(-31 * 24 * time.Hour)}},
		{`INSERT INTO prompt_better.tasks
			(task_id,project_id,source_id,task_kind,attribution_state,
			 algorithm_version,observed_at,knowledge_state)
			VALUES ('82000000-0000-0000-0000-000000000003',$1,$2,'codex_task',
			 'attributed','attribution-v1',$3,'observed')`,
			[]any{projectID, sourceID, asOf.Add(-31 * 24 * time.Hour)}},
		{`INSERT INTO prompt_better.state_epochs
			(state_epoch_id,trajectory_id,source_id,state_hash,mutation_state,
			 started_at,knowledge_state)
			VALUES ('82000000-0000-0000-0000-000000000004',
			 '82000000-0000-0000-0000-000000000002',$1,
			 decode(repeat('ef',32),'hex'),'mutated',$2,'observed')`,
			[]any{sourceID, asOf.Add(-31 * 24 * time.Hour)}},
		{`INSERT INTO prompt_better.turns
			(turn_id,trajectory_id,task_id,source_id,ordinal,observed_at,knowledge_state)
			VALUES ('82000000-0000-0000-0000-000000000005',
			 '82000000-0000-0000-0000-000000000002',
			 '82000000-0000-0000-0000-000000000003',$1,0,$2,'observed')`,
			[]any{sourceID, asOf.Add(-31 * 24 * time.Hour)}},
		{`INSERT INTO prompt_better.responses
			(response_id,turn_id,source_id,started_at,knowledge_state)
			VALUES ('82000000-0000-0000-0000-000000000006',
			 '82000000-0000-0000-0000-000000000005',$1,$2,'observed')`,
			[]any{sourceID, asOf.Add(-31 * 24 * time.Hour)}},
		{`INSERT INTO prompt_better.items
			(item_id,response_id,source_id,item_kind,ordinal,observed_at,knowledge_state)
			VALUES ('82000000-0000-0000-0000-000000000007',
			 '82000000-0000-0000-0000-000000000006',$1,'message',0,$2,'observed')`,
			[]any{sourceID, asOf.Add(-31 * 24 * time.Hour)}},
		{`INSERT INTO prompt_better.phases
			(phase_id,trajectory_id,response_id,phase_kind,ordinal,started_at,knowledge_state)
			VALUES ('82000000-0000-0000-0000-000000000008',
			 '82000000-0000-0000-0000-000000000002',
			 '82000000-0000-0000-0000-000000000006','tool',0,$1,'observed')`,
			[]any{asOf.Add(-31 * 24 * time.Hour)}},
		{`INSERT INTO prompt_better.tool_calls
			(tool_call_id,response_id,phase_id,source_id,call_path,tool_kind,started_at,knowledge_state)
			VALUES ('82000000-0000-0000-0000-000000000009',
			 '82000000-0000-0000-0000-000000000006',
			 '82000000-0000-0000-0000-000000000008',$1,'direct','synthetic',$2,'observed')`,
			[]any{sourceID, asOf.Add(-31 * 24 * time.Hour)}},
		{`INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,project_id,session_id,
			 evidence_artifact_id,outcome,observed_at,knowledge_state)
			VALUES
			('82000000-0000-0000-0000-000000000010','boundary','runtime','1',$1,
			 '82000000-0000-0000-0000-000000000001',$2,'allowed',$4,'observed'),
			('82000000-0000-0000-0000-000000000011','boundary','runtime','1',$1,
			 '82000000-0000-0000-0000-000000000001',$3,'allowed',$4,'observed')`,
			[]any{projectID, expiredEvidenceID, retainedEvidenceID, asOf.Add(-31 * 24 * time.Hour)}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func putDerivedRetentionRows(t *testing.T, ctx context.Context, db *sql.DB,
	projectID, expiredEvidenceID, retainedEvidenceID string, asOf time.Time) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO prompt_better.audit_windows
            (audit_window_id,project_id,window_kind,starts_at,ends_at,as_of)
            VALUES ('81000000-0000-0000-0000-000000000001',$1,'synthetic',$2,$3,$3)`,
			[]any{projectID, asOf.Add(-40 * 24 * time.Hour), asOf.Add(-39 * 24 * time.Hour)}},
		{`INSERT INTO prompt_better.audit_revisions
            (audit_revision_id,audit_window_id,revision_number,source_watermark_at,
             coverage_state,revision_hash,created_at) VALUES
            ('81000000-0000-0000-0000-000000000002','81000000-0000-0000-0000-000000000001',
			 1,$1,'complete',decode(repeat('cd',32),'hex'),$1),
			('81000000-0000-0000-0000-000000000014','81000000-0000-0000-0000-000000000001',
			 2,$1,'complete',decode(repeat('ce',32),'hex'),$1),
			('81000000-0000-0000-0000-000000000017','81000000-0000-0000-0000-000000000001',
			 3,$1,'complete',decode(repeat('cf',32),'hex'),$1)`, []any{asOf}},
		{`INSERT INTO prompt_better.metric_results
            (metric_result_id,audit_revision_id,metric_name,metric_version,native_unit,
             coverage_state,provenance,computed_at) VALUES
            ('81000000-0000-0000-0000-000000000003','81000000-0000-0000-0000-000000000002',
             'synthetic','1.0.0','count','complete','runtime_observed',$1)`, []any{asOf}},
		{`INSERT INTO prompt_better.findings
            (finding_id,audit_revision_id,finding_kind,detector_name,detector_version,status,created_at)
            VALUES ('81000000-0000-0000-0000-000000000004',
             '81000000-0000-0000-0000-000000000002','synthetic','synthetic','1.0.0','open',$1)`, []any{asOf}},
		{`INSERT INTO prompt_better.recommendations
			(recommendation_id,audit_revision_id,finding_id,project_id,recommendation_kind,lifecycle_state,
			 approval_required,created_at,updated_at) VALUES
			('81000000-0000-0000-0000-000000000005','81000000-0000-0000-0000-000000000002',
			 '81000000-0000-0000-0000-000000000004',$1,'synthetic','proposed',true,$2,$2),
			('81000000-0000-0000-0000-000000000012','81000000-0000-0000-0000-000000000002',
			 NULL,$1,'no_action','proposed',false,$2,$2),
			('81000000-0000-0000-0000-000000000015','81000000-0000-0000-0000-000000000014',
			 NULL,$1,'no_action','proposed',false,$2,$2)`, []any{projectID, asOf}},
		{`INSERT INTO prompt_better.recommendation_events
			(recommendation_event_id,recommendation_id,event_kind,observed_at,evidence_artifact_id)
			VALUES ('81000000-0000-0000-0000-000000000013',
			 '81000000-0000-0000-0000-000000000005','verified',$3,$2),
			('81000000-0000-0000-0000-000000000016',
			 '81000000-0000-0000-0000-000000000015','verified',$3,$1)`,
			[]any{expiredEvidenceID, retainedEvidenceID, asOf}},
		{`INSERT INTO prompt_better.observations
			(observation_id,observation_kind,schema_version,audit_revision_id,
			 evidence_artifact_id,metric_kind,state_value,provenance,observed_at,knowledge_state)
			VALUES
			('81000000-0000-0000-0000-000000000018','confounder','1',
			 '81000000-0000-0000-0000-000000000017',$1,'synthetic','present',
			 'runtime_observed',$3,'observed'),
			('81000000-0000-0000-0000-000000000019','confounder','1',
			 '81000000-0000-0000-0000-000000000017',$2,'synthetic','present',
			 'runtime_observed',$3,'observed')`,
			[]any{expiredEvidenceID, retainedEvidenceID, asOf}},
		{`INSERT INTO prompt_better.evidence_links
			(evidence_link_id,evidence_artifact_id,target_kind,target_id,link_kind,created_at)
			VALUES
			('81000000-0000-0000-0000-000000000006',$1,'audit_revision',
			 '81000000-0000-0000-0000-000000000002','supports',$3),
			('81000000-0000-0000-0000-000000000007',$2,'audit_revision',
			 '81000000-0000-0000-0000-000000000002','supports',$3),
			('81000000-0000-0000-0000-000000000008',$1,'metric_result',
			 '81000000-0000-0000-0000-000000000003','supports',$3),
			('81000000-0000-0000-0000-000000000009',$2,'metric_result',
			 '81000000-0000-0000-0000-000000000003','supports',$3),
			('81000000-0000-0000-0000-000000000010',$1,'finding',
			 '81000000-0000-0000-0000-000000000004','supports',$3),
			('81000000-0000-0000-0000-000000000011',$2,'finding',
			 '81000000-0000-0000-0000-000000000004','supports',$3)`,
			[]any{expiredEvidenceID, retainedEvidenceID, asOf}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func putRetentionEvidence(t *testing.T, ctx context.Context, repo *store.Repository,
	projectID, sourceID, sessionID, id string, observedAt time.Time, content string) {
	t.Helper()
	hash := sha256.Sum256([]byte(content))
	if err := repo.PutEvidence(ctx, store.Evidence{
		ID: id, SourceID: sourceID, ProjectID: &projectID, SessionID: &sessionID,
		SchemaVersion: "1.0.0",
		ContentHash:   hash[:], ContentLength: int64(len(content)),
		Classification: "internal", RedactionState: "not_needed",
		CoverageState: "complete", Provenance: "runtime_observed",
		ProductSurface: "local", ObservedAt: observedAt,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertSQLCount(t *testing.T, ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query, projectID string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, query, projectID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count=%d want=%d", got, want)
	}
}

func assertNormalizedLineageCount(t *testing.T, ctx context.Context, db *sql.DB,
	projectID string, want int) {
	t.Helper()
	queries := []string{
		`SELECT count(*) FROM prompt_better.sessions WHERE project_id=$1`,
		`SELECT count(*) FROM prompt_better.tasks WHERE project_id=$1`,
		`SELECT count(*) FROM prompt_better.trajectories trajectory
			JOIN prompt_better.sessions session USING (session_id) WHERE session.project_id=$1`,
		`SELECT count(*) FROM prompt_better.turns turn_row
			JOIN prompt_better.trajectories trajectory USING (trajectory_id)
			JOIN prompt_better.sessions session USING (session_id) WHERE session.project_id=$1`,
		`SELECT count(*) FROM prompt_better.responses response
			JOIN prompt_better.turns turn_row USING (turn_id)
			JOIN prompt_better.trajectories trajectory USING (trajectory_id)
			JOIN prompt_better.sessions session USING (session_id) WHERE session.project_id=$1`,
		`SELECT count(*) FROM prompt_better.items item
			JOIN prompt_better.responses response USING (response_id)
			JOIN prompt_better.turns turn_row USING (turn_id)
			JOIN prompt_better.trajectories trajectory USING (trajectory_id)
			JOIN prompt_better.sessions session USING (session_id) WHERE session.project_id=$1`,
		`SELECT count(*) FROM prompt_better.phases phase
			JOIN prompt_better.trajectories trajectory USING (trajectory_id)
			JOIN prompt_better.sessions session USING (session_id) WHERE session.project_id=$1`,
		`SELECT count(*) FROM prompt_better.state_epochs epoch
			JOIN prompt_better.trajectories trajectory USING (trajectory_id)
			JOIN prompt_better.sessions session USING (session_id) WHERE session.project_id=$1`,
		`SELECT count(*) FROM prompt_better.tool_calls tool_call
			JOIN prompt_better.responses response USING (response_id)
			JOIN prompt_better.turns turn_row USING (turn_id)
			JOIN prompt_better.trajectories trajectory USING (trajectory_id)
			JOIN prompt_better.sessions session USING (session_id) WHERE session.project_id=$1`,
	}
	for _, query := range queries {
		assertSQLCount(t, ctx, db, query, projectID, want)
	}
}
