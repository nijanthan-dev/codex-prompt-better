package policypack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const (
	MaxPackBytes  = 64 * 1024
	MaxPacks      = 16
	MaxRules      = 256
	MaxConditions = 16
)

type Condition struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type Rule struct {
	ID          string      `json:"id"`
	Category    string      `json:"category"`
	Outcome     string      `json:"outcome"`
	RiskScore   int         `json:"risk_score"`
	Explanation string      `json:"explanation"`
	Conditions  []Condition `json:"conditions"`
}

type Pack struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Version       string `json:"version"`
	Rules         []Rule `json:"rules"`
}

type Match struct {
	PackID      string
	PackVersion string
	Rule        Rule
}

func Parse(data []byte) (Pack, error) {
	if len(data) == 0 || len(data) > MaxPackBytes {
		return Pack{}, invalid("policy pack size must be between 1 and 65536 bytes", "policy_pack")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var pack Pack
	if err := decoder.Decode(&pack); err != nil {
		return Pack{}, invalid("invalid policy pack", "policy_pack")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Pack{}, invalid("policy pack contains trailing data", "policy_pack")
	}
	if err := Validate(pack); err != nil {
		return Pack{}, err
	}
	return pack, nil
}

func Validate(pack Pack) error {
	if pack.SchemaVersion != contracts.SchemaVersion || pack.Version != contracts.SchemaVersion {
		return invalid("unsupported policy pack version", "policy_pack.version")
	}
	if !validIdentifier(pack.ID, 80) {
		return invalid("invalid policy pack id", "policy_pack.id")
	}
	if len(pack.Rules) == 0 || len(pack.Rules) > MaxRules {
		return invalid("policy pack must contain 1 to 256 rules", "policy_pack.rules")
	}

	seen := make(map[string]struct{}, len(pack.Rules))
	for index, rule := range pack.Rules {
		field := fmt.Sprintf("policy_pack.rules.%d", index)
		if err := validateRule(rule, field); err != nil {
			return err
		}
		if _, exists := seen[rule.ID]; exists {
			return invalid("duplicate policy rule id", field+".id")
		}
		seen[rule.ID] = struct{}{}
	}
	return nil
}

func ValidateSet(packs []Pack) error {
	if len(packs) > MaxPacks {
		return invalid("at most 16 policy packs are supported", "policy_packs")
	}
	seen := make(map[string]struct{}, len(packs))
	for _, pack := range packs {
		if err := Validate(pack); err != nil {
			return err
		}
		key := pack.ID + "@" + pack.Version
		if _, exists := seen[key]; exists {
			return invalid("duplicate policy pack", "policy_packs")
		}
		seen[key] = struct{}{}
	}
	return nil
}

// ValidateExtensions enforces that external packs can only narrow behavior.
func ValidateExtensions(packs []Pack) error {
	if err := ValidateSet(packs); err != nil {
		return err
	}
	for _, pack := range packs {
		for _, rule := range pack.Rules {
			if rule.Outcome == "continue" {
				return invalid("extension policy rules cannot broaden behavior", "policy_packs")
			}
		}
	}
	return nil
}

func Evaluate(packs []Pack, facts map[string]string) ([]Match, error) {
	if err := ValidateSet(packs); err != nil {
		return nil, err
	}
	matches := make([]Match, 0)
	for _, pack := range packs {
		for _, rule := range pack.Rules {
			if matchesAll(rule.Conditions, facts) {
				matches = append(matches, Match{PackID: pack.ID, PackVersion: pack.Version, Rule: rule})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].PackID != matches[j].PackID {
			return matches[i].PackID < matches[j].PackID
		}
		return matches[i].Rule.ID < matches[j].Rule.ID
	})
	return matches, nil
}

func validateRule(rule Rule, field string) error {
	if !validIdentifier(rule.ID, 80) || !contains(categories, rule.Category) || !contains(outcomes, rule.Outcome) {
		return invalid("invalid policy rule identity, category, or outcome", field)
	}
	if rule.RiskScore < 0 || rule.RiskScore > 100 || len(rule.Explanation) == 0 || len(rule.Explanation) > 300 {
		return invalid("invalid policy rule risk or explanation", field)
	}
	if len(rule.Conditions) == 0 || len(rule.Conditions) > MaxConditions {
		return invalid("policy rule must contain 1 to 16 conditions", field+".conditions")
	}
	for _, condition := range rule.Conditions {
		if !contains(fields, condition.Field) || !contains(operators, condition.Operator) || len(condition.Value) > 128 || !validConditionValue(condition) {
			return invalid("invalid policy condition", field+".conditions")
		}
	}
	return nil
}

func validConditionValue(condition Condition) bool {
	values := conditionValues[condition.Field]
	switch condition.Operator {
	case "present":
		return condition.Value == ""
	case "prefix":
		return contains(values, condition.Value)
	default:
		return contains(values, condition.Value)
	}
}

func matchesAll(conditions []Condition, facts map[string]string) bool {
	for _, condition := range conditions {
		value, present := facts[condition.Field]
		switch condition.Operator {
		case "equals":
			if !present || value != condition.Value {
				return false
			}
		case "not_equals":
			if !present || value == condition.Value {
				return false
			}
		case "prefix":
			if !present || !strings.HasPrefix(value, condition.Value) {
				return false
			}
		case "present":
			if !present {
				return false
			}
		}
	}
	return true
}

func validIdentifier(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, current := range value[1:] {
		letter := current >= 'a' && current <= 'z'
		digit := current >= '0' && current <= '9'
		if !letter && !digit && current != '_' && current != '.' && current != '-' {
			return false
		}
	}
	return true
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func invalid(message, field string) error {
	return contracts.NewError(contracts.ErrorCodeInvalidSchema, message, field, false)
}

var categories = []string{"repository", "worktree", "instruction", "scope", "non_goal", "generated", "vendor", "validation", "release", "privacy", "security", "tool_routing", "retrieval", "ptc", "autonomy", "fallback", "stopping", "delegation"}
var outcomes = []string{"continue", "warn", "clarify", "block"}
var fields = []string{"category", "action_class", "phase", "source_kind", "capability_state", "delegation_policy", "ambiguous", "conflicted"}
var operators = []string{"equals", "not_equals", "prefix", "present"}
var conditionValues = map[string][]string{
	"category":          categories,
	"action_class":      {"read_only", "local_reversible", "local_mutation", "external_write", "costly", "permission_sensitive", "scope_expanding", "destructive"},
	"phase":             {"research", "design", "implementation", "review", "external_coordination"},
	"source_kind":       {"host_permission", "configuration", "instruction", "user_request", "repository_metadata", "derived_default"},
	"capability_state":  {"supported", "unsupported", "unknown"},
	"delegation_policy": {"none", "user_requested_only", "bounded"},
	"ambiguous":         {"true", "false"},
	"conflicted":        {"true", "false"},
}
