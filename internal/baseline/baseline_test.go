package baseline

import (
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
			current := rawMetric(test.current)
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

func TestMedianEvenAndOdd(t *testing.T) {
	t.Parallel()
	if Median([]float64{4, 1, 3}) != 3 || Median([]float64{4, 1, 3, 2}) != 2.5 {
		t.Fatal("median calculation changed")
	}
}

func rawMetric(value float64) contracts.MetricResult {
	return contracts.MetricResult{
		Name: "non_cached_input_per_turn", Version: metrics.DefinitionVersion,
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
