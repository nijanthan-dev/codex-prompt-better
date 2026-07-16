package eval

import "testing"

func TestMigrationMatrixChangesOneControlAtATime(t *testing.T) {
	t.Parallel()
	base := MigrationVariant{
		ModelSnapshot: "gpt-5.6-sol", ReasoningEffort: "medium",
		ReasoningMode: "current_turn", Verbosity: "medium",
		PromptContract: "baseline", ToolSet: "direct",
		CacheMode: "implicit", RuntimeRoute: "standard",
		DelegationPolicy: "disabled", RetrievalBudget: "bounded-1",
	}
	cases := []MigrationCase{
		oneChange("reasoning-low", base, func(value *MigrationVariant) { value.ReasoningEffort = "low" }),
		oneChange("reasoning-none", base, func(value *MigrationVariant) { value.ReasoningEffort = "none" }),
		oneChange("persist-all-turns", base, func(value *MigrationVariant) { value.ReasoningMode = "all_turns" }),
		oneChange("verbosity-low", base, func(value *MigrationVariant) { value.Verbosity = "low" }),
		oneChange("lean-prompt", base, func(value *MigrationVariant) { value.PromptContract = "lean-ablation" }),
		oneChange("ptc", base, func(value *MigrationVariant) { value.ToolSet = "programmatic" }),
		oneChange("explicit-cache", base, func(value *MigrationVariant) { value.CacheMode = "explicit" }),
		oneChange("pro", base, func(value *MigrationVariant) { value.RuntimeRoute = "pro" }),
		oneChange("multi-agent", base, func(value *MigrationVariant) { value.DelegationPolicy = "bounded" }),
		oneChange("retrieval-2", base, func(value *MigrationVariant) { value.RetrievalBudget = "bounded-2" }),
	}
	for _, test := range cases {
		if err := ValidateMigrationCase(test); err != nil {
			t.Fatalf("%s: %v", test.ID, err)
		}
	}
	invalid := MigrationCase{ID: "two", Baseline: base, Candidate: base}
	invalid.Candidate.ReasoningEffort = "low"
	invalid.Candidate.Verbosity = "low"
	if err := ValidateMigrationCase(invalid); err == nil {
		t.Fatal("multi-control migration case accepted")
	}
}

func oneChange(id string, baseline MigrationVariant,
	change func(*MigrationVariant),
) MigrationCase {
	candidate := baseline
	change(&candidate)
	return MigrationCase{ID: id, Baseline: baseline, Candidate: candidate}
}
