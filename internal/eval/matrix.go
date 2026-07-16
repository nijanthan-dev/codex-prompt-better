package eval

import "errors"

type MigrationVariant struct {
	ModelSnapshot    string `json:"model_snapshot"`
	ReasoningEffort  string `json:"reasoning_effort"`
	ReasoningMode    string `json:"reasoning_mode"`
	Verbosity        string `json:"verbosity"`
	PromptContract   string `json:"prompt_contract"`
	ToolSet          string `json:"tool_set"`
	CacheMode        string `json:"cache_mode"`
	RuntimeRoute     string `json:"runtime_route"`
	DelegationPolicy string `json:"delegation_policy"`
	RetrievalBudget  string `json:"retrieval_budget"`
}

type MigrationCase struct {
	ID        string           `json:"id"`
	Baseline  MigrationVariant `json:"baseline"`
	Candidate MigrationVariant `json:"candidate"`
}

func (variant MigrationVariant) controls() []string {
	return []string{
		variant.ModelSnapshot,
		variant.ReasoningEffort,
		variant.ReasoningMode,
		variant.Verbosity,
		variant.PromptContract,
		variant.ToolSet,
		variant.CacheMode,
		variant.RuntimeRoute,
		variant.DelegationPolicy,
		variant.RetrievalBudget,
	}
}

func ValidateMigrationCase(test MigrationCase) error {
	if test.ID == "" {
		return errors.New("migration case id required")
	}
	changes := 0
	baseline := test.Baseline.controls()
	candidate := test.Candidate.controls()
	for index := range baseline {
		if baseline[index] != candidate[index] {
			changes++
		}
	}
	if changes != 1 {
		return errors.New("migration case must change exactly one control")
	}
	return nil
}
