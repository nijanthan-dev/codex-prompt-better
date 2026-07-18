package collector

import (
	"strings"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
)

func TestRuntimeObservation_ParsesBoundedNativeFields(t *testing.T) {
	record := evidence.Record{
		Attributes: map[string]string{
			"phase": "commentary", "response_event": "completed",
			"canonical_call": "redacted:tool:query", "state_epoch": "epoch-one",
			"mutation_state": "unchanged", "output_modality": "text",
			"output_size_bytes": "2048", "usage_kind": "total_tokens",
			"usage_value": "100", "usage_unit": "tokens",
			"accounting_regime": "native", "cache_kind": "cached_input",
			"cache_value": "20", "cache_ttl": "300",
		},
		GovernanceOverhead: true,
		ObservedAt:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	got, err := runtimeObservation(record)
	if err != nil {
		t.Fatal(err)
	}
	if got.OutputSizeBytes == nil || *got.OutputSizeBytes != 2048 ||
		got.UsageValue == nil || *got.UsageValue != 100 ||
		got.CacheValue == nil || *got.CacheValue != 20 ||
		got.CacheTTLSeconds == nil || *got.CacheTTLSeconds != 300 ||
		len(got.CanonicalCallHash) != 32 || len(got.StateEpochHash) != 32 ||
		!got.GovernanceOverhead {
		t.Fatalf("unexpected normalized runtime: %#v", got)
	}
}

func TestRuntimeObservation_ParsesReplaySnapshots(t *testing.T) {
	record := evidence.Record{ObservedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Attributes: map[string]string{
		"task_attribution_state": "attributed", "project_attribution_confidence": "0.9",
		"project_attribution_valid_to": "2026-02-01T00:00:00Z",
		"plan_hash":                    strings.Repeat("11", 32), "plan_phase_scope": "execution",
		"approval_boundary_class": "explicit",
		"budget_enforcement":      "host", "budget_max_tool_loops": "8",
		"budget_max_retries": "2", "budget_max_concurrency": "3",
		"budget_delegation_policy": "bounded", "budget_exhaustion_outcome": "stop",
		"policy_schema_version": "policy-v1", "policy_hash": strings.Repeat("22", 32),
		"raw_prompt_retention": "false", "telemetry": "false",
		"host_kind": "codex", "host_version": "1", "host_capability_name": "shell",
		"host_capability_state": "available",
	}}
	got, err := runtimeObservation(record)
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan == nil || got.Budget == nil || got.Policy == nil || got.HostCapability == nil ||
		got.AttributionConfidence == nil || *got.AttributionConfidence != 0.9 ||
		got.AttributionValidTo == nil || got.Budget.MaxConcurrency == nil ||
		*got.Budget.MaxConcurrency != 3 || len(got.Policy.Hash) != 32 {
		t.Fatalf("replay snapshots lost: %#v", got)
	}
}

func TestRuntimeObservation_RejectsPartialReplaySnapshots(t *testing.T) {
	for _, attributes := range []map[string]string{
		{"plan_hash": strings.Repeat("11", 32)},
		{"budget_enforcement": "host"},
		{"policy_hash": strings.Repeat("22", 32)},
		{"host_capability_name": "shell"},
		{"project_attribution_confidence": "0.5"},
	} {
		if _, err := runtimeObservation(evidence.Record{ObservedAt: time.Now(), Attributes: attributes}); err == nil {
			t.Fatalf("accepted partial replay snapshot: %#v", attributes)
		}
	}
}

func TestRuntimeObservation_RejectsMalformedNativeValues(t *testing.T) {
	for _, attributes := range []map[string]string{
		{"output_size_bytes": "-1"},
		{"usage_value": "not-a-number"},
		{"cache_ttl": "-1"},
	} {
		if _, err := runtimeObservation(evidence.Record{Attributes: attributes}); err == nil {
			t.Fatalf("accepted malformed values: %#v", attributes)
		}
	}
}

func TestPersistentSource_ProjectIDIsOptionalConfiguredAttribution(t *testing.T) {
	source := PersistentSource{
		SourceID: "source-id", CursorID: "cursor-id", ProjectID: "project-id",
	}
	if source.ProjectID != "project-id" {
		t.Fatalf("project mapping lost: %#v", source)
	}
}

func TestParseOptionalFloatRejectsNonFiniteValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"NaN", "+Inf", "-Inf"} {
		if _, err := parseOptionalFloat(value); err == nil {
			t.Fatalf("accepted non-finite value %q", value)
		}
	}
}
