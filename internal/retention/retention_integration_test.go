//go:build integration

package retention

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
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
	putRetentionEvidence(t, ctx, repo, projectID, sourceID, oldID,
		asOf.Add(-31*24*time.Hour), "old")
	putRetentionEvidence(t, ctx, repo, projectID, sourceID, youngID,
		asOf.Add(-29*24*time.Hour), "young")
	putDerivedRetentionRows(t, ctx, db, projectID, oldID, asOf)
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
	apply := store.RetentionApply{
		ActionID:        "80000000-0000-0000-0000-000000000031",
		DeletionAuditID: "80000000-0000-0000-0000-000000000032",
		ArchiveEntityID: "80000000-0000-0000-0000-000000000033",
		Plan:            plan, Receipt: receipt,
	}
	if applied, err := repo.ApplyRetention(ctx, apply); err != nil || applied != 1 {
		t.Fatalf("apply count=%d error=%v", applied, err)
	}
	if applied, err := repo.ApplyRetention(ctx, apply); err != nil || applied != 1 {
		t.Fatalf("idempotent apply count=%d error=%v", applied, err)
	}
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.evidence_artifacts
        WHERE project_id=$1`, projectID, 1)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.project_aliases
        WHERE project_id=$1`, projectID, 1)
	for _, table := range []string{"audit_revisions", "metric_results", "findings", "recommendations"} {
		assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.`+table+
			` WHERE $1::uuid IS NOT NULL`, projectID, 0)
	}

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
		ActionID:        "80000000-0000-0000-0000-000000000041",
		DeletionAuditID: "80000000-0000-0000-0000-000000000042",
		ArchiveEntityID: "80000000-0000-0000-0000-000000000043",
		Plan:            secondPlan, Receipt: secondReceipt,
	}); err != nil || applied != 1 {
		t.Fatalf("second apply count=%d error=%v", applied, err)
	}
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.evidence_artifacts
        WHERE project_id=$1`, projectID, 0)
	assertSQLCount(t, ctx, db, `SELECT count(*) FROM prompt_better.project_aliases
        WHERE project_id=$1`, projectID, 0)
	if err := repo.MaintainAfterRetention(ctx); err != nil {
		t.Fatal(err)
	}
}

func putDerivedRetentionRows(t *testing.T, ctx context.Context, db *sql.DB,
	projectID, evidenceID string, asOf time.Time) {
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
             1,$1,'complete',decode(repeat('cd',32),'hex'),$1)`, []any{asOf}},
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
            (recommendation_id,finding_id,project_id,recommendation_kind,lifecycle_state,
             approval_required,created_at,updated_at) VALUES
            ('81000000-0000-0000-0000-000000000005','81000000-0000-0000-0000-000000000004',
             $1,'synthetic','proposed',true,$2,$2)`, []any{projectID, asOf}},
		{`INSERT INTO prompt_better.evidence_links
            (evidence_link_id,evidence_artifact_id,target_kind,target_id,link_kind,created_at)
            VALUES ('81000000-0000-0000-0000-000000000006',$1,'audit_revision',
             '81000000-0000-0000-0000-000000000002','supports',$2)`, []any{evidenceID, asOf}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func putRetentionEvidence(t *testing.T, ctx context.Context, repo *store.Repository,
	projectID, sourceID, id string, observedAt time.Time, content string) {
	t.Helper()
	hash := sha256.Sum256([]byte(content))
	if err := repo.PutEvidence(ctx, store.Evidence{
		ID: id, SourceID: sourceID, ProjectID: &projectID, SchemaVersion: "1.0.0",
		ContentHash: hash[:], ContentLength: int64(len(content)),
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
