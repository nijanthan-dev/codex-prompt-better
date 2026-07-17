package baseline

import (
	"math"
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/internal/metrics"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestApply_StatusRules(t *testing.T) {
	t.Parallel()
	definition := definitionByName(t, "non_cached_input_per_turn")
	tests := []struct {
		name       string
		current    float64
		previous   float64
		rolling    []float64
		comparison Comparison
		want       contracts.MetricStatus
		reason     string
	}{
		{name: "improved lower better", current: 80, previous: 100, rolling: []float64{100, 102, 98}, comparison: Comparison{ConfoundersMatched: true, PersistenceCount: 2}, want: contracts.MetricStatusImproved, reason: "meaningful_matched_change"},
		{name: "flat in noise", current: 101, previous: 100, rolling: []float64{98, 100, 102}, comparison: Comparison{ConfoundersMatched: true, PersistenceCount: 2}, want: contracts.MetricStatusFlat, reason: "within_practical_or_noise_threshold"},
		{name: "unmatched confounder", current: 120, previous: 100, rolling: []float64{98, 100, 102}, comparison: Comparison{PersistenceCount: 2}, want: contracts.MetricStatusMixed, reason: "confounder_unmatched"},
		{name: "guardrail blocks", current: 80, previous: 100, rolling: []float64{98, 100, 102}, comparison: Comparison{ConfoundersMatched: true, PersistenceCount: 2}, want: contracts.MetricStatusInsufficient, reason: "guardrail_regressed_or_unknown"},
		{name: "privacy suppresses", current: 80, previous: 100, rolling: []float64{98, 100, 102}, comparison: Comparison{PrivacySuppressed: true}, want: contracts.MetricStatusInsufficient, reason: "privacy_suppressed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := rawMetric(definition.Name, test.current)
			test.comparison.Previous = window(test.previous)
			if test.name != "guardrail blocks" {
				test.comparison.Previous.GuardrailsPass = true
			}
			for _, value := range test.rolling {
				test.comparison.Rolling = append(test.comparison.Rolling, window(value))
			}
			result, err := Apply(current, definition, test.comparison)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want || result.StatusReason != test.reason {
				t.Fatalf("status=%s reason=%s", result.Status, result.StatusReason)
			}
		})
	}
}

func TestApply_NonFiniteEvidenceIsInsufficient(t *testing.T) {
	t.Parallel()
	definition := definitionByName(t, "non_cached_input_per_turn")
	for _, value := range []float64{math.NaN(), math.Inf(1)} {
		current := rawMetric(definition.Name, value)
		result, err := Apply(current, definition, Comparison{})
		if err != nil || result.Status != contracts.MetricStatusInsufficient ||
			result.StatusReason != "current_incomplete" {
			t.Fatalf("non-finite current accepted: %#v error=%v", result, err)
		}
		comparison := Comparison{
			Previous: window(value), Rolling: []Window{window(1), window(1), window(1)},
		}
		result, err = Apply(rawMetric(definition.Name, 1), definition, comparison)
		if err != nil || result.StatusReason != "previous_incompatible" {
			t.Fatalf("non-finite baseline accepted: %#v error=%v", result, err)
		}
	}
}

func TestMedianEvenAndOdd(t *testing.T) {
	t.Parallel()
	if Median([]float64{4, 1, 3}) != 3 || Median([]float64{4, 1, 3, 2}) != 2.5 {
		t.Fatal("median calculation changed")
	}
}

func TestApply_ToolErrorGuardrailDirection(t *testing.T) {
	t.Parallel()
	definition := definitionByName(t, "tool_error_rate")
	tests := []struct {
		name     string
		current  float64
		baseline float64
		want     contracts.MetricStatus
	}{
		{name: "rising errors worsen", current: 0.20, baseline: 0.10, want: contracts.MetricStatusWorsened},
		{name: "falling errors improve", current: 0.05, baseline: 0.10, want: contracts.MetricStatusImproved},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := rawMetric(definition.Name, test.current)
			comparison := Comparison{
				Previous: window(test.baseline), Rolling: []Window{
					window(test.baseline * 0.99), window(test.baseline), window(test.baseline * 1.01),
				},
				ConfoundersMatched: true, PersistenceCount: 2,
			}
			comparison.Previous.GuardrailsPass = true
			result, err := Apply(current, definition, comparison)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want {
				t.Fatalf("status=%s want=%s", result.Status, test.want)
			}
		})
	}
}

func rawMetric(name string, value float64) contracts.MetricResult {
	return contracts.MetricResult{
		Name: name, Version: metrics.DefinitionVersion,
		NativeValue: &value, Coverage: contracts.CoverageStateComplete,
	}
}

func window(value float64) Window {
	return Window{
		Value: &value, Coverage: contracts.CoverageStateComplete,
		MetricVersion: metrics.DefinitionVersion, Comparable: true,
	}
}

func definitionByName(t *testing.T, name string) metrics.Definition {
	t.Helper()
	for _, definition := range metrics.Definitions() {
		if definition.Name == name {
			return definition
		}
	}
	t.Fatalf("definition %s missing", name)
	return metrics.Definition{}
}
