package metrics

import (
	"math"
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func FuzzComputeRatiosRemainFinite(f *testing.F) {
	f.Add(uint16(10), uint16(100), uint16(2))
	f.Fuzz(func(t *testing.T, turnsRaw, callsRaw, errorsRaw uint16) {
		turns := int(turnsRaw%1000) + 1
		calls := int(callsRaw%1000) + 5
		errorsCount := int(errorsRaw) % (calls + 1)
		results, err := Compute(Input{
			CompletedTurns: turns, AttributedTurns: turns,
			ToolCalls: calls, ToolErrors: errorsCount,
			AcceptedEvidence: 1, RedactedEvidence: 1,
			Coverage: contracts.CoverageStateComplete,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range results {
			if result.NativeValue != nil && (math.IsNaN(*result.NativeValue) ||
				math.IsInf(*result.NativeValue, 0)) {
				t.Fatalf("non-finite metric: %+v", result)
			}
		}
	})
}

func TestCompute_RatiosUseRawNumeratorsAndPreserveUnknown(t *testing.T) {
	tests := []struct {
		name     string
		input    Input
		metric   string
		want     float64
		wantNil  bool
		wantNote string
	}{
		{
			name: "ratio from raw",
			input: Input{
				CompletedTurns: 2, TotalTokens: 300, ToolCalls: 10,
				ToolResults: 10, PassiveWaitCalls: 2, Coverage: contracts.CoverageStateComplete,
				EvidenceRefs: []string{"synthetic-evidence"},
			},
			metric: "passive_waits_per_100_tool_calls", want: 20,
		},
		{
			name: "partial remains unknown",
			input: Input{
				CompletedTurns: 2, TotalTokens: 300, ToolCalls: 10,
				ToolResults: 10, Coverage: contracts.CoverageStatePartial,
			},
			metric: "tokens_per_turn", wantNil: true, wantNote: "coverage_incomplete",
		},
		{
			name: "small denominator remains unknown",
			input: Input{
				CompletedTurns: 2, ToolCalls: 2, ToolResults: 2,
				PassiveWaitCalls: 1, Coverage: contracts.CoverageStateComplete,
			},
			metric: "passive_waits_per_100_tool_calls", wantNil: true, wantNote: "minimum_sample_not_met",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results, err := Compute(test.input)
			if err != nil {
				t.Fatal(err)
			}
			got := findMetric(t, results, test.metric)
			if test.wantNil {
				if got.NativeValue != nil || got.Status != contracts.MetricStatusInsufficient ||
					got.Uncertainty != test.wantNote {
					t.Fatalf("unexpected unknown result: %#v", got)
				}
				return
			}
			if got.NativeValue == nil || *got.NativeValue != test.want ||
				got.Numerator == nil || got.Denominator == nil {
				t.Fatalf("unexpected metric: %#v", got)
			}
		})
	}
}

func TestDefinitions_DocumentGovernanceFields(t *testing.T) {
	for _, definition := range Definitions() {
		if definition.Intent == "" || definition.NativeUnit == "" ||
			definition.MinimumSample < 1 || definition.PracticalChange <= 0 ||
			definition.Version == "" || definition.Formula == "" ||
			definition.Numerator == "" || definition.Denominator == "" ||
			definition.MinimumCoverage <= 0 || definition.Hysteresis <= 0 ||
			definition.MinimumPersistence < 1 || definition.CooldownWindows < 1 ||
			len(definition.EvidenceRequired) == 0 || definition.MisuseRisk == "" {
			t.Fatalf("incomplete definition: %#v", definition)
		}
	}
}

func TestCompute_GuardrailsUseObservableDenominators(t *testing.T) {
	t.Parallel()
	results, err := Compute(Input{
		CompletedTurns: 2, ToolCalls: 10, ToolResults: 10,
		ToolErrors: 1, ObservableMutations: 2, ValidatedMutations: 2,
		AcceptedEvidence: 4, RedactedEvidence: 4,
		Coverage: contracts.CoverageStateComplete,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMetricValue(t, results, "tool_error_rate", 0.1)
	assertMetricValue(t, results, "validation_presence", 1)
	assertMetricValue(t, results, "privacy_redaction_coverage", 1)

	unknown, err := Compute(Input{
		CompletedTurns: 1, ToolCalls: 5, ToolResults: 5,
		Coverage: contracts.CoverageStateComplete,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"validation_presence", "privacy_redaction_coverage"} {
		metric := findMetric(t, unknown, name)
		if metric.NativeValue != nil || metric.Uncertainty != "minimum_sample_not_met" {
			t.Fatalf("%s fabricated from missing denominator: %#v", name, metric)
		}
	}
}

func TestCompute_RejectsInconsistentInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input Input
	}{
		{name: "negative count", input: Input{ToolErrors: -1}},
		{name: "attributed turns exceed completed", input: Input{CompletedTurns: 1, AttributedTurns: 2}},
		{name: "repeated calls exceed calls", input: Input{ToolCalls: 1, RepeatedCalls: 2}},
		{name: "redacted evidence exceeds accepted", input: Input{AcceptedEvidence: 1, RedactedEvidence: 2}},
		{name: "non-cached tokens exceed total", input: Input{TotalTokens: 1, NonCachedInputTokens: 2}},
		{name: "active time exceeds wall clock", input: Input{WallClockSeconds: 1, ActiveSeconds: 2}},
		{name: "non-finite measurement", input: Input{TotalTokens: math.NaN()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Compute(test.input); err == nil {
				t.Fatalf("inconsistent input accepted: %#v", test.input)
			}
		})
	}
}

func assertMetricValue(t *testing.T, results []contracts.MetricResult, name string, want float64) {
	t.Helper()
	got := findMetric(t, results, name)
	if got.NativeValue == nil || *got.NativeValue != want {
		t.Fatalf("%s=%#v want=%v", name, got, want)
	}
}

func findMetric(t *testing.T, results []contracts.MetricResult, name string) contracts.MetricResult {
	t.Helper()
	for _, result := range results {
		if result.Name == name {
			return result
		}
	}
	t.Fatalf("metric %q missing", name)
	return contracts.MetricResult{}
}
