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
	assertCount(t, ctx, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'prompt_better' AND table_type = 'BASE TABLE'`, 47)
	assertCount(t, ctx, db, `SELECT count(*) FROM information_schema.table_constraints WHERE constraint_schema = 'prompt_better' AND constraint_type = 'FOREIGN KEY'`, 90)
	assertCount(t, ctx, db, `SELECT count(*) FROM information_schema.views WHERE table_schema = 'prompt_better'`, 9)
	t.Run("trajectory evidence upgrade backfill", func(t *testing.T) {
		if _, err := runner.provider.DownTo(ctx, 9); err != nil {
			t.Fatal(err)
		}
		if _, err := runner.provider.DownTo(ctx, 8); err != nil {
			t.Fatal(err)
		}
		statements := []string{
			`INSERT INTO prompt_better.sources (source_id,source_kind,created_at)
			 VALUES ('10000000-0000-0000-0000-000000000001','synthetic',now())`,
			`INSERT INTO prompt_better.projects (project_id,created_at)
			 VALUES ('10000000-0000-0000-0000-000000000002',now())`,
			`INSERT INTO prompt_better.sessions
			 (session_id,project_id,source_id,started_at,coverage_state,knowledge_state)
			 VALUES ('10000000-0000-0000-0000-000000000003',
			 '10000000-0000-0000-0000-000000000002',
			 '10000000-0000-0000-0000-000000000001',now(),'complete','observed')`,
			`INSERT INTO prompt_better.trajectories
			 (trajectory_id,session_id,source_id,started_at,knowledge_state)
			 VALUES ('10000000-0000-0000-0000-000000000004',
			 '10000000-0000-0000-0000-000000000003',
			 '10000000-0000-0000-0000-000000000001',now(),'observed')`,
			`INSERT INTO prompt_better.evidence_artifacts
			 (evidence_artifact_id,source_id,project_id,session_id,schema_version,
			 content_hash,content_length,classification,redaction_state,coverage_state,
			 provenance,product_surface,observed_at)
			 VALUES ('10000000-0000-0000-0000-000000000005',
			 '10000000-0000-0000-0000-000000000001',
			 '10000000-0000-0000-0000-000000000002',
			 '10000000-0000-0000-0000-000000000003','1.0.0',
			 decode(repeat('11',32),'hex'),1,'internal','not_needed','complete',
			 'runtime_observed','local',now())`,
			`INSERT INTO prompt_better.usage_observations
			 (usage_observation_id,trajectory_id,evidence_artifact_id,metric_kind,
			 value_numeric,usage_unit,product_surface,accounting_regime,provenance,
			 source_adapter,observed_at,knowledge_state)
			 VALUES ('10000000-0000-0000-0000-000000000008',
			 '10000000-0000-0000-0000-000000000004',
			 '10000000-0000-0000-0000-000000000005','total_tokens',1,'tokens',
			 'local','native','runtime_observed','synthetic',now(),'observed')`,
		}
		for _, statement := range statements {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := runner.Up(ctx); err != nil {
			t.Fatal(err)
		}
		assertCount(t, ctx, db, `SELECT count(*) FROM prompt_better.evidence_links
			WHERE evidence_artifact_id='10000000-0000-0000-0000-000000000005'
			  AND target_kind='trajectory'
			  AND target_id='10000000-0000-0000-0000-000000000004'`, 1)
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.sessions
			(session_id,project_id,source_id,started_at,coverage_state,knowledge_state)
			VALUES ('10000000-0000-0000-0000-000000000009',
			'10000000-0000-0000-0000-000000000002',
			'10000000-0000-0000-0000-000000000001',now(),'complete','observed')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.evidence_artifacts
			(evidence_artifact_id,source_id,project_id,session_id,schema_version,
			 content_hash,content_length,classification,redaction_state,coverage_state,
			 provenance,product_surface,observed_at)
			VALUES ('10000000-0000-0000-0000-000000000010',
			'10000000-0000-0000-0000-000000000001',
			'10000000-0000-0000-0000-000000000002',
			'10000000-0000-0000-0000-000000000003','1.0.0',
			decode(repeat('33',32),'hex'),1,'internal','not_needed','partial',
			'runtime_observed','local',now())`); err != nil {
			t.Fatal(err)
		}
		var sessions, evidence, complete int
		if err := db.QueryRowContext(ctx, `SELECT session_count,evidence_count,
			complete_evidence_count FROM prompt_better.project_coverage
			WHERE project_id='10000000-0000-0000-0000-000000000002'`).
			Scan(&sessions, &evidence, &complete); err != nil || sessions != 2 || evidence != 2 || complete != 1 {
			t.Fatalf("project coverage sessions=%d evidence=%d complete=%d error=%v", sessions, evidence, complete, err)
		}
	})
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
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.audit_windows
			(audit_window_id,project_id,window_kind,starts_at,ends_at,as_of,
			 timezone_name,immutable_since)
			VALUES ('10000000-0000-0000-0000-000000000006',
			'10000000-0000-0000-0000-000000000002','synthetic',
			now()-interval '1 hour',now(),now(),'UTC',now())`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO prompt_better.audit_revisions
			(audit_revision_id,audit_window_id,revision_number,source_watermark_at,
			 coverage_state,revision_hash,created_at,engine_version)
			VALUES ('10000000-0000-0000-0000-000000000007',
			'10000000-0000-0000-0000-000000000006',1,now(),'complete',
			decode(repeat('22',32),'hex'),now(),'audit-v2')`); err != nil {
			t.Fatal(err)
		}
		if _, err := runner.provider.DownTo(ctx, 9); err != nil {
			t.Fatal(err)
		}
		if _, err := runner.provider.DownTo(ctx, 8); err != nil {
			t.Fatal(err)
		}
		if _, err := runner.provider.DownTo(ctx, 4); err == nil {
			t.Fatal("migration 8 down accepted non-v1 audit history")
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM prompt_better.audit_windows
			WHERE audit_window_id IN (
				SELECT audit_window_id FROM prompt_better.audit_revisions
				WHERE engine_version <> 'audit-v1')`); err != nil {
			t.Fatal(err)
		}
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
