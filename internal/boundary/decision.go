package boundary

import (
	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

type ActionClass string

const (
	ActionReadOnly            ActionClass = "read_only"
	ActionLocalReversible     ActionClass = "local_reversible"
	ActionLocalMutation       ActionClass = "local_mutation"
	ActionExternalWrite       ActionClass = "external_write"
	ActionCostly              ActionClass = "costly"
	ActionPermissionSensitive ActionClass = "permission_sensitive"
	ActionScopeExpanding      ActionClass = "scope_expanding"
	ActionDestructive         ActionClass = "destructive"
)

type Outcome string

const (
	OutcomeContinue Outcome = "continue"
	OutcomeWarn     Outcome = "warn"
	OutcomeClarify  Outcome = "clarify"
	OutcomeBlock    Outcome = "block"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type DecisionInput struct {
	Action           ActionClass
	ExecutionPolicy  contracts.ExecutionPolicy
	DelegationPolicy string
	HostPermission   policy.HostPermission
	CapabilityKnown  bool
	ExplicitIntent   bool
	InScope          bool
	Ambiguous        bool
	Conflicted       bool
	UnsafeLink       bool
	Sensitive        bool
	Unbounded        bool
}

type Decision struct {
	Outcome       Outcome
	Risk          int
	RiskLevel     RiskLevel
	PolicyOutcome contracts.PolicyOutcome
}

func Decide(input DecisionInput) (Decision, error) {
	base, ok := actionRisk[input.Action]
	if !ok || !policy.ValidExecutionPolicy(input.ExecutionPolicy) ||
		!policy.ValidHostPermission(input.HostPermission) || !validDelegation(input.DelegationPolicy) {
		return Decision{}, contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid boundary decision input", "boundary", false)
	}
	risk := base
	if input.Ambiguous {
		risk += 10
	}
	if input.Conflicted {
		risk += 20
	}
	if risk > 100 {
		risk = 100
	}
	level := riskLevel(risk)
	outcome := OutcomeContinue
	switch {
	case input.HostPermission == policy.HostDenied, input.UnsafeLink, input.Sensitive, input.Unbounded:
		outcome = OutcomeBlock
	case input.Conflicted && risk >= 50:
		outcome = OutcomeBlock
	case !input.InScope:
		outcome = OutcomeBlock
	case requiresApproval(input.Action):
		outcome = OutcomeClarify
	case !input.CapabilityKnown && input.Action != ActionReadOnly:
		outcome = OutcomeClarify
	case input.Ambiguous && risk >= 25:
		outcome = OutcomeClarify
	case input.Ambiguous:
		outcome = OutcomeWarn
	case input.ExecutionPolicy == contracts.ExecutionPolicyAskBeforeExecute && input.Action != ActionReadOnly:
		outcome = OutcomeClarify
	case input.ExecutionPolicy == contracts.ExecutionPolicyFollowUserIntent && (!input.ExplicitIntent || input.HostPermission != policy.HostPermitted):
		outcome = OutcomeClarify
	}
	return Decision{Outcome: outcome, Risk: risk, RiskLevel: level, PolicyOutcome: mapOutcome(outcome, input)}, nil
}

func mapOutcome(outcome Outcome, input DecisionInput) contracts.PolicyOutcome {
	switch outcome {
	case OutcomeBlock:
		return contracts.PolicyOutcomeDenied
	case OutcomeClarify:
		return contracts.PolicyOutcomeApprovalRequired
	default:
		if input.ExecutionPolicy == contracts.ExecutionPolicyFollowUserIntent && input.ExplicitIntent && input.InScope && input.HostPermission == policy.HostPermitted {
			return contracts.PolicyOutcomeExecutionRecommended
		}
		return contracts.PolicyOutcomeReturnOnly
	}
}

func riskLevel(risk int) RiskLevel {
	switch {
	case risk < 25:
		return RiskLow
	case risk < 50:
		return RiskMedium
	case risk < 75:
		return RiskHigh
	default:
		return RiskCritical
	}
}

func requiresApproval(action ActionClass) bool {
	return action == ActionExternalWrite || action == ActionCostly || action == ActionDestructive || action == ActionScopeExpanding
}

func validDelegation(value string) bool {
	return value == "none" || value == "user_requested_only" || value == "bounded"
}

var actionRisk = map[ActionClass]int{
	ActionReadOnly: 10, ActionLocalReversible: 25, ActionLocalMutation: 40,
	ActionExternalWrite: 60, ActionCostly: 70, ActionPermissionSensitive: 75,
	ActionScopeExpanding: 80, ActionDestructive: 90,
}
