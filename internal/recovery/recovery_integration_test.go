//go:build integration

package recovery

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	store "github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

func TestPostgresEncryptedBackupAndIsolatedRestore(t *testing.T) {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	container := os.Getenv("TEST_POSTGRES_CONTAINER")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const sourceDB = "prompt_better_recovery_source"
	const targetDB = "prompt_better_recovery_target"
	resetRecoveryDatabases(t, ctx, baseURL, container, sourceDB, targetDB)
	sourceURL := databaseURL(t, baseURL, sourceDB)
	targetURL := databaseURL(t, baseURL, targetDB)
	db, err := store.OpenMigrationDB(sourceURL)
	if err != nil {
		t.Fatal(err)
	}
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
	repo, err := store.OpenRepository(ctx, sourceURL, store.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	projectID := "90000000-0000-0000-0000-000000000001"
	if err := repo.PutProject(ctx, store.Project{
		ID: projectID, VersionID: "90000000-0000-0000-0000-000000000002",
		CreatedAt: created, EffectiveAt: created, Classification: "internal",
		LifecycleState: "active",
	}); err != nil {
		t.Fatal(err)
	}
	repo.Close()
	db.Close()

	toolchain := Toolchain{DumpPath: "pg_dump", RestorePath: "pg_restore"}
	if major := os.Getenv("TEST_POSTGRES_MAJOR"); major != "" {
		toolchain.DumpPath = "/usr/lib/postgresql/" + major + "/bin/pg_dump"
		toolchain.RestorePath = "/usr/lib/postgresql/" + major + "/bin/pg_restore"
	}
	dumpTarget, restoreTarget := sourceURL, targetURL
	if container != "" {
		toolchain = Toolchain{
			DumpPath: "docker", DumpPrefix: []string{"exec", container, "pg_dump", "-U", "postgres"},
			RestorePath: "docker", RestorePrefix: []string{"exec", "-i", container, "pg_restore", "-U", "postgres"},
		}
		dumpTarget, restoreTarget = sourceDB, targetDB
	}
	key := bytes.Repeat([]byte{8}, 32)
	archive := t.TempDir() + "/database.pbd"
	metadata, err := toolchain.BackupFile(ctx, dumpTarget, archive, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.PlaintextBytes == 0 {
		t.Fatal("empty database backup")
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(projectID)) {
		t.Fatal("encrypted database backup contains project identity")
	}
	if err := toolchain.RestoreFile(ctx, restoreTarget, archive, key, nil); err != nil {
		t.Fatal(err)
	}
	target, err := sql.Open("pgx", targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var version, projects, versions int
	if err := target.QueryRowContext(ctx, `SELECT COALESCE(max(version_id),0)
        FROM public.schema_migrations WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRowContext(ctx, `SELECT count(*) FROM prompt_better.projects`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRowContext(ctx, `SELECT count(*) FROM prompt_better.project_versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if version != 8 || projects != 1 || versions != 1 {
		t.Fatalf("restored version=%d projects=%d dimensions=%d", version, projects, versions)
	}
}

func resetRecoveryDatabases(t *testing.T, ctx context.Context, baseURL, container string, names ...string) {
	t.Helper()
	if container != "" {
		for _, name := range names {
			_ = exec.CommandContext(ctx, "docker", "exec", container, "dropdb",
				"--if-exists", "-U", "postgres", name).Run()
			if output, err := exec.CommandContext(ctx, "docker", "exec", container,
				"createdb", "-U", "postgres", name).CombinedOutput(); err != nil {
				t.Fatalf("create synthetic database: %v: %s", err, output)
			}
		}
		return
	}
	db, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range names {
		// Names are compile-time synthetic constants, never external input.
		if _, err := db.ExecContext(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
			t.Fatal(err)
		}
	}
}

func databaseURL(t *testing.T, raw, database string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + database
	if !strings.Contains(parsed.RawQuery, "sslmode=") {
		query := parsed.Query()
		query.Set("sslmode", "disable")
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}
