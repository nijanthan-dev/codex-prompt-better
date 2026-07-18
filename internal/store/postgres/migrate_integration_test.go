//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
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
	if version != LatestSchemaVersion {
		t.Fatalf("version = %d, want %d", version, LatestSchemaVersion)
	}
	var engineDefault string
	if err := db.QueryRowContext(ctx, `SELECT column_default
		FROM information_schema.columns
		WHERE table_schema='prompt_better' AND table_name='audit_revisions'
		  AND column_name='engine_version'`).Scan(&engineDefault); err != nil ||
		engineDefault != "'audit-v1'::text" {
		t.Fatalf("engine version default=%q error=%v", engineDefault, err)
	}
	assertExists(t, ctx, db, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'prompt_better')")
	assertExists(t, ctx, db, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'prompt_better_runtime')")
	assertExists(t, ctx, db, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_migrations')")
	assertCount(t, ctx, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'prompt_better' AND table_type = 'BASE TABLE'`, 32)
	assertCount(t, ctx, db, `SELECT count(*) FROM information_schema.table_constraints WHERE constraint_schema = 'prompt_better' AND constraint_type = 'FOREIGN KEY'`, 70)
	assertCount(t, ctx, db, `SELECT count(*) FROM information_schema.views WHERE table_schema = 'prompt_better'`, 10)
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
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.sources
			(source_id,source_kind,created_at) VALUES
			('00000000-0000-0000-0000-000000000006','synthetic',now())`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.evidence_artifacts
			(evidence_artifact_id,source_id,project_id,schema_version,content_hash,
			 content_length,classification,redaction_state,coverage_state,provenance,
			 product_surface,observed_at)
			VALUES ('00000000-0000-0000-0000-000000000007',
			 '00000000-0000-0000-0000-000000000006',
			 '00000000-0000-0000-0000-000000000001','1',
			 decode(repeat('01',32),'hex'),1,'internal','not_needed','complete',
			 'synthetic','local',now())`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.sessions
            (session_id, source_id, started_at, coverage_state, knowledge_state)
            VALUES ('00000000-0000-0000-0000-000000000002',
            '00000000-0000-0000-0000-000000000099', now(), 'unknown', 'unknown')`); err == nil {
			t.Fatal("orphan source accepted")
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,schema_version,evidence_artifact_id,attributes,
			 observed_at,knowledge_state)
			VALUES ('00000000-0000-0000-0000-000000000003','plan_snapshot','1',
			 '00000000-0000-0000-0000-000000000007','{}',now(),'observed')`); err == nil {
			t.Fatal("incomplete plan snapshot accepted")
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,outcome,evidence_artifact_id,
			 attributes,observed_at,knowledge_state)
			VALUES ('00000000-0000-0000-0000-000000000004','boundary','runtime','1','allowed',
			 '00000000-0000-0000-0000-000000000007',
			 jsonb_build_object('payload',repeat('x',5000)),now(),'observed')`); err == nil {
			t.Fatal("oversized event attributes accepted")
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,outcome,attributes,
			 observed_at,knowledge_state)
			VALUES ('00000000-0000-0000-0000-000000000008','boundary','runtime','1',
			 'allowed','{}',now(),'observed')`); err == nil {
			t.Fatal("unowned execution event accepted")
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.observations
			(observation_id,observation_kind,schema_version,provenance,attributes,observed_at,knowledge_state)
			VALUES ('00000000-0000-0000-0000-000000000005','usage','1','synthetic','{}',now(),'observed')`); err == nil {
			t.Fatal("incomplete usage observation accepted")
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
			{"prompt_better_runtime", "public.schema_migrations", "SELECT", true},
			{"prompt_better_collector", "public.schema_migrations", "SELECT", true},
			{"prompt_better_reporter", "public.schema_migrations", "SELECT", false},
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
		runtimeConfig := DefaultPoolConfig()
		runtimeConfig.ExpectedRole = RoleRuntime
		runtimeRepo, err := OpenRepository(ctx, integrationRoleDSN(t, RoleRuntime), runtimeConfig)
		if err != nil {
			t.Fatal(err)
		}
		if doctor := runtimeRepo.Doctor(ctx); !doctor.Ready || doctor.Role != string(RoleRuntime) {
			t.Fatalf("runtime role doctor=%+v", doctor)
		}
		runtimeRepo.Close()
		if repo, err := OpenRepository(ctx, integrationRoleDSN(t, RoleCollector), runtimeConfig); err == nil {
			repo.Close()
			t.Fatal("collector credentials accepted by runtime path")
		}
		if repo, err := OpenRepository(ctx, superuserRoleDSN(t, dsn, RoleRuntime), runtimeConfig); err == nil {
			repo.Close()
			t.Fatal("superuser login accepted by runtime path")
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
		if err != nil || version != LatestSchemaVersion {
			t.Fatalf("repaired version=%d error=%v", version, err)
		}
	})
	t.Run("legacy schema fails closed with reset guidance", func(t *testing.T) {
		if _, err := runner.provider.DownTo(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO public.schema_migrations
			(version_id,is_applied) VALUES (10,true)`); err != nil {
			t.Fatal(err)
		}
		if err := runner.Up(ctx); !errors.Is(err, ErrSchemaResetRequired) {
			t.Fatalf("migration error = %v, want reset guidance", err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM public.schema_migrations WHERE version_id=10`); err != nil {
			t.Fatal(err)
		}
		if err := runner.Up(ctx); err != nil {
			t.Fatalf("restore current baseline: %v", err)
		}
	})
}

func integrationRoleDSN(t *testing.T, role DatabaseRole) string {
	t.Helper()
	name := "TEST_RUNTIME_DATABASE_URL"
	if role == RoleCollector {
		name = "TEST_COLLECTOR_DATABASE_URL"
	}
	dsn := os.Getenv(name)
	if dsn == "" {
		t.Fatalf("%s is not set", name)
	}
	return dsn
}

func superuserRoleDSN(t *testing.T, dsn string, role DatabaseRole) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("options", "-c role="+string(role))
	parsed.RawQuery = query.Encode()
	return parsed.String()
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
