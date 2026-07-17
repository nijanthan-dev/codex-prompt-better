package replay

import (
	"math"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestComparePreservesUnknownAndContextualMovement(t *testing.T) {
	t.Parallel()
	baseline := contracts.AuditProjectResult{
		RevisionHash: "baseline",
		Metrics: []contracts.MetricResult{
			metric("non_cached_input_per_turn", 10),
			metric("tokens_per_turn", 20),
		},
	}
	candidate := contracts.AuditProjectResult{
		RevisionHash: "candidate",
		Metrics: []contracts.MetricResult{
			metric("non_cached_input_per_turn", 8),
			metric("tokens_per_turn", 18),
			{Name: "new_metric", Coverage: contracts.CoverageStateMissing},
		},
	}
	comparison, err := Compare(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Outcome != "mixed" ||
		comparison.Statuses["non_cached_input_per_turn"] != contracts.MetricStatusImproved ||
		comparison.Statuses["tokens_per_turn"] != contracts.MetricStatusMixed ||
		comparison.Statuses["new_metric"] != contracts.MetricStatusInsufficient {
		t.Fatalf("comparison lost semantics: %#v", comparison)
	}
}

func TestManifestPinsVersionsAndProvesSourceImmutability(t *testing.T) {
	t.Parallel()
	result := contracts.AuditProjectResult{
		RevisionHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Window: contracts.AuditWindowProvenance{
			AsOf: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		Metrics: []contracts.MetricResult{
			{Name: "tokens_per_turn", Version: "metric-v1"},
		},
	}
	before, err := NewManifest(result, map[string]string{"fixture": "synthetic"},
		[]string{"project-v1"}, []string{"source-v1"}, []string{"policy-v1"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := NewManifest(result, map[string]string{"fixture": "synthetic"},
		[]string{"project-v1"}, []string{"source-v1"}, []string{"policy-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyImmutable(before, after); err != nil {
		t.Fatal(err)
	}
	after.SourceVersions = []string{"source-v2"}
	if err := VerifyImmutable(before, after); err == nil {
		t.Fatal("changed source version accepted as immutable replay")
	}
}

func TestCompareUsesPolarityVersionsAndCompleteMetricSet(t *testing.T) {
	t.Parallel()
	baseline := contracts.AuditProjectResult{
		RevisionHash: "baseline",
		Metrics: []contracts.MetricResult{
			metric("scope_attribution_coverage", 0.9),
			metric("validation_presence", 1),
			metric("non_cached_input_per_turn", 10),
		},
	}
	candidate := contracts.AuditProjectResult{
		RevisionHash: "candidate",
		Metrics: []contracts.MetricResult{
			metric("scope_attribution_coverage", 0.8),
			metric("validation_presence", 0.9),
		},
	}
	candidate.Metrics[1].Version = "metric-v2"
	comparison, err := Compare(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Statuses["scope_attribution_coverage"] != contracts.MetricStatusWorsened ||
		comparison.Statuses["validation_presence"] != contracts.MetricStatusInsufficient ||
		comparison.Statuses["non_cached_input_per_turn"] != contracts.MetricStatusInsufficient ||
		comparison.Outcome != "regressed" {
		t.Fatalf("polarity/version/removed metric semantics lost: %#v", comparison)
	}
}

func TestCompareIncompleteEvidenceCannotPromote(t *testing.T) {
	t.Parallel()
	baseline := contracts.AuditProjectResult{
		RevisionHash: "baseline",
		Metrics: []contracts.MetricResult{
			metric("non_cached_input_per_turn", 10),
			metric("validation_presence", 1),
		},
	}
	candidate := contracts.AuditProjectResult{
		RevisionHash: "candidate",
		Metrics: []contracts.MetricResult{
			metric("non_cached_input_per_turn", 8),
			{Name: "validation_presence", Version: "metric-v1", Coverage: contracts.CoverageStateMissing},
		},
		Guardrails: []contracts.GuardrailResult{
			{Name: "validation_presence", State: "pass"},
			{Name: "privacy_redaction_coverage", State: "unknown"},
		},
	}
	comparison, err := Compare(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Outcome != "mixed" || comparison.QualityGateState != "unknown" {
		t.Fatalf("incomplete comparison could promote: %#v", comparison)
	}
}

func TestCompareRejectsAmbiguousAndNonFiniteMetrics(t *testing.T) {
	t.Parallel()
	duplicate := contracts.AuditProjectResult{
		RevisionHash: "baseline",
		Metrics:      []contracts.MetricResult{metric("validation_presence", 1), metric("validation_presence", 1)},
	}
	if _, err := Compare(duplicate, contracts.AuditProjectResult{RevisionHash: "candidate"}); err == nil {
		t.Fatal("duplicate baseline metric names accepted")
	}
	duplicate.RevisionHash = "candidate"
	if _, err := Compare(contracts.AuditProjectResult{RevisionHash: "baseline"}, duplicate); err == nil {
		t.Fatal("duplicate candidate metric names accepted")
	}
	nonFinite := metric("validation_presence", math.NaN())
	comparison, err := Compare(
		contracts.AuditProjectResult{
			RevisionHash: "baseline",
			Metrics:      []contracts.MetricResult{metric("validation_presence", 1)},
		},
		contracts.AuditProjectResult{RevisionHash: "candidate", Metrics: []contracts.MetricResult{nonFinite}},
	)
	if err != nil || comparison.Statuses["validation_presence"] != contracts.MetricStatusInsufficient ||
		comparison.Outcome != "mixed" {
		t.Fatalf("non-finite metric did not fail closed: %#v error=%v", comparison, err)
	}
}

func metric(name string, value float64) contracts.MetricResult {
	return contracts.MetricResult{
		Name: name, Version: "metric-v1", NativeValue: &value,
		Coverage: contracts.CoverageStateComplete,
	}
}
