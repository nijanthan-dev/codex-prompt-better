package postgres

import (
	"context"
	"io/fs"
	"strings"
	"testing"
)

func TestMigrationFSStartsWithSchemaOnly(t *testing.T) {
	migrations, err := MigrationFS()
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(migrations, "00001_initialize.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"CREATE TABLE", "CREATE INDEX", "FOREIGN KEY", "CREATE VIEW"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("step 2 migration contains later-step object %q", forbidden)
		}
	}
}

func TestNewRunnerRejectsNil(t *testing.T) {
	if _, err := NewRunner(nil); err == nil {
		t.Fatal("expected nil database rejection")
	}
}

func TestRunnerHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &Runner{}
	if err := runner.Up(ctx); err != context.Canceled {
		t.Fatalf("Up() error = %v, want context.Canceled", err)
	}
}
