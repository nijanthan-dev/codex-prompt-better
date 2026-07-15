package boundary

import (
	"sort"

	"github.com/nijanthan-dev/codex-prompt-better/internal/policypack"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
	"github.com/nijanthan-dev/codex-prompt-better/policies/builtin"
)

const MaxDecisions = 256

func EvaluateContext(discovered Context, extensions []policypack.Pack) ([]contracts.BoundaryDecision, error) {
	packs, err := builtin.Load()
	if err != nil {
		return nil, err
	}
	if err := policypack.ValidateSet(extensions); err != nil {
		return nil, err
	}
	builtinIDs := make(map[string]struct{}, len(packs))
	for _, pack := range packs {
		builtinIDs[pack.ID+"@"+pack.Version] = struct{}{}
	}
	for _, pack := range extensions {
		if _, exists := builtinIDs[pack.ID+"@"+pack.Version]; exists {
			return nil, contracts.NewError(contracts.ErrorCodeInvalidSchema, "extension pack duplicates built-in identity", "policy_pack", false)
		}
	}
	resolution := Resolve(discovered.Candidates)
	decisions := make([]contracts.BoundaryDecision, 0)
	for _, candidate := range resolution.Candidates {
		matches, evaluateErr := policypack.Evaluate(packs, candidate.Facts)
		if evaluateErr != nil {
			return nil, evaluateErr
		}
		extensionMatches, evaluateErr := policypack.Evaluate(extensions, candidate.Facts)
		if evaluateErr != nil {
			return nil, evaluateErr
		}
		matches = append(matches, extensionMatches...)
		sort.Slice(matches, func(i, j int) bool {
			left, right := matches[i], matches[j]
			leftPriority, rightPriority := outcomePriority(left.Rule.Outcome), outcomePriority(right.Rule.Outcome)
			if leftPriority != rightPriority {
				return leftPriority > rightPriority
			}
			if left.Rule.RiskScore != right.Rule.RiskScore {
				return left.Rule.RiskScore > right.Rule.RiskScore
			}
			if left.PackID != right.PackID {
				return left.PackID < right.PackID
			}
			return left.Rule.ID < right.Rule.ID
		})
		for _, match := range matches {
			if len(decisions) == MaxDecisions {
				break
			}
			conflicts := append([]string{}, candidate.Conflicts...)
			sort.Strings(conflicts)
			outcome := match.Rule.Outcome
			risk := match.Rule.RiskScore
			if len(resolution.Cycle) > 0 && risk >= 50 {
				outcome = string(OutcomeBlock)
			}
			decisions = append(decisions, contracts.BoundaryDecision{
				Category: match.Rule.Category, Outcome: outcome, Risk: risk,
				RiskLevel: string(riskLevel(risk)), Confidence: candidate.Confidence,
				SourceRef: candidate.SourceRef, PackID: match.PackID, RuleID: match.Rule.ID,
				Version: match.PackVersion, Explanation: match.Rule.Explanation, ConflictRefs: conflicts,
			})
		}
		if len(decisions) == MaxDecisions {
			break
		}
	}
	sort.Slice(decisions, func(i, j int) bool {
		left, right := decisions[i], decisions[j]
		if left.PackID != right.PackID {
			return left.PackID < right.PackID
		}
		if left.RuleID != right.RuleID {
			return left.RuleID < right.RuleID
		}
		return left.SourceRef < right.SourceRef
	})
	return decisions, nil
}

func outcomePriority(outcome string) int {
	switch Outcome(outcome) {
	case OutcomeBlock:
		return 4
	case OutcomeClarify:
		return 3
	case OutcomeWarn:
		return 2
	default:
		return 1
	}
}
