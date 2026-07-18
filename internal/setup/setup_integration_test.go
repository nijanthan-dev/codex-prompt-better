//go:build integration

package setup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

func TestDoctor_PostgreSQLFixtureReady(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	t.Setenv(databaseEnv, setupRoleDSN(t))
	checks := doctorChecks(context.Background(), t.TempDir(), t.TempDir()+"/integration.json", &fakeRunner{})
	for _, check := range checks {
		if check.Name == "database" {
			if check.State != "ready" {
				t.Fatalf("database state=%s", check.State)
			}
			return
		}
	}
	t.Fatal("database check missing")
}

func TestDoctor_PostgreSQLConfiguredCollectorReady(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	repository, err := postgres.OpenRepository(ctx, dsn, postgres.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := repository.PutSource(ctx, postgres.Source{
		ID: "00000000-0000-4000-8000-000000000071", VersionID: "00000000-0000-4000-8000-000000000072",
		Kind: "git", ProductSurface: "local", CoverageState: "complete", Enabled: true,
		CreatedAt: now, EffectiveAt: now,
	}); err != nil {
		repository.Close()
		t.Fatal(err)
	}
	repository.Close()

	home := t.TempDir()
	configPath := filepath.Join(home, "integration.json")
	config := IntegrationConfig{
		SchemaVersion: configVersion, ExecutionPolicy: "improve_only", SourceKinds: []string{"git"},
		RawPromptRetention: false, Telemetry: false, DatabaseEnv: databaseEnv,
	}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(databaseEnv, setupRoleDSN(t))
	checks := doctorChecks(ctx, home, configPath, &fakeRunner{})
	if stateFor(checks, "collectors") != "ready" {
		t.Fatalf("collector checks=%#v", checks)
	}
}

func setupRoleDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_RUNTIME_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_RUNTIME_DATABASE_URL is not set")
	}
	return dsn
}
