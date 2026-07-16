package boundary

import (
	"container/heap"
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
	selected := &decisionHeap{}
	heap.Init(selected)
	for candidateOrder, candidate := range resolution.Candidates {
		facts := normalizedCandidateFacts(candidate, resolution.Conflicted[candidate.ID])
		matches, evaluateErr := policypack.Evaluate(packs, facts)
		if evaluateErr != nil {
			return nil, evaluateErr
		}
		extensionMatches, evaluateErr := policypack.Evaluate(extensions, facts)
		if evaluateErr != nil {
			return nil, evaluateErr
		}
		matches = append(matches, extensionMatches...)
		for _, match := range matches {
			conflicts := append([]string{}, candidate.Conflicts...)
			sort.Strings(conflicts)
			outcome := match.Rule.Outcome
			risk := match.Rule.RiskScore
			if len(resolution.Cycle) > 0 && risk >= 50 {
				outcome = string(OutcomeBlock)
			}
			decision := contracts.BoundaryDecision{
				Category: match.Rule.Category, Outcome: outcome, Risk: risk,
				RiskLevel: string(riskLevel(risk)), Confidence: candidate.Confidence,
				SourceRef: candidate.SourceRef, PackID: match.PackID, RuleID: match.Rule.ID,
				Version: match.PackVersion, Explanation: match.Rule.Explanation, ConflictRefs: conflicts,
			}
			_, isBuiltin := builtinIDs[match.PackID+"@"+match.PackVersion]
			entry := rankedDecision{decision: decision, builtin: isBuiltin, candidateOrder: candidateOrder}
			selectDecision(selected, entry)
		}
	}
	decisions := make([]contracts.BoundaryDecision, len(*selected))
	for index, entry := range *selected {
		decisions[index] = entry.decision
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

func normalizedCandidateFacts(candidate Candidate, conflicted bool) map[string]string {
	facts := make(map[string]string, len(candidate.Facts)+3)
	for name, value := range candidate.Facts {
		facts[name] = value
	}
	facts["category"] = candidate.Category
	facts["source_kind"] = candidate.SourceKind
	if conflicted {
		facts["conflicted"] = "true"
	}
	return facts
}

type rankedDecision struct {
	decision       contracts.BoundaryDecision
	builtin        bool
	candidateOrder int
}

type decisionHeap []rankedDecision

func (items decisionHeap) Len() int      { return len(items) }
func (items decisionHeap) Swap(i, j int) { items[i], items[j] = items[j], items[i] }
func (items decisionHeap) Less(i, j int) bool {
	return betterDecision(items[j], items[i])
}
func (items *decisionHeap) Push(value any) { *items = append(*items, value.(rankedDecision)) }
func (items *decisionHeap) Pop() any {
	old := *items
	last := old[len(old)-1]
	*items = old[:len(old)-1]
	return last
}

func selectDecision(selected *decisionHeap, entry rankedDecision) {
	if selected.Len() < MaxDecisions {
		heap.Push(selected, entry)
		return
	}
	if betterDecision(entry, (*selected)[0]) {
		(*selected)[0] = entry
		heap.Fix(selected, 0)
	}
}

func betterDecision(left, right rankedDecision) bool {
	leftPriority, rightPriority := outcomePriority(left.decision.Outcome), outcomePriority(right.decision.Outcome)
	if leftPriority != rightPriority {
		return leftPriority > rightPriority
	}
	if left.builtin != right.builtin {
		return left.builtin
	}
	if left.decision.Risk != right.decision.Risk {
		return left.decision.Risk > right.decision.Risk
	}
	if left.candidateOrder != right.candidateOrder {
		return left.candidateOrder < right.candidateOrder
	}
	if left.decision.PackID != right.decision.PackID {
		return left.decision.PackID < right.decision.PackID
	}
	if left.decision.RuleID != right.decision.RuleID {
		return left.decision.RuleID < right.decision.RuleID
	}
	return left.decision.SourceRef < right.decision.SourceRef
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
