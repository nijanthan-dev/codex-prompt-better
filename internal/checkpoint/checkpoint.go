package checkpoint

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

// Checkpoint is a bounded continuation summary.
type Checkpoint struct {
	Objective         string   `json:"objective"`
	AcceptedDecisions []string `json:"accepted_decisions"`
	Constraints       []string `json:"constraints"`
	EvidenceRefs      []string `json:"evidence_refs"`
	Completed         []string `json:"completed"`
	Validation        []string `json:"validation"`
	Blockers          []string `json:"blockers"`
	NextAction        string   `json:"next_action"`
	RemainingGates    []string `json:"remaining_gates"`
}

func Render(value Checkpoint) (string, error) {
	if !validText(value.Objective, 1000) || !validText(value.NextAction, 500) {
		return "", checkpointError(
			"checkpoint objective and next action are required",
			"checkpoint",
		)
	}
	for _, list := range []struct {
		name   string
		values []string
		max    int
	}{
		{name: "accepted_decisions", values: value.AcceptedDecisions, max: 50},
		{name: "constraints", values: value.Constraints, max: 50},
		{name: "evidence_refs", values: value.EvidenceRefs, max: 50},
		{name: "completed", values: value.Completed, max: 50},
		{name: "validation", values: value.Validation, max: 50},
		{name: "blockers", values: value.Blockers, max: 20},
		{name: "remaining_gates", values: value.RemainingGates, max: 50},
	} {
		if err := validateList(list.values, list.max); err != nil {
			return "", checkpointError(
				"invalid checkpoint list",
				"checkpoint."+list.name,
			)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Checkpoint\n\nObjective:\n%s\n", strings.TrimSpace(value.Objective))
	writeList(&b, "Accepted decisions", value.AcceptedDecisions)
	writeList(&b, "Constraints", value.Constraints)
	writeList(&b, "Evidence", value.EvidenceRefs)
	writeList(&b, "Completed", value.Completed)
	writeList(&b, "Validation", value.Validation)
	writeList(&b, "Blockers", value.Blockers)
	fmt.Fprintf(&b, "\nNext action:\n%s\n", strings.TrimSpace(value.NextAction))
	writeList(&b, "Remaining gates", value.RemainingGates)
	return strings.TrimSpace(b.String()), nil
}

func checkpointError(message, field string) error {
	return contracts.NewError(contracts.ErrorCodeInvalidSchema, message, field, false)
}

func validateList(values []string, max int) error {
	if len(values) > max {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "too many checkpoint items", "checkpoint", false)
	}
	for _, value := range values {
		if !validText(value, 500) {
			return contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid checkpoint item", "checkpoint", false)
		}
	}
	return nil
}

func validText(value string, max int) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func writeList(b *strings.Builder, title string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s:\n", title)
	for _, value := range values {
		fmt.Fprintf(b, "- %s\n", strings.TrimSpace(value))
	}
}
