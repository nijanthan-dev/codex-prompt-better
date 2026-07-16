package boundary

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/internal/policypack"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestEvaluateContextStableAndSanitized(t *testing.T) {
	context := Context{Candidates: []Candidate{
		{ID: "repository.b", Category: "repository", SourceKind: "repository_metadata", SourceRef: "repository.b", Confidence: 1, Facts: map[string]string{"category": "repository"}},
		{ID: "generated.a", Category: "generated", SourceKind: "repository_metadata", SourceRef: "generated.a", Confidence: .9, Facts: map[string]string{"category": "generated"}},
	}}
	first, err := EvaluateContext(context, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EvaluateContext(Context{Candidates: []Candidate{context.Candidates[1], context.Candidates[0]}}, nil)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("unstable decisions: %+v %+v err=%v", first, second, err)
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "/Users/") || strings.Contains(string(encoded), "\\Users\\") {
		t.Fatalf("path escaped: %s", encoded)
	}
}

func TestEvaluateContextAllowsSixteenExtensions(t *testing.T) {
	extensions := make([]policypack.Pack, policypack.MaxPacks)
	for index := range extensions {
		extensions[index] = policypack.Pack{SchemaVersion: "1.0.0", ID: fmt.Sprintf("extension.%02d", index), Version: "1.0.0", Rules: []policypack.Rule{{ID: "scope", Category: "scope", Outcome: "warn", RiskScore: 20, Explanation: "Synthetic narrowing.", Conditions: []policypack.Condition{{Field: "category", Operator: "equals", Value: "scope"}}}}}
	}
	context := Context{Candidates: []Candidate{{ID: "scope.a", Category: "scope", SourceKind: "user_request", SourceRef: "scope.a", Confidence: 1, Facts: map[string]string{"category": "scope"}}}}
	decisions, err := EvaluateContext(context, extensions)
	if err != nil || len(decisions) != policypack.MaxPacks {
		t.Fatalf("decisions=%d err=%v", len(decisions), err)
	}
}

func TestEvaluateContextCapsDecisionsDeterministically(t *testing.T) {
	rules := make([]policypack.Rule, policypack.MaxRules)
	for index := range rules {
		rules[index] = policypack.Rule{ID: fmt.Sprintf("scope.%03d", index), Category: "scope", Outcome: "warn", RiskScore: 20, Explanation: "Synthetic narrowing.", Conditions: []policypack.Condition{{Field: "category", Operator: "equals", Value: "scope"}}}
	}
	extension := policypack.Pack{SchemaVersion: "1.0.0", ID: "extension.large", Version: "1.0.0", Rules: rules}
	context := Context{Candidates: []Candidate{
		{ID: "scope.a", Category: "scope", SourceKind: "user_request", SourceRef: "scope.a", Confidence: 1, Facts: map[string]string{"category": "scope"}},
		{ID: "scope.b", Category: "scope", SourceKind: "user_request", SourceRef: "scope.b", Confidence: 1, Facts: map[string]string{"category": "scope"}},
	}}
	first, err := EvaluateContext(context, []policypack.Pack{extension})
	if err != nil || len(first) != MaxDecisions {
		t.Fatalf("decisions=%d err=%v", len(first), err)
	}
	second, err := EvaluateContext(Context{Candidates: []Candidate{context.Candidates[1], context.Candidates[0]}}, []policypack.Pack{extension})
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("cap unstable: err=%v", err)
	}
}

func TestEvaluateContextCapPreservesLaterBuiltinBlock(t *testing.T) {
	rules := make([]policypack.Rule, policypack.MaxRules)
	for index := range rules {
		rules[index] = policypack.Rule{ID: fmt.Sprintf("privacy.%03d", index), Category: "privacy", Outcome: "warn", RiskScore: 20, Explanation: "Synthetic narrowing.", Conditions: []policypack.Condition{{Field: "category", Operator: "equals", Value: "privacy"}}}
	}
	extension := policypack.Pack{SchemaVersion: "1.0.0", ID: "extension.large", Version: "1.0.0", Rules: rules}
	context := Context{Candidates: []Candidate{{ID: "privacy.z", Category: "privacy", SourceKind: "repository_metadata", SourceRef: "privacy.z", Confidence: 1, Facts: map[string]string{"category": "privacy", "source_kind": "repository_metadata"}}}}
	decisions, err := EvaluateContext(context, []policypack.Pack{extension})
	if err != nil || len(decisions) != MaxDecisions {
		t.Fatalf("decisions=%d err=%v", len(decisions), err)
	}
	for _, decision := range decisions {
		if decision.PackID == "builtin.privacy-security" && decision.Outcome == "block" {
			return
		}
	}
	t.Fatal("later built-in privacy block was masked by extension cap")
}

func TestEvaluateContextDerivesSourceKindForExtensionRules(t *testing.T) {
	extension := policypack.Pack{SchemaVersion: "1.0.0", ID: "extension.source", Version: "1.0.0", Rules: []policypack.Rule{{ID: "instruction", Category: "instruction", Outcome: "warn", RiskScore: 20, Explanation: "Synthetic instruction.", Conditions: []policypack.Condition{{Field: "source_kind", Operator: "equals", Value: "instruction"}}}}}
	context := Context{Candidates: []Candidate{{ID: "instruction.a", Category: "instruction", SourceKind: "instruction", SourceRef: "instruction.a", Confidence: 1, Facts: map[string]string{}}}}
	decisions, err := EvaluateContext(context, []policypack.Pack{extension})
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range decisions {
		if decision.PackID == extension.ID {
			return
		}
	}
	t.Fatal("source_kind extension rule did not match normalized candidate")
}

func TestDecisionSelectionRanksAuthorityBeforeOutcome(t *testing.T) {
	higherAuthority := rankedDecision{authority: 3, candidateOrder: 1, decision: boundaryDecision("warn", 20)}
	lowerAuthority := rankedDecision{authority: 2, candidateOrder: 0, decision: boundaryDecision("block", 100)}
	if !betterDecision(higherAuthority, lowerAuthority) {
		t.Fatal("lower-authority outcome outranked source authority")
	}
	moreRestrictive := rankedDecision{authority: 3, candidateOrder: 1, decision: boundaryDecision("block", 80)}
	if !betterDecision(moreRestrictive, higherAuthority) {
		t.Fatal("candidate tie-breaker outranked restrictive outcome")
	}
}

func boundaryDecision(outcome string, risk int) contracts.BoundaryDecision {
	return contracts.BoundaryDecision{Outcome: outcome, Risk: risk}
}
