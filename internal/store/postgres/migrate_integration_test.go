//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

func TestMigrationAndRoleBootstrap(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	db, err := OpenMigrationDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
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
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("idempotent up: %v", err)
	}
	version, err := runner.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != 5 {
		t.Fatalf("version = %d, want 5", version)
	}
	assertExists(t, ctx, db, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'prompt_better')")
	assertExists(t, ctx, db, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'prompt_better_runtime')")
	assertExists(t, ctx, db, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_migrations')")
	assertCount(t, ctx, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'prompt_better' AND table_type = 'BASE TABLE'`, 44)
	assertCount(t, ctx, db, `SELECT count(*) FROM information_schema.table_constraints WHERE constraint_schema = 'prompt_better' AND constraint_type = 'FOREIGN KEY'`, 83)
	assertCount(t, ctx, db, `SELECT count(*) FROM information_schema.views WHERE table_schema = 'prompt_better'`, 9)
	t.Run("constraints reject invalid and orphan rows", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.projects
            (project_id, created_at) VALUES
            ('00000000-0000-0000-0000-000000000001', now())
            ON CONFLICT DO NOTHING`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.project_versions
            (project_version_id, project_id, version_number, classification,
             lifecycle_state, version_hash, valid_from)
            VALUES ('00000000-0000-0000-0000-000000000011',
            '00000000-0000-0000-0000-000000000001', 1, 'secret', 'active',
            decode(repeat('00', 32), 'hex'), now())`); err == nil {
			t.Fatal("invalid classification accepted")
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.sessions
            (session_id, source_id, started_at, coverage_state, knowledge_state)
            VALUES ('00000000-0000-0000-0000-000000000002',
            '00000000-0000-0000-0000-000000000099', now(), 'unknown', 'unknown')`); err == nil {
			t.Fatal("orphan source accepted")
		}
	})
	t.Run("least privilege matrix", func(t *testing.T) {
		checks := []struct {
			role, object, privilege string
			want                    bool
		}{
			{"prompt_better_reporter", "prompt_better.rare_cohort_safe_metrics", "SELECT", true},
			{"prompt_better_reporter", "prompt_better.projects", "SELECT", false},
			{"prompt_better_collector", "prompt_better.evidence_artifacts", "INSERT", true},
			{"prompt_better_collector", "prompt_better.key_versions", "SELECT", false},
			{"prompt_better_runtime", "prompt_better.evidence_artifacts", "DELETE", true},
			{"prompt_better_runtime", "prompt_better.key_versions", "UPDATE", false},
			{"prompt_better_migrator", "prompt_better.projects", "DELETE", true},
		}
		for _, check := range checks {
			var got bool
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege($1,$2,$3)`,
				check.role, check.object, check.privilege).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != check.want {
				t.Fatalf("%s %s %s = %t, want %t", check.role, check.privilege,
					check.object, got, check.want)
			}
		}
	})
	t.Run("pre-release down and forward repair", func(t *testing.T) {
		if _, err := runner.provider.DownTo(ctx, 4); err != nil {
			t.Fatal(err)
		}
		assertCount(t, ctx, db, `SELECT count(*) FROM information_schema.views
            WHERE table_schema = 'prompt_better'`, 0)
		if err := runner.Up(ctx); err != nil {
			t.Fatal(err)
		}
		version, err := runner.Version(ctx)
		if err != nil || version != latestSchemaVersion {
			t.Fatalf("repaired version=%d error=%v", version, err)
		}
	})
}

func assertCount(t *testing.T, ctx context.Context, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d for query %q", got, want, query)
	}
}

func assertExists(t *testing.T, ctx context.Context, db *sql.DB, query string) {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(ctx, query).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("expected object missing for query %q", query)
	}
}
