//go:build integration

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

func TestConfiguredCollectionIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.OpenMigrationDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := postgres.BootstrapRoles(ctx, db); err != nil {
		t.Fatal(err)
	}
	migrations, err := postgres.NewRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM prompt_better.sources
        WHERE source_id='41000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "git.jsonl")
	initial := `{"version":"1","observed_at":"2026-01-01T00:00:00Z","attributes":{"repository_alias":"synthetic-project-one"}}` + "\n" +
		`{"version":"1","observed_at":"2026-01-01T00:00:01Z","attributes":{"repository_alias":"synthetic-project-two"}}` + "\n"
	if err := os.WriteFile(sourcePath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "collector.json")
	config := `{"owner":"integration","sources":[{"kind":"git","path":"` + sourcePath + `","source_id":"41000000-0000-0000-0000-000000000001","version_id":"41000000-0000-0000-0000-000000000011","cursor_id":"41000000-0000-0000-0000-000000000021","enabled":true,"supported":true}]}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	collectorDSN := os.Getenv("TEST_COLLECTOR_DATABASE_URL")
	if collectorDSN == "" {
		t.Fatal("TEST_COLLECTOR_DATABASE_URL is not set")
	}
	t.Setenv("PROMPT_BETTER_DATABASE_URL", collectorDSN)
	t.Setenv("PROMPT_BETTER_IDENTITY_KEY", "synthetic-integration-key-32-bytes!!")
	var output bytes.Buffer
	if err := run(ctx, []string{"--config", configPath}, &output, func() time.Time {
		return time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"collected":2`) {
		t.Fatalf("unexpected collector output: %s", output.String())
	}
	replacement := `{"version":"2","observed_at":"2026-01-02T00:00:00Z","attributes":{"repository_alias":"synthetic-replacement"}}` + "\n"
	if err := os.WriteFile(sourcePath, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := run(ctx, []string{"--config", configPath}, &output, func() time.Time {
		return time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"collected":1`) {
		t.Fatalf("replacement was not collected once: %s", output.String())
	}
}
