package diagnosis

import (
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestEvaluate_PositiveNegativeAmbiguousMissingAndRequired(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		observation Observation
		want        int
		class       string
		confidence  string
	}{
		{name: "negative", observation: Observation{PositiveCount: 0}, want: 0},
		{name: "positive", observation: complete("repeat", 2, 10), want: 1, class: "avoidable", confidence: "high"},
		{name: "ambiguous", observation: withCounter(complete("repeat", 2, 10)), want: 1, class: "mixed", confidence: "medium"},
		{name: "missing", observation: Observation{Code: "repeat", PositiveCount: 1, Denominator: 10, MinimumSample: 5, Coverage: contracts.CoverageStateMissing}, want: 1, class: "uncertain", confidence: "unknown"},
		{name: "required", observation: required(complete("validation", 1, 1)), want: 1, class: "necessary", confidence: "high"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := Evaluate([]Observation{test.observation})
			if len(findings) != test.want {
				t.Fatalf("finding count=%d want=%d", len(findings), test.want)
			}
			if test.want == 1 && (findings[0].Classification != test.class ||
				findings[0].Confidence != test.confidence) {
				t.Fatalf("finding=%#v", findings[0])
			}
		})
	}
}

func complete(code string, positives, denominator int) Observation {
	return Observation{
		Code: code, Cause: "synthetic", ExceptionCheck: "synthetic exceptions checked",
		Classification: "avoidable", Coverage: contracts.CoverageStateComplete,
		PositiveCount: positives, Denominator: denominator, MinimumSample: 1,
		ConfounderMatched: true,
	}
}

func withCounter(value Observation) Observation {
	value.Counterevidence = []string{"synthetic-counterevidence"}
	return value
}

func required(value Observation) Observation {
	value.RequiredWork = true
	return value
}
