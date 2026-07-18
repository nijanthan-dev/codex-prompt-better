package postgres

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/migrations"
)

func TestMigrationFSStartsWithSchemaOnly(t *testing.T) {
	data, err := fs.ReadFile(migrations.Files, "00001_initialize.sql")
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

func TestDefaultPoolConfigIsLocalBounded(t *testing.T) {
	config := DefaultPoolConfig()
	if config.MaxConnections != 4 || config.MinConnections != 0 || config.MaxDatabaseBytes != maxLocalDatabaseBytes {
		t.Fatalf("unexpected pool bounds: %+v", config)
	}
}

func TestEstimatedCollectionBytesIsBounded(t *testing.T) {
	if got := estimatedCollectionBytes(make([]Evidence, 2)); got != collectionBudgetReserve+2*estimatedEvidenceBytes {
		t.Fatalf("estimate = %d", got)
	}
	if got := estimatedCollectionBytes(make([]Evidence, 0)); got != 0 {
		t.Fatalf("empty estimate = %d", got)
	}
	if !storageBudgetAvailable(100, 50, 200) || storageBudgetAvailable(150, 50, 200) ||
		storageBudgetAvailable(200, 0, 200) {
		t.Fatal("storage budget boundary is not fail-closed")
	}
}

func TestCollectionEvidenceRejectsInvalidTypedFacts(t *testing.T) {
	observedAt := time.Now().UTC()
	hash := sha256.Sum256([]byte("synthetic"))
	negative, zero, invalidConfidence := int64(-1), int64(0), math.NaN()
	base := Evidence{SourceID: "source", SchemaVersion: "1", ContentHash: hash[:], ObservedAt: observedAt}

	for name, runtime := range map[string]RuntimeObservation{
		"negative budget": {Plan: &PlanSnapshot{Hash: hash[:], PhaseScope: "execution", ApprovalBoundary: "explicit"},
			Budget: &BudgetSnapshot{Enforcement: "host", DelegationPolicy: "bounded",
				ExhaustionOutcome: "stop", MaxRetries: &negative}},
		"zero concurrency": {Plan: &PlanSnapshot{Hash: hash[:], PhaseScope: "execution", ApprovalBoundary: "explicit"},
			Budget: &BudgetSnapshot{Enforcement: "host", DelegationPolicy: "bounded",
				ExhaustionOutcome: "stop", MaxConcurrency: &zero}},
		"invalid confidence": {AttributionConfidence: &invalidConfidence},
		"invalid validity":   {AttributionValidTo: &observedAt},
	} {
		item := base
		item.Runtime = runtime
		if err := validateCollectionEvidence(item); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestPutUsageBatchRejectsInvalidTypedFact(t *testing.T) {
	value := math.Inf(1)
	trajectoryID := "trajectory"
	err := (&Repository{}).PutUsageBatch(context.Background(), []UsageObservation{{
		ID: "observation", TrajectoryID: &trajectoryID, MetricKind: "tokens", Value: &value,
		UsageUnit: "tokens", Provenance: "runtime_observed", ObservedAt: time.Now().UTC(),
	}})
	if err == nil {
		t.Fatal("accepted non-finite usage fact")
	}
}
