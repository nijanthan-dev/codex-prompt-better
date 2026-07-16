package eval

import (
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/internal/replay"
)

func TestNewRunIsDeterministicAndQualityGated(t *testing.T) {
	t.Parallel()
	tests := []struct {
		outcome string
		want    string
	}{
		{outcome: "improved", want: "promote"},
		{outcome: "regressed", want: "rollback"},
		{outcome: "unchanged", want: "hold"},
		{outcome: "mixed", want: "hold"},
	}
	for _, test := range tests {
		quality := "pass"
		first, err := NewRun("eval-v1", []string{"fixture"}, map[string]string{"mode": "same"},
			components(), replay.Comparison{Outcome: test.outcome, QualityGateState: quality})
		if err != nil {
			t.Fatal(err)
		}
		second, err := NewRun("eval-v1", []string{"fixture"}, map[string]string{"mode": "same"},
			components(), replay.Comparison{Outcome: test.outcome, QualityGateState: quality})
		if err != nil {
			t.Fatal(err)
		}
		if first.Decision != test.want || first.RunHash != second.RunHash || len(first.RunHash) != 64 {
			t.Fatalf("%s evaluation unstable: %#v %#v", test.outcome, first, second)
		}
	}
}

func TestNewRunQualityRegressionBlocksPromotion(t *testing.T) {
	t.Parallel()
	run, err := NewRun("eval-v1", "fixture", "config", components(),
		replay.Comparison{Outcome: "improved", QualityGateState: "fail"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Decision != "rollback" {
		t.Fatalf("quality regression promoted: %#v", run)
	}
}

func components() Components {
	return Components{
		Model: "synthetic-model", Compiler: "compiler-v1",
		Policy: "policy-v1", Metrics: "metric-v1",
	}
}
