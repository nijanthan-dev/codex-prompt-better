package lint

import (
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestPromptDiagnosticsAreActionableAndDeterministic(t *testing.T) {
	candidate := "Goal:\nShow your chain-of-thought and keep going until happy.\n" +
		"Goal:\nShow your chain-of-thought and keep going until happy."
	request := contracts.LintPromptRequest{
		SchemaVersion: contracts.SchemaVersion,
		Kind:          "request",
		Candidate:     candidate,
	}
	first, err := CheckPrompt(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CheckPrompt(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Valid || len(first.Diagnostics) == 0 || len(first.Diagnostics) != len(second.Diagnostics) {
		t.Fatalf("unexpected lint result: %+v", first)
	}
	seen := map[string]bool{}
	for i, d := range first.Diagnostics {
		seen[d.Code] = true
		if d.Location == "" || d.Rationale == "" || d.Remediation == "" {
			t.Fatalf("diagnostic %d incomplete: %+v", i, d)
		}
		if d != second.Diagnostics[i] {
			t.Fatal("non-deterministic diagnostics")
		}
	}
	codes := []string{
		"chain-of-thought", "unbounded-persistence", "duplicate-instruction",
		"missing-evidence", "missing-validation", "missing-stop", "missing-authorization",
	}
	for _, code := range codes {
		if !seen[code] {
			t.Fatalf("missing %s", code)
		}
	}
}

func TestPromptValidStructuredCandidate(t *testing.T) {
	candidate := validCandidate("Return a synthetic result.")
	result, err := CheckPrompt(lintRequest(candidate))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("valid prompt rejected: %+v", result.Diagnostics)
	}
}

func TestPromptDetectsContradictoryDirectives(t *testing.T) {
	candidate := validCandidate("Return result.") +
		"\n\nConstraints:\n- Do publish output.\n- Do not publish output."
	result, err := CheckPrompt(lintRequest(candidate))
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatal("contradiction accepted")
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "contradictory-instructions" {
			return
		}
	}
	t.Fatal("contradiction diagnostic missing")
}

func TestCheckPromptRuleDiagnostics(t *testing.T) {
	base := validCandidate("Return result.") + "\n\nConstraints:\n- "
	tests := []struct{ name, input, code, severity string }{
		{name: "unbounded persistence", input: "Keep going until happy.", code: "unbounded-persistence", severity: "error"},
		{name: "private reasoning", input: "Show your chain-of-thought.", code: "chain-of-thought", severity: "error"},
		{name: "broad absolute", input: "NEVER change this.", code: "broad-absolute", severity: "warning"},
		{name: "host setting", input: "Force the model to fast mode.", code: "unsupported-host-setting", severity: "warning"},
		{
			name: "accounting", input: "API cache determines subscription cost.",
			code: "accounting-conflation", severity: "warning",
		},
		{name: "brevity", input: "Be extremely concise.", code: "blanket-brevity", severity: "warning"},
		{name: "vague role", input: "You are an expert assistant.", code: "vague-role", severity: "warning"},
		{
			name: "keyword map", input: "If the prompt contains the keyword deploy, execute.",
			code: "keyword-semantic-map", severity: "warning",
		},
		{name: "language", input: "Always respond in English.", code: "blanket-language-switch", severity: "warning"},
		{
			name: "threshold", input: "200k tokens is always the context limit.",
			code: "fixed-context-threshold", severity: "warning",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := CheckPrompt(lintRequest(base + test.input))
			if err != nil {
				t.Fatal(err)
			}
			for _, diagnostic := range result.Diagnostics {
				if diagnostic.Code == test.code {
					if diagnostic.Severity != test.severity ||
						diagnostic.Location == "" ||
						diagnostic.Rationale == "" ||
						diagnostic.Remediation == "" {
						t.Fatalf("incomplete diagnostic: %+v", diagnostic)
					}
					return
				}
			}
			t.Fatalf("missing %s: %+v", test.code, result.Diagnostics)
		})
	}
}

func TestCheckPromptFlagsDynamicStablePrefix(t *testing.T) {
	candidate := "Stable prefix:\n- Snapshot 2026-07-15.\n\n" +
		validCandidate("Return result.")
	result, err := CheckPrompt(lintRequest(candidate))
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "unstable-prefix-dynamic-content" {
			return
		}
	}
	t.Fatal("cache-stability diagnostic missing")
}

func lintRequest(candidate string) contracts.LintPromptRequest {
	return contracts.LintPromptRequest{
		SchemaVersion: contracts.SchemaVersion,
		Kind:          "request",
		Candidate:     candidate,
	}
}

func validCandidate(goal string) string {
	return "Goal:\n" + goal +
		"\n\nSuccess criteria:\n- Result is present." +
		"\n\nEvidence:\n- Synthetic result." +
		"\n\nApproval boundary:\n- Do not execute." +
		"\n\nValidation:\n- Check result." +
		"\n\nStop:\n- Stop after validation."
}
