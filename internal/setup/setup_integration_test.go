//go:build integration

package setup

import (
	"context"
	"os"
	"testing"
)

func TestDoctor_PostgreSQLFixtureReady(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	t.Setenv(databaseEnv, dsn)
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
