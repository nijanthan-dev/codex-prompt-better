package postgres

import (
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestEvaluationOutcomePrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		statuses []contracts.MetricStatus
		want     string
	}{
		{
			name: "worsened after insufficient", want: "regressed",
			statuses: []contracts.MetricStatus{
				contracts.MetricStatusInsufficient,
				contracts.MetricStatusWorsened,
			},
		},
		{
			name: "worsened before insufficient", want: "regressed",
			statuses: []contracts.MetricStatus{
				contracts.MetricStatusWorsened,
				contracts.MetricStatusInsufficient,
			},
		},
		{
			name: "mixed after improved", want: "mixed",
			statuses: []contracts.MetricStatus{
				contracts.MetricStatusImproved,
				contracts.MetricStatusMixed,
			},
		},
		{
			name: "improved after mixed", want: "mixed",
			statuses: []contracts.MetricStatus{
				contracts.MetricStatusMixed,
				contracts.MetricStatusImproved,
			},
		},
		{
			name: "improved over flat", want: "improved",
			statuses: []contracts.MetricStatus{
				contracts.MetricStatusFlat,
				contracts.MetricStatusImproved,
			},
		},
		{name: "flat only", statuses: []contracts.MetricStatus{contracts.MetricStatusFlat}, want: "unchanged"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := contracts.AuditProjectResult{}
			for _, status := range test.statuses {
				result.Metrics = append(result.Metrics, contracts.MetricResult{Status: status})
			}
			if got := evaluationOutcome(result); got != test.want {
				t.Fatalf("evaluation outcome=%q want=%q", got, test.want)
			}
		})
	}
}

func TestEvaluationQualityGatePrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		states []string
		want   string
	}{
		{name: "fail after unknown", states: []string{"unknown", "fail"}, want: "fail"},
		{name: "fail before unknown", states: []string{"fail", "unknown"}, want: "fail"},
		{name: "unknown after pass", states: []string{"pass", "unknown"}, want: "unknown"},
		{name: "pass after unknown", states: []string{"unknown", "pass"}, want: "unknown"},
		{name: "pass only", states: []string{"pass"}, want: "pass"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := contracts.AuditProjectResult{}
			for _, state := range test.states {
				result.Guardrails = append(result.Guardrails, contracts.GuardrailResult{State: state})
			}
			if got := evaluationQualityGate(result); got != test.want {
				t.Fatalf("quality gate=%q want=%q", got, test.want)
			}
		})
	}
}
