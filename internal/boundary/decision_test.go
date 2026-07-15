package boundary

import (
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestDecisionTruthTable(t *testing.T) {
	actions := []ActionClass{ActionReadOnly, ActionLocalReversible, ActionLocalMutation, ActionExternalWrite, ActionCostly, ActionPermissionSensitive, ActionScopeExpanding, ActionDestructive}
	policies := []contracts.ExecutionPolicy{contracts.ExecutionPolicyImproveOnly, contracts.ExecutionPolicyAskBeforeExecute, contracts.ExecutionPolicyFollowUserIntent}
	delegations := []string{"none", "user_requested_only", "bounded"}
	hosts := []policy.HostPermission{policy.HostPermitted, policy.HostDenied, policy.HostUnknown}
	for _, action := range actions {
		for _, executionPolicy := range policies {
			for _, delegation := range delegations {
				for _, host := range hosts {
					input := DecisionInput{Action: action, ExecutionPolicy: executionPolicy, DelegationPolicy: delegation, HostPermission: host, CapabilityKnown: true, ExplicitIntent: true, InScope: true}
					decision, err := Decide(input)
					if err != nil {
						t.Fatalf("%s/%s/%s/%s: %v", action, executionPolicy, delegation, host, err)
					}
					if host == policy.HostDenied && decision.Outcome != OutcomeBlock {
						t.Fatalf("host denial broadened: %+v", decision)
					}
					if requiresApproval(action) && host != policy.HostDenied && decision.Outcome != OutcomeClarify {
						t.Fatalf("approval bypassed: %+v", decision)
					}
				}
			}
		}
	}
}

func TestDecisionRiskAndAmbiguity(t *testing.T) {
	tests := []struct {
		name    string
		input   DecisionInput
		outcome Outcome
		risk    int
		level   RiskLevel
	}{
		{name: "low ambiguity warns", input: baseDecision(ActionReadOnly), outcome: OutcomeWarn, risk: 20, level: RiskLow},
		{name: "medium ambiguity clarifies", input: baseDecision(ActionLocalReversible), outcome: OutcomeClarify, risk: 35, level: RiskMedium},
		{name: "high conflict blocks", input: baseDecision(ActionLocalMutation), outcome: OutcomeBlock, risk: 60, level: RiskHigh},
	}
	tests[0].input.Ambiguous = true
	tests[1].input.Ambiguous = true
	tests[2].input.Conflicted = true
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Decide(test.input)
			if err != nil || got.Outcome != test.outcome || got.Risk != test.risk || got.RiskLevel != test.level {
				t.Fatalf("decision=%+v err=%v", got, err)
			}
		})
	}
}

func TestDecisionFailsClosed(t *testing.T) {
	for _, mutate := range []func(*DecisionInput){
		func(input *DecisionInput) { input.UnsafeLink = true },
		func(input *DecisionInput) { input.Sensitive = true },
		func(input *DecisionInput) { input.Unbounded = true },
		func(input *DecisionInput) { input.InScope = false },
	} {
		input := baseDecision(ActionReadOnly)
		mutate(&input)
		got, err := Decide(input)
		if err != nil || got.Outcome != OutcomeBlock || got.PolicyOutcome != contracts.PolicyOutcomeDenied {
			t.Fatalf("decision=%+v err=%v", got, err)
		}
	}
}

func TestFollowUserIntentDoesNotGrantAuthority(t *testing.T) {
	input := baseDecision(ActionLocalMutation)
	input.ExecutionPolicy = contracts.ExecutionPolicyFollowUserIntent
	input.ExplicitIntent = false
	got, err := Decide(input)
	if err != nil || got.Outcome != OutcomeClarify || got.PolicyOutcome != contracts.PolicyOutcomeApprovalRequired {
		t.Fatalf("decision=%+v err=%v", got, err)
	}
}

func baseDecision(action ActionClass) DecisionInput {
	return DecisionInput{Action: action, ExecutionPolicy: contracts.ExecutionPolicyImproveOnly, DelegationPolicy: "none", HostPermission: policy.HostPermitted, CapabilityKnown: true, ExplicitIntent: true, InScope: true}
}
