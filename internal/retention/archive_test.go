package retention

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	store "github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

func TestArchiveEncryptsAndVerifiesNormalizedPlan(t *testing.T) {
	directory := t.TempDir()
	archiver, err := NewFileArchiver(directory, "archive-v1", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	plan := store.RetentionPlan{
		PolicyID:  "70000000-0000-0000-0000-000000000000",
		ProjectID: "70000000-0000-0000-0000-000000000001",
		AsOf:      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Records: []store.ArchiveRecord{{
			EvidenceID:  "70000000-0000-0000-0000-000000000002",
			SourceID:    "70000000-0000-0000-0000-000000000003",
			ProjectID:   "70000000-0000-0000-0000-000000000001",
			ContentHash: bytes.Repeat([]byte{4}, 32), ContentLength: 10,
			Classification: "internal", CoverageState: "complete",
			Provenance: "runtime_observed", ProductSurface: "local",
			ObservedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		}},
	}
	plaintext, err := store.EncodeArchive(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Digest = sha256.Sum256(plaintext)
	receipt, err := archiver.Archive(context.Background(),
		"70000000-0000-0000-0000-000000000004", plan)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.PlaintextDigest != plan.Digest || receipt.VerifiedAt.IsZero() {
		t.Fatalf("invalid archive receipt: %+v", receipt)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 1 {
		t.Fatalf("archive files = %d, error = %v", len(files), err)
	}
	data, err := os.ReadFile(filepath.Join(directory, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, plaintext) || bytes.Contains(data, []byte(plan.ProjectID)) {
		t.Fatal("encrypted archive contains normalized plaintext")
	}
	data[len(data)-1] ^= 0xff
	path := filepath.Join(directory, files[0].Name())
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := archiver.verify(path, plan.Digest); err == nil {
		t.Fatal("tampered archive verified")
	}
}
