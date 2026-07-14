package policy

import (
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestResolveTruthTable(t *testing.T) {
	policies := []contracts.ExecutionPolicy{
		contracts.ExecutionPolicyImproveOnly,
		contracts.ExecutionPolicyAskBeforeExecute,
		contracts.ExecutionPolicyFollowUserIntent,
	}
	phases := []string{"research", "design", "implementation", "review", "external_coordination"}
	hosts := []HostPermission{HostPermitted, HostDenied, HostUnknown}
	for _, executionPolicy := range policies {
		for _, phase := range phases {
			for _, host := range hosts {
				name := string(executionPolicy) + "/" + phase + "/" + string(host)
				t.Run(name, func(t *testing.T) {
					got, err := Resolve(executionPolicy, phase, host)
					if err != nil {
						t.Fatal(err)
					}
					want := expectedOutcome(executionPolicy, phase, host)
					if got != want {
						t.Fatalf("got %q want %q", got, want)
					}
				})
			}
		}
	}
}

func TestResolveRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		policy contracts.ExecutionPolicy
		phase  string
		host   HostPermission
	}{
		{name: "policy", policy: "invalid", phase: "design", host: HostUnknown},
		{name: "phase", policy: contracts.ExecutionPolicyImproveOnly, phase: "invalid", host: HostUnknown},
		{name: "host", policy: contracts.ExecutionPolicyImproveOnly, phase: "design", host: "invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Resolve(test.policy, test.phase, test.host); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func expectedOutcome(
	executionPolicy contracts.ExecutionPolicy,
	phase string,
	host HostPermission,
) contracts.PolicyOutcome {
	isExecutionPhase := phase == "implementation" || phase == "external_coordination"
	if !isExecutionPhase || executionPolicy == contracts.ExecutionPolicyImproveOnly {
		return contracts.PolicyOutcomeReturnOnly
	}
	if host == HostDenied {
		return contracts.PolicyOutcomeDenied
	}
	if host == HostUnknown || executionPolicy == contracts.ExecutionPolicyAskBeforeExecute {
		return contracts.PolicyOutcomeApprovalRequired
	}
	return contracts.PolicyOutcomeExecutionRecommended
}
