package lint

import (
	"regexp"
	"strings"

	"github.com/nijanthan-dev/codex-prompt-better/internal/textutil"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

type rule struct {
	code, severity, message, rationale, remediation string
	pattern                                         *regexp.Regexp
}

var lineRules = []rule{
	{
		code: "unbounded-persistence", severity: "error",
		message:     "Unbounded persistence or delegation request.",
		rationale:   "Work without measurable completion or limits can overrun scope and budget.",
		remediation: "Add measurable completion, retry or fanout limits, and an observable stop rule.",
		pattern: regexp.MustCompile(
			`(?i)\b(keep going|until happy|babysit|do everything|never stop|` +
				`recursive delegation)\b`,
		),
	},
	{
		code: "chain-of-thought", severity: "error",
		message:     "Private reasoning or chain-of-thought requested.",
		rationale:   "The task should request conclusions and evidence, not hidden reasoning.",
		remediation: "Ask for a concise rationale, evidence, or verification summary.",
		pattern:     regexp.MustCompile(`(?i)\b(chain[- ]of[- ]thought|show (all|your) reasoning|hidden reasoning)\b`),
	},
	{
		code: "broad-absolute", severity: "warning",
		message:     "Broad ALWAYS/NEVER instruction.",
		rationale:   "Absolute wording is brittle unless it protects a true invariant.",
		remediation: "Replace with a scoped decision rule or state why this is an invariant.",
		pattern:     regexp.MustCompile(`\b(ALWAYS|NEVER)\b`),
	},
	{
		code: "unsupported-host-setting", severity: "warning",
		message:     "Host setting claim may be unsupported.",
		rationale:   "Prompt Better must not invent or change model, effort, verbosity, fast, Ultra, or hidden settings.",
		remediation: "Capability-tag the advice and require current evidence, or remove it.",
		pattern: regexp.MustCompile(
			`(?i)\b(set|switch|force|guarantee).*` +
				`(model|reasoning effort|verbosity|fast mode|ultra mode|hidden flag)\b`,
		),
	},
	{
		code: "accounting-conflation", severity: "warning",
		message:     "API and subscription accounting may be conflated.",
		rationale:   "API token or cache fields do not establish Codex subscription usage or cost.",
		remediation: "Name the product surface and accounting regime; mark unknowns explicitly.",
		pattern: regexp.MustCompile(
			`(?i)\b(api|cache).*(subscription|usage unit|price|cost)\b|` +
				`\bsubscription.*(token|cache|api)\b`,
		),
	},
	{
		code: "blanket-brevity", severity: "warning",
		message:     "Blanket brevity may drop required content.",
		rationale:   "Facts, decisions, caveats, evidence, and next actions take priority over generic brevity.",
		remediation: "State a concrete length or format while preserving required content.",
		pattern:     regexp.MustCompile(`(?i)\b(be (extremely )?(brief|concise)|sacrifice .* for brevity)\b`),
	},
	{
		code: "vague-role", severity: "warning",
		message:     "Role is too vague to change behavior reliably.",
		rationale:   "Generic expert or helpful roles add tokens without defining responsibility.",
		remediation: "Name the concrete responsibility or remove the role.",
		pattern:     regexp.MustCompile(`(?i)\byou are (a |an )?(helpful|expert|world[- ]class) (assistant|ai|agent)\b`),
	},
	{
		code: "keyword-semantic-map", severity: "warning",
		message:     "Keyword-only semantic routing may be brittle.",
		rationale:   "Keywords do not reliably establish user intent, authorization, or phase.",
		remediation: "Use an explicit decision rule with context and fallback behavior.",
		pattern:     regexp.MustCompile(`(?i)\bif (the )?(prompt|request) (contains|has) (the )?(word|keyword)\b`),
	},
	{
		code: "blanket-language-switch", severity: "warning",
		message:     "Blanket language switching may drop user intent.",
		rationale:   "Output language should change only under an explicit request or product rule.",
		remediation: "Preserve the requested language or state the scoped language rule.",
		pattern:     regexp.MustCompile(`(?i)\b(always |regardless.*,? )?(respond|answer|output) in english\b`),
	},
	{
		code: "fixed-context-threshold", severity: "warning",
		message:     "Fixed cache, context, or compaction threshold may be unsupported.",
		rationale:   "Host thresholds and accounting behavior are capability- and version-dependent.",
		remediation: "Treat the number as source-qualified evidence or remove the universal claim.",
		pattern:     regexp.MustCompile(`(?i)\b[0-9]+k? (tokens?|context|compaction|cache).*(always|threshold|limit)\b`),
	},
}

var dynamicDatePattern = regexp.MustCompile(`\b20[0-9]{2}[-/]`)

type requiredSection struct {
	name        string
	code        string
	message     string
	rationale   string
	remediation string
}

var requiredSections = []requiredSection{
	{name: "goal", code: "missing-goal", message: "Missing explicit outcome.",
		rationale:   "Without an outcome, transformations cannot be checked for success.",
		remediation: "Add a Goal section or a direct outcome-first instruction."},
	{name: "success criteria", code: "missing-success", message: "Missing success criteria.",
		rationale:   "Completion is ambiguous without observable success criteria.",
		remediation: "Add measurable success criteria."},
	{name: "evidence", code: "missing-evidence", message: "Missing evidence requirement.",
		rationale:   "Claims and completion cannot be verified without required evidence.",
		remediation: "State the evidence needed or explicitly say none is required."},
	{name: "validation", code: "missing-validation", message: "Missing validation requirement.",
		rationale:   "A result can look complete without evidence that it works.",
		remediation: "State the checks or evidence required before completion."},
	{name: "stop", code: "missing-stop", message: "Missing stop rule.",
		rationale:   "Open-ended work can drift into later phases or excessive retries.",
		remediation: "Add an observable completion and blocked/budget stop rule."},
	{name: "approval boundary", code: "missing-authorization", message: "Missing authorization boundary.",
		rationale:   "The prompt may silently advance from planning to implementation or external action.",
		remediation: "State what requires user or host approval."},
}

type directiveState struct {
	isNegative bool
	line       int
}

// CheckPrompt returns deterministic, actionable v1 diagnostics.
func CheckPrompt(request contracts.LintPromptRequest) (contracts.LintPromptResult, error) {
	if request.SchemaVersion != contracts.SchemaVersion || request.Kind != "request" {
		return contracts.LintPromptResult{}, invalid("request must use schema 1.0.0 and kind request", "request")
	}
	candidate, err := textutil.Normalize(request.Candidate)
	if err != nil {
		return contracts.LintPromptResult{}, err
	}
	if len(candidate) > 16000 {
		return contracts.LintPromptResult{}, invalid("candidate exceeds 16000 bytes", "candidate")
	}
	lines := strings.Split(candidate, "\n")
	lower := strings.ToLower(candidate)
	diagnostics := make([]contracts.Diagnostic, 0)
	add := func(code, severity, message, location, rationale, remediation string) {
		diagnostics = append(diagnostics, contracts.Diagnostic{
			Code: code, Severity: severity, Message: message,
			Location: location, Rationale: rationale, Remediation: remediation,
		})
	}
	for _, section := range requiredSections {
		if !hasSection(lines, section.name) {
			add(
				section.code, "warning", section.message, "document",
				section.rationale, section.remediation,
			)
		}
	}
	seen := map[string]int{}
	directives := map[string]directiveState{}
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		location := "line " + itoa(index+1)
		if trimmed != "" {
			key := strings.ToLower(trimmed)
			if prior, ok := seen[key]; ok {
				add(
					"duplicate-instruction", "warning",
					"Instruction duplicates line "+itoa(prior)+".", location,
					"Repeated instructions add noise and can destabilize reusable prefixes.",
					"Keep one authoritative copy.",
				)
			} else {
				seen[key] = index + 1
			}
		}
		for _, current := range lineRules {
			if current.pattern.MatchString(line) {
				add(current.code, current.severity, current.message, location, current.rationale, current.remediation)
			}
		}
		key, negative, ok := directive(line)
		if !ok {
			continue
		}
		if prior, exists := directives[key]; exists && prior.isNegative != negative {
			add(
				"contradictory-instructions", "error",
				"Instruction contradicts line "+itoa(prior.line)+".", location,
				"Opposing directives make the required behavior undefined.",
				"Keep one scoped authoritative directive or add an explicit precedence rule.",
			)
		} else if !exists {
			directives[key] = directiveState{isNegative: negative, line: index + 1}
		}
	}
	if strings.Contains(lower, "stable prefix:") {
		inPrefix := false
		for index, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.EqualFold(trimmed, "Stable prefix:") {
				inPrefix = true
				continue
			}
			if inPrefix && isSectionBoundary(trimmed) {
				inPrefix = false
			}
			if inPrefix && dynamicDatePattern.MatchString(line) {
				add(
					"unstable-prefix-dynamic-content", "warning",
					"Dated dynamic content appears in the stable prefix.", "line "+itoa(index+1),
					"Volatile content churns a reusable prefix without changing its durable contract.",
					"Move dated task evidence to dynamic context unless ordering authority requires otherwise.",
				)
			}
		}
	}
	valid := true
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			valid = false
			break
		}
	}
	diagnostics = capDiagnostics(diagnostics, 100)
	return contracts.LintPromptResult{
		SchemaVersion: contracts.SchemaVersion,
		Kind:          "result",
		Valid:         valid,
		Diagnostics:   diagnostics,
	}, nil
}

