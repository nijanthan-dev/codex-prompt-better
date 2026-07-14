package policy

import "github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"

type HostPermission string

const (
	HostPermitted HostPermission = "permitted"
	HostDenied    HostPermission = "denied"
	HostUnknown   HostPermission = "unknown"
)

func Resolve(
	executionPolicy contracts.ExecutionPolicy,
	phase string,
	host HostPermission,
) (contracts.PolicyOutcome, error) {
	if !ValidExecutionPolicy(executionPolicy) {
		return "", contracts.NewError(contracts.ErrorCodeInvalidSchema, "unknown execution policy", "execution_policy", false)
	}
	if !ValidHostPermission(host) {
		return "", contracts.NewError(contracts.ErrorCodeInvalidSchema, "unknown host permission", "host_permission", false)
	}
	if !validPhase(phase) {
		return "", contracts.NewError(contracts.ErrorCodeInvalidSchema, "unknown phase", "phase", false)
	}
	execute := phase == "implementation" || phase == "external_coordination"
	if !execute || executionPolicy == contracts.ExecutionPolicyImproveOnly {
		return contracts.PolicyOutcomeReturnOnly, nil
	}
	if host == HostDenied {
		return contracts.PolicyOutcomeDenied, nil
	}
	if host == HostUnknown || executionPolicy == contracts.ExecutionPolicyAskBeforeExecute {
		return contracts.PolicyOutcomeApprovalRequired, nil
	}
	if executionPolicy == contracts.ExecutionPolicyFollowUserIntent && host == HostPermitted {
		return contracts.PolicyOutcomeExecutionRecommended, nil
	}
	return "", contracts.NewError(contracts.ErrorCodeInternal, "policy resolution failed", "execution_policy", false)
}

func validPhase(value string) bool {
	switch value {
	case "research", "design", "implementation", "review", "external_coordination":
		return true
	default:
		return false
	}
}

func ValidExecutionPolicy(value contracts.ExecutionPolicy) bool {
	switch value {
	case contracts.ExecutionPolicyImproveOnly,
		contracts.ExecutionPolicyAskBeforeExecute,
		contracts.ExecutionPolicyFollowUserIntent:
		return true
	default:
		return false
	}
}

func ValidHostPermission(value HostPermission) bool {
	switch value {
	case HostPermitted, HostDenied, HostUnknown:
		return true
	default:
		return false
	}
}
