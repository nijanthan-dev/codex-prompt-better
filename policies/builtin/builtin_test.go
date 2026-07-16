package builtin

import (
	"reflect"
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/internal/policypack"
)

func TestLoadBuiltinsInIssueOrder(t *testing.T) {
	packs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"builtin.repository-worktree", "builtin.instructions-scope-non-goals", "builtin.generated-vendor", "builtin.validation-release", "builtin.privacy-security", "builtin.tool-retrieval-ptc", "builtin.autonomy-fallback-stopping-delegation"}
	got := make([]string, len(packs))
	for index, pack := range packs {
		got[index] = pack.ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pack order=%v", got)
	}
}

func TestRoutingAndAutonomyPolicies(t *testing.T) {
	packs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		facts map[string]string
		want  string
	}{
		{facts: map[string]string{"category": "tool_routing", "capability_state": "supported"}, want: "tool.direct"},
		{facts: map[string]string{"category": "tool_routing", "capability_state": "unknown"}, want: "tool.unknown"},
		{facts: map[string]string{"category": "retrieval"}, want: "retrieval.bounded"},
		{facts: map[string]string{"category": "ptc", "capability_state": "unknown"}, want: "ptc.capability"},
		{facts: map[string]string{"category": "fallback"}, want: "fallback.bounded"},
		{facts: map[string]string{"category": "stopping"}, want: "stopping.exhausted"},
		{facts: map[string]string{"delegation_policy": "none"}, want: "delegation.none"},
		{facts: map[string]string{"delegation_policy": "user_requested_only"}, want: "delegation.user-requested"},
		{facts: map[string]string{"delegation_policy": "bounded"}, want: "delegation.bounded"},
	}
	for _, test := range tests {
		matches, evaluateErr := policypack.Evaluate(packs, test.facts)
		if evaluateErr != nil {
			t.Fatal(evaluateErr)
		}
		found := false
		for _, match := range matches {
			found = found || match.Rule.ID == test.want
		}
		if !found {
			t.Fatalf("facts=%v missing=%s matches=%+v", test.facts, test.want, matches)
		}
	}
}

func TestGuidanceCoverageLedger(t *testing.T) {
	packs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tool.relevant-only", "tool.order", "tool.synthesize",
		"retrieval.first-search", "retrieval.expand-only", "retrieval.citations",
		"ptc.eligible", "ptc.direct-exclusions",
		"autonomy.classify", "autonomy.approval", "autonomy.layers",
		"fallback.attempts", "stopping.supported", "stopping.checkpoint", "stopping.context-efficiency",
		"delegation.bounded", "delegation.no-recursion",
	}
	available := make(map[string]struct{})
	for _, pack := range packs {
		for _, rule := range pack.Rules {
			available[rule.ID] = struct{}{}
		}
	}
	for _, ruleID := range want {
		if _, ok := available[ruleID]; !ok {
			t.Fatalf("guidance rule missing: %s", ruleID)
		}
	}
}

func TestBuiltinsMatchSyntheticContexts(t *testing.T) {
	packs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		facts map[string]string
		want  string
	}{
		{name: "repository", facts: map[string]string{"category": "repository"}, want: "repository.detected"},
		{name: "instruction", facts: map[string]string{"category": "instruction"}, want: "instruction.applicable"},
		{name: "generated", facts: map[string]string{"category": "generated"}, want: "generated.protected"},
		{name: "validation", facts: map[string]string{"category": "validation"}, want: "validation.required"},
		{name: "privacy", facts: map[string]string{"category": "privacy"}, want: "privacy.sensitive"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matches, err := policypack.Evaluate(packs[:index+1], test.facts)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, match := range matches {
				found = found || match.Rule.ID == test.want
			}
			if !found {
				t.Fatalf("missing rule %s in %+v", test.want, matches)
			}
		})
	}
}

func TestBuiltinsDoNotMatchUnknownCategory(t *testing.T) {
	packs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	matches, err := policypack.Evaluate(packs, map[string]string{"category": "unknown"})
	if err != nil || len(matches) != 0 {
		t.Fatalf("matches=%+v err=%v", matches, err)
	}
}