func capDiagnostics(diagnostics []contracts.Diagnostic, limit int) []contracts.Diagnostic {
	if len(diagnostics) <= limit {
		return diagnostics
	}
	result := make([]contracts.Diagnostic, 0, limit)
	for _, errorsFirst := range []bool{true, false} {
		for _, diagnostic := range diagnostics {
			if (diagnostic.Severity == "error") == errorsFirst {
				result = append(result, diagnostic)
				if len(result) == limit {
					return result
				}
			}
		}
	}
	return result
}

func isSectionBoundary(value string) bool {
	if value == "" || !strings.HasSuffix(value, ":") {
		return false
	}
	return !dynamicDatePattern.MatchString(value)
}

func invalid(message, field string) error {
	return contracts.NewError(contracts.ErrorCodeInvalidSchema, message, field, false)
}

func hasSection(lines []string, name string) bool {
	want := strings.ToLower(name) + ":"
	for _, line := range lines {
		value := strings.ToLower(strings.TrimSpace(line))
		if value == want || name == "goal" && value == "take this as a new goal:" {
			return true
		}
	}
	return false
}

func directive(line string) (key string, negative, ok bool) {
	value := strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "-* ")))
	for _, prefix := range []string{"do not ", "never "} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(value, prefix)), true, true
		}
	}
	for _, prefix := range []string{"do ", "always "} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(value, prefix)), false, true
		}
	}
	return "", false, false
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
