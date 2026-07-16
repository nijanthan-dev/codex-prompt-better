package recommendation

import (
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestGenerate_LifecycleAndSafetyRules(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	finding := contracts.AuditFinding{
		Code: "passive_polling", Classification: "avoidable",
		EvidenceRefs: []string{"synthetic-evidence"},
	}
	tests := []struct {
		name    string
		context Context
		want    string
	}{
		{name: "candidate", context: Context{AsOf: at, QualityGuardrailsPass: true}, want: "wait_on_state_change"},
		{name: "existing coverage", context: Context{AsOf: at, QualityGuardrailsPass: true, ExistingRuleCoverage: map[string]bool{"passive_polling": true}}, want: "no_action"},
		{name: "dismissed cooldown", context: Context{AsOf: at, QualityGuardrailsPass: true, Feedback: map[string]Feedback{"passive_polling": {State: "dismissed", CooldownUntil: at.Add(time.Hour), EvidenceRevision: "old"}}, MaterialEvidenceRevision: "old"}, want: "no_action"},
		{name: "new evidence reactivates", context: Context{AsOf: at, QualityGuardrailsPass: true, Feedback: map[string]Feedback{"passive_polling": {State: "dismissed", CooldownUntil: at.Add(time.Hour), EvidenceRevision: "old"}}, MaterialEvidenceRevision: "new"}, want: "wait_on_state_change"},
		{name: "guardrail blocks", context: Context{AsOf: at}, want: "no_action"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Generate([]contracts.AuditFinding{finding}, test.context)
			if len(got) != 1 || got[0].Code != test.want {
				t.Fatalf("recommendations=%#v", got)
			}
		})
	}
}

func TestGenerate_HighTokensNeverDowngradesModel(t *testing.T) {
	t.Parallel()
	got := Generate([]contracts.AuditFinding{{
		Code: "high_tokens", Classification: "avoidable",
	}}, Context{QualityGuardrailsPass: true})
	if len(got) != 1 || got[0].Code != "no_action" {
		t.Fatalf("resource-only model advice emitted: %#v", got)
	}
}
