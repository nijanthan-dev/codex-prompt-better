//go:build integration

package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nijanthan-dev/codex-prompt-better/internal/identity"
)

func TestRepositoryIntegration(t *testing.T) {
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

	t.Run("doctor reports sanitized readiness", func(t *testing.T) {
		result := repo.Doctor(ctx)
		if !result.Ready || result.SchemaVersion != latestSchemaVersion || result.ServerVersion < 160000 {
			t.Fatalf("unexpected doctor result: %+v", result)
		}
		if len(result.Problems) != 0 || result.Role == "" {
			t.Fatalf("unexpected doctor diagnostics: %+v", result)
		}
	})

	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	project := Project{
		ID: "10000000-0000-0000-0000-000000000001", VersionID: "10000000-0000-0000-0000-000000000011",
		CreatedAt: base, EffectiveAt: base, Classification: "internal", LifecycleState: "active",
	}
	t.Run("project SCD2 is idempotent and historical", func(t *testing.T) {
		if err := repo.PutProject(ctx, project); err != nil {
			t.Fatal(err)
		}
		identical := project
		identical.VersionID = "10000000-0000-0000-0000-000000000012"
		identical.EffectiveAt = base.Add(time.Hour)
		if err := repo.PutProject(ctx, identical); err != nil {
			t.Fatal(err)
		}
		changed := project
		changed.VersionID = "10000000-0000-0000-0000-000000000013"
		changed.Classification = "restricted"
		changed.EffectiveAt = base.Add(2 * time.Hour)
		if err := repo.PutProject(ctx, changed); err != nil {
			t.Fatal(err)
		}
		var versionCount int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.project_versions
            WHERE project_id = $1`, project.ID).Scan(&versionCount); err != nil {
			t.Fatal(err)
		}
		if versionCount != 2 {
			t.Fatalf("project versions = %d, want 2", versionCount)
		}
		var historical, current string
		if err := repo.pool.QueryRow(ctx, `SELECT classification FROM prompt_better.project_versions
            WHERE project_id=$1 AND valid_from <= $2 AND (valid_to > $2 OR valid_to IS NULL)`,
			project.ID, base.Add(time.Hour)).Scan(&historical); err != nil {
			t.Fatal(err)
		}
		if err := repo.pool.QueryRow(ctx, `SELECT classification FROM prompt_better.current_project_dimensions
            WHERE project_id=$1`, project.ID).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if historical != "internal" || current != "restricted" {
			t.Fatalf("historical=%q current=%q", historical, current)
		}
	})

	source := Source{
		ID: "20000000-0000-0000-0000-000000000001", VersionID: "20000000-0000-0000-0000-000000000011",
		Kind: "synthetic_jsonl", ProductSurface: "local", CoverageState: "complete",
		Enabled: true, CreatedAt: base, EffectiveAt: base,
	}
	t.Run("source, cursor, evidence, and coverage", func(t *testing.T) {
		if err := repo.PutSource(ctx, source); err != nil {
			t.Fatal(err)
		}
		if err := repo.AdvanceSequenceCursor(ctx, "30000000-0000-0000-0000-000000000001", source.ID, 1, base); err != nil {
			t.Fatal(err)
		}
		if err := repo.AdvanceSequenceCursor(ctx, "30000000-0000-0000-0000-000000000002", source.ID, 1, base); !errors.Is(err, ErrStaleCursor) {
			t.Fatalf("duplicate cursor error = %v", err)
		}
		coverage, err := repo.GetProjectCoverage(ctx, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if coverage.CoverageRatio != nil || coverage.DenominatorState != "missing" {
			t.Fatalf("unexpected empty coverage: %+v", coverage)
		}
		hash := sha256.Sum256([]byte("synthetic evidence"))
		alias := "30000000-0000-0000-0000-000000000011"
		evidence := Evidence{
			ID: "30000000-0000-0000-0000-000000000021", SourceID: source.ID,
			ProjectID: &project.ID, ExternalAliasID: &alias, SchemaVersion: "1.0.0",
			ContentHash: hash[:], ContentLength: 18, Classification: "internal",
			RedactionState: "not_needed", CoverageState: "complete",
			Provenance: "runtime_observed", ProductSurface: "local", ObservedAt: base,
		}
		if err := repo.PutEvidence(ctx, evidence); err != nil {
			t.Fatal(err)
		}
		if err := repo.PutEvidence(ctx, evidence); err != nil {
			t.Fatal(err)
		}
		conflict := evidence
		other := sha256.Sum256([]byte("different synthetic evidence"))
		conflict.ContentHash = other[:]
		if err := repo.PutEvidence(ctx, conflict); !errors.Is(err, ErrSourceConflict) {
			t.Fatalf("conflict error = %v", err)
		}
		coverage, err = repo.GetProjectCoverage(ctx, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if coverage.CoverageRatio == nil || *coverage.CoverageRatio != 1 {
			t.Fatalf("unexpected observed coverage: %+v", coverage)
		}
	})

	t.Run("keyed alias rotation loss and deletion", func(t *testing.T) {
		active := base
		firstKeyID := "35000000-0000-0000-0000-000000000001"
		secondKeyID := "35000000-0000-0000-0000-000000000002"
		if err := repo.PutKeyVersion(ctx, KeyVersion{ID: firstKeyID,
			Reference: "synthetic-identity-v1", State: "active", CreatedAt: base,
			ActivatedAt: &active}); err != nil {
			t.Fatal(err)
		}
		if err := repo.PutKeyVersion(ctx, KeyVersion{ID: secondKeyID,
			Reference: "synthetic-identity-v2", State: "active", CreatedAt: base.Add(time.Hour),
			ActivatedAt: &active}); err != nil {
			t.Fatal(err)
		}
		identifier := []byte("synthetic-private-project")
		firstDigest, err := identity.Alias(bytes.Repeat([]byte{1}, 32), identifier)
		if err != nil {
			t.Fatal(err)
		}
		oldAlias := ProjectAlias{ID: "35000000-0000-0000-0000-000000000011",
			ProjectID: project.ID, SourceID: source.ID, KeyVersionID: firstKeyID,
			Kind: "repository", Digest: firstDigest[:], CreatedAt: base}
		if err := repo.PutProjectAlias(ctx, oldAlias); err != nil {
			t.Fatal(err)
		}
		secondDigest, err := identity.Alias(bytes.Repeat([]byte{2}, 32), identifier)
		if err != nil {
			t.Fatal(err)
		}
		newAlias := ProjectAlias{ID: "35000000-0000-0000-0000-000000000012",
			ProjectID: project.ID, SourceID: source.ID, KeyVersionID: secondKeyID,
			Kind: "repository", Digest: secondDigest[:], CreatedAt: base.Add(time.Hour)}
		if err := repo.RekeyProjectAlias(ctx, oldAlias.ID, newAlias); err != nil {
			t.Fatal(err)
		}
		if err := repo.MarkKeyLost(ctx, firstKeyID, base.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := repo.DeleteKeyVersion(ctx, secondKeyID, base.Add(3*time.Hour)); err != nil {
			t.Fatal(err)
		}
		var aliasCount int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.project_aliases
            WHERE project_id=$1`, project.ID).Scan(&aliasCount); err != nil {
			t.Fatal(err)
		}
		if aliasCount != 0 {
			t.Fatalf("aliases after key deletion = %d", aliasCount)
		}
	})

	t.Run("callback failure rolls back", func(t *testing.T) {
		err := repo.WithSerializable(ctx, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, `INSERT INTO prompt_better.projects (project_id, created_at)
                VALUES ('40000000-0000-0000-0000-000000000001', $1)`, base)
			if execErr != nil {
				return execErr
			}
			return errors.New("synthetic rollback")
		})
		if err == nil || !strings.Contains(err.Error(), "synthetic rollback") {
			t.Fatalf("rollback error = %v", err)
		}
		var count int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.projects
            WHERE project_id='40000000-0000-0000-0000-000000000001'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("rolled-back project persisted")
		}
	})

	t.Run("concurrent identical SCD2 change leaves one current row", func(t *testing.T) {
		changes := []Project{project, project}
		for i := range changes {
			changes[i].VersionID = []string{
				"50000000-0000-0000-0000-000000000001",
				"50000000-0000-0000-0000-000000000002",
			}[i]
			changes[i].Classification = "confidential"
			changes[i].EffectiveAt = base.Add(3 * time.Hour)
		}
		var wg sync.WaitGroup
		errs := make(chan error, len(changes))
		for _, change := range changes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- repo.PutProject(ctx, change)
			}()
		}
		wg.Wait()
		close(errs)
		successes := 0
		for err := range errs {
			if err == nil {
				successes++
			}
		}
		if successes < 1 {
			t.Fatal("no concurrent change succeeded")
		}
		var current int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.project_versions
            WHERE project_id=$1 AND valid_to IS NULL`, project.ID).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if current != 1 {
			t.Fatalf("current SCD2 rows = %d", current)
		}
	})
}

func prepareDatabase(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	db, err := OpenMigrationDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := BootstrapRoles(ctx, db); err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
}
