package tokens

import "testing"

func TestDerive_Table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		start     Observation
		end       Observation
		wrap      uint64
		wantState string
		wantValue uint64
		wantErr   bool
	}{
		{name: "delta", start: obs("input_tokens", "tokens", 10, 1), end: obs("input_tokens", "tokens", 15, 2), wantState: "derived", wantValue: 5},
		{name: "reset", start: obs("input_tokens", "tokens", 10, 1), end: obs("input_tokens", "tokens", 2, 2), wantState: "reset"},
		{name: "wrap", start: obs("input_tokens", "tokens", 14, 1), end: obs("input_tokens", "tokens", 2, 2), wrap: 15, wantState: "wrap", wantValue: 4},
		{name: "duplicate", start: obs("input_tokens", "tokens", 10, 2), end: obs("input_tokens", "tokens", 10, 2), wantState: "duplicate_or_out_of_order"},
		{name: "unlike native class", start: obs("cached_tokens", "tokens", 10, 1), end: obs("input_tokens", "tokens", 20, 2), wantState: "unknown", wantErr: true},
		{name: "api and subscription isolated", start: obs("input_tokens", "tokens", 10, 1), end: Observation{CounterType: "input_tokens", Unit: "subscription_units", Value: 20, Sequence: 2, Source: "synthetic"}, wantState: "unknown", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := Derive(test.start, test.end, test.wrap)
			if (err != nil) != test.wantErr || result.State != test.wantState || result.Value != test.wantValue || result.AlgorithmVersion != AlgorithmVersion {
				t.Fatalf("unexpected delta: %#v, %v", result, err)
			}
		})
	}
}

func obs(kind, unit string, value, sequence uint64) Observation {
	return Observation{CounterType: kind, Unit: unit, Value: value, Sequence: sequence, Source: "synthetic"}
}
