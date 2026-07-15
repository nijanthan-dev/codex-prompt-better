package compiler

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
	"github.com/nijanthan-dev/codex-prompt-better/internal/textutil"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

var reviewHeadPattern = regexp.MustCompile(`^[a-f0-9]{7,64}$`)

// Improve compiles one v1 prompt request without executing it.
func Improve(
	ctx context.Context,
	request contracts.ImprovePromptRequest,
	host policy.HostPermission,
) (contracts.ImprovePromptResult, error) {
	if err := checkContext(ctx); err != nil {
		return contracts.ImprovePromptResult{}, err
	}
	intent, plan, err := prepareImproveRequest(request)
	if err != nil {
		return contracts.ImprovePromptResult{}, err
	}
	if err := checkContext(ctx); err != nil {
		return contracts.ImprovePromptResult{}, err
	}
	compiled := intent
	if !looksCompiled(intent) || request.PromptPlan != nil {
		compiled = RenderPlan(plan, true)
	}
	if len(compiled) > 16000 {
		return contracts.ImprovePromptResult{}, semanticInvalid("compiled prompt exceeds 16000 bytes", "improved_prompt")
	}
	outcome, policyErr := policy.Resolve(request.ExecutionPolicy, plan.PhaseScope, host)
	if policyErr != nil {
		return contracts.ImprovePromptResult{}, policyErr
	}
	return contracts.ImprovePromptResult{
		SchemaVersion: contracts.SchemaVersion, Kind: "result", ImprovedPrompt: compiled,
		PolicyOutcome: outcome, Diagnostics: []string{},
	}, nil
}

// ValidateImproveRequest validates the unmodified v1 request contract.
func ValidateImproveRequest(request contracts.ImprovePromptRequest) error {
	_, _, err := prepareImproveRequest(request)
	return err
}

func prepareImproveRequest(
	request contracts.ImprovePromptRequest,
) (string, contracts.PromptPlan, error) {
	if request.SchemaVersion != contracts.SchemaVersion || request.Kind != "request" {
		return "", contracts.PromptPlan{}, invalid(
			"request must use schema 1.0.0 and kind request",
			"request",
		)
	}
	if !policy.ValidExecutionPolicy(request.ExecutionPolicy) {
		return "", contracts.PromptPlan{}, invalid(
			"invalid execution policy",
			"execution_policy",
		)
	}
	intent, err := textutil.Normalize(request.Intent)
	if err != nil {
		return "", contracts.PromptPlan{}, err
	}
	if len(intent) > 8000 {
		return "", contracts.PromptPlan{}, invalid("intent exceeds 8000 bytes", "intent")
	}
	plan := defaultPlan(intent)
	if request.PromptPlan != nil {
		plan = *request.PromptPlan
		if err := validatePlan(plan); err != nil {
			return "", contracts.PromptPlan{}, err
		}
	}
	if request.Budget != nil {
		if err := validateBudget(*request.Budget); err != nil {
			return "", contracts.PromptPlan{}, err
		}
	}
	return intent, plan, nil
}

// CreateGoal renders one v1 goal request in the house format.
func CreateGoal(
	ctx context.Context,
	request contracts.CreateGoalPromptRequest,
) (contracts.CreateGoalPromptResult, error) {
	if err := checkContext(ctx); err != nil {
		return contracts.CreateGoalPromptResult{}, err
	}
	if request.SchemaVersion != contracts.SchemaVersion || request.Kind != "request" {
		return contracts.CreateGoalPromptResult{}, invalid("request must use schema 1.0.0 and kind request", "request")
	}
	objective, err := textutil.Normalize(request.Objective)
	if err != nil {
		return contracts.CreateGoalPromptResult{}, err
	}
	if err := validatePlan(request.PromptPlan); err != nil {
		return contracts.CreateGoalPromptResult{}, err
	}
	body := RenderPlan(request.PromptPlan, false)
	prompt := "Take this as a new goal:\n\n" + objective
	if body != "" {
		prompt += "\n\n" + body
	}
	if len(objective) > 4000 || len(prompt) > 16000 {
		return contracts.CreateGoalPromptResult{}, invalid("goal request or result exceeds contract limit", "objective")
	}
	return contracts.CreateGoalPromptResult{
		SchemaVersion:      contracts.SchemaVersion,
		Kind:               "result",
		GoalPrompt:         prompt,
		CheckpointRequired: true,
	}, nil
}

// CreateReviewFix renders bounded remediation for one review head.
func CreateReviewFix(
	ctx context.Context,
	request contracts.CreateReviewFixPromptRequest,
) (contracts.CreateReviewFixPromptResult, error) {
	if err := checkContext(ctx); err != nil {
		return contracts.CreateReviewFixPromptResult{}, err
	}
	if request.SchemaVersion != contracts.SchemaVersion || request.Kind != "request" {
		return contracts.CreateReviewFixPromptResult{}, invalid("request must use schema 1.0.0 and kind request", "request")
	}
	if len(request.Findings) == 0 || len(request.Findings) > 100 {
		return contracts.CreateReviewFixPromptResult{}, invalid("findings must contain 1 to 100 items", "findings")
	}
	if !reviewHeadPattern.MatchString(request.ReviewHead) {
		return contracts.CreateReviewFixPromptResult{}, invalid("review_head must be a commit hash", "review_head")
	}
	findings := make([]string, 0, len(request.Findings))
	classes := make([]string, 0, len(request.Findings))
	seen := map[string]bool{}
	for _, item := range request.Findings {
		value, err := textutil.Normalize(item)
		if err != nil {
			return contracts.CreateReviewFixPromptResult{}, invalid("finding must not be empty", "findings")
		}
		if len(value) > 1000 {
			return contracts.CreateReviewFixPromptResult{}, invalid("finding exceeds 1000 bytes", "findings")
		}
		findings = append(findings, value)
		class := failureClass(value)
		if !seen[class] {
			seen[class] = true
			classes = append(classes, class)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Fix review findings for head %s.\n\nFindings:\n", request.ReviewHead)
	for _, finding := range findings {
		fmt.Fprintf(&b, "- %s\n", finding)
	}
	b.WriteString(reviewFixInstructions)
	if b.Len() > 16000 {
		return contracts.CreateReviewFixPromptResult{}, semanticInvalid("review fix prompt exceeds 16000 bytes", "findings")
	}
	return contracts.CreateReviewFixPromptResult{
		SchemaVersion:           contracts.SchemaVersion,
		Kind:                    "result",
		FixPrompt:               b.String(),
		FailureClasses:          classes,
		SamePatternScanRequired: true,
	}, nil
}

const reviewFixInstructions = `
For each finding:
- State the violated invariant.
- Scan directly analogous paths for the same failure class.
- Add the smallest focused regression proof.
- Apply the fix without scope expansion.
- Run focused validation, then reply and resolve the review thread.

Stop after all listed findings and same-pattern gaps are validated; do not start unrelated work.`

func defaultPlan(intent string) contracts.PromptPlan {
	return contracts.PromptPlan{
		SchemaVersion: contracts.SchemaVersion, Goal: intent,
		SuccessCriteria: []string{"Complete the requested outcome without dropping stated requirements."},
		Invariants: []string{
			"Preserve explicit facts, decisions, caveats, language, safety, privacy, and authorization boundaries.",
		},
		DecisionRules:        []string{"Ask only when material ambiguity prevents a safe result."},
		EvidenceRequirements: []string{"Use the evidence required by the request; do not invent support."},
		Tools:                []string{"Use only task-relevant tools within granted permissions."},
		OutputContract:       "Return the requested artifact and material next action.",
		ApprovalBoundary:     "Do not execute, expand scope, or take external action without applicable authorization.",
		PhaseScope:           "design",
		StopRules: []string{
			"Stop when the result is validated; if blocked, report the blocker and next safe action.",
		},
		FallbackRules: []string{"Return the safest useful partial result with missing evidence identified."},
		AbstainRules:  []string{"Abstain from unsafe, unauthorized, or unsupported claims."},
		ValidationBar: []string{"Check the artifact against the goal, constraints, evidence, and requested format."},
	}
}

// NewPlan returns the deterministic default plan for plain input.
func NewPlan(intent string) contracts.PromptPlan { return defaultPlan(intent) }

func validatePlan(plan contracts.PromptPlan) error {
	if plan.SchemaVersion != contracts.SchemaVersion {
		return invalid("prompt plan must use schema 1.0.0", "prompt_plan.schema_version")
	}
	if !validText(plan.Goal, 2000, true) {
		return invalid("prompt plan goal is required", "prompt_plan.goal")
	}
	required := []struct {
		name   string
		values []string
	}{
		{name: "success_criteria", values: plan.SuccessCriteria},
		{name: "invariants", values: plan.Invariants},
		{name: "decision_rules", values: plan.DecisionRules},
		{name: "evidence_requirements", values: plan.EvidenceRequirements},
		{name: "tools", values: plan.Tools},
		{name: "stop_rules", values: plan.StopRules},
		{name: "fallback_rules", values: plan.FallbackRules},
		{name: "abstain_rules", values: plan.AbstainRules},
		{name: "validation_bar", values: plan.ValidationBar},
	}
	for _, field := range required {
		if err := validateList(field.values, true); err != nil {
			return invalid(field.name+" is required", "prompt_plan."+field.name)
		}
	}
	if len(plan.ArtifactPriorities) > 0 {
		if err := validateList(plan.ArtifactPriorities, false); err != nil {
			return invalid("invalid artifact priorities", "prompt_plan.artifact_priorities")
		}
	}
	if !validText(plan.OutputContract, 2000, true) || !validText(plan.ApprovalBoundary, 1000, true) {
		return invalid("output, approval, and phase contracts are required", "prompt_plan")
	}
	if !validPhase(plan.PhaseScope) {
		return invalid("invalid phase scope", "prompt_plan.phase_scope")
	}
	optionalText := []struct{ name, value string }{
		{name: "role", value: plan.Role},
		{name: "personality", value: plan.Personality},
		{name: "collaboration_style", value: plan.CollaborationStyle},
	}
	for _, field := range optionalText {
		if !validText(field.value, 500, false) {
			return invalid(field.name+" exceeds 500 bytes", "prompt_plan."+field.name)
		}
	}
	if !validText(plan.OutputLanguage, 80, false) {
		return invalid("output language exceeds 80 bytes", "prompt_plan.output_language")
	}
	if plan.StablePrefix != nil && !validText(*plan.StablePrefix, 4000, false) {
		return invalid("stable prefix exceeds 4000 bytes", "prompt_plan.stable_prefix")
	}
	if plan.DynamicTail != nil && !validText(*plan.DynamicTail, 4000, false) {
		return invalid("dynamic tail exceeds 4000 bytes", "prompt_plan.dynamic_tail")
	}
	return nil
}

func validateList(values []string, required bool) error {
	if (required && len(values) == 0) || len(values) > 50 {
		return invalid("invalid list size", "prompt_plan")
	}
	for _, value := range values {
		if !validText(value, 1000, true) {
			return invalid("invalid list item", "prompt_plan")
		}
	}
	return nil
}

func validText(value string, limit int, required bool) bool {
	if !utf8.ValidString(value) || len(value) > limit {
		return false
	}
	return !required || strings.TrimSpace(value) != ""
}

func validPhase(value string) bool {
	switch value {
	case "research", "design", "implementation", "review", "external_coordination":
		return true
	default:
		return false
	}
}

func validateBudget(b contracts.ExecutionBudget) error {
	if b.SchemaVersion != contracts.SchemaVersion {
		return invalid("execution budget must use schema 1.0.0", "budget.schema_version")
	}
	if b.Enforcement != "host_enforced" && b.Enforcement != "advisory" && b.Enforcement != "unknown" {
		return invalid("invalid budget enforcement", "budget.enforcement")
	}
	if len(b.ActivePhases) == 0 {
		return invalid("active phases are required", "budget.active_phases")
	}
	seen := map[string]bool{}
	for _, phase := range b.ActivePhases {
		if !validPhase(phase) || seen[phase] {
			return invalid("invalid or duplicate active phase", "budget.active_phases")
		}
		seen[phase] = true
	}
	if b.DelegationPolicy != "none" && b.DelegationPolicy != "user_requested_only" && b.DelegationPolicy != "bounded" {
		return invalid("invalid delegation policy", "budget.delegation_policy")
	}
	if !oneOf(b.ExhaustionOutcome, "stop", "fallback", "abstain", "approval_required") {
		return invalid("invalid exhaustion outcome", "budget.exhaustion_outcome")
	}
	if b.ContextMode != "" && !oneOf(b.ContextMode, "none", "minimal", "selected", "full", "unknown") {
		return invalid("invalid context mode", "budget.context_mode")
	}
	validRanges := within(b.MaxUsefulToolLoops, 0, 1000) &&
		within(b.MaxRetries, 0, 100) &&
		within(b.MaxRetrievalExpansions, 0, 100) &&
		within(b.MaxAgentDepth, 0, 16) &&
		within(b.MaxConcurrency, 1, 64)
	if !validRanges {
		return invalid("budget value out of range", "budget")
	}
	if b.DelegationPolicy == "bounded" && (b.MaxAgentDepth == nil || b.MaxConcurrency == nil) {
		return invalid("bounded delegation requires depth and concurrency", "budget.delegation_policy")
	}
	if b.DelegationPolicy != "bounded" && (b.MaxAgentDepth != nil || b.MaxConcurrency != nil) {
		return invalid("unbounded fields forbidden for this delegation policy", "budget.delegation_policy")
	}
	return nil
}

func within(value *int, min, max int) bool { return value == nil || *value >= min && *value <= max }

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func RenderPlan(plan contracts.PromptPlan, includeGoal bool) string {
	var sections []string
	addText := func(title, value string) {
		if strings.TrimSpace(value) != "" {
			sections = append(sections, title+":\n"+strings.TrimSpace(value))
		}
	}
	addList := func(title string, values []string) {
		if len(values) == 0 {
			return
		}
		var b strings.Builder
		b.WriteString(title + ":\n")
		for _, value := range values {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(value))
		}
		sections = append(sections, strings.TrimRight(b.String(), "\n"))
	}
	addText("Role", plan.Role)
	addText("Personality", plan.Personality)
	addText("Collaboration", plan.CollaborationStyle)
	if plan.StablePrefix != nil {
		addText("Stable prefix", *plan.StablePrefix)
	}
	if includeGoal {
		addText("Goal", plan.Goal)
	}
	addList("Success criteria", plan.SuccessCriteria)
	addList("Constraints", plan.Invariants)
	addList("Decision rules", plan.DecisionRules)
	addList("Evidence", plan.EvidenceRequirements)
	addList("Tools", plan.Tools)
	addText("Approval boundary", plan.ApprovalBoundary)
	addText("Phase", plan.PhaseScope)
	if plan.DynamicTail != nil {
		addText("Dynamic context", *plan.DynamicTail)
	}
	addList("Validation", plan.ValidationBar)
	addText("Output", plan.OutputContract)
	addText("Output language", plan.OutputLanguage)
	addList("Stop", plan.StopRules)
	addList("Fallback", plan.FallbackRules)
	addList("Abstain", plan.AbstainRules)
	return strings.Join(sections, "\n\n")
}

func failureClass(finding string) string {
	value := strings.ToLower(strings.TrimSpace(strings.Split(finding, "\n")[0]))
	value = strings.Trim(value, ".:; ")
	value = truncateUTF8(value, 200)
	if value == "" {
		return "review finding"
	}
	return value
}

func truncateUTF8(value string, limit int) string {
	for len(value) > limit {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}

func looksCompiled(value string) bool {
	required := []string{
		"Goal",
		"Success criteria",
		"Constraints",
		"Decision rules",
		"Evidence",
		"Tools",
		"Approval boundary",
		"Phase",
		"Validation",
		"Output",
		"Stop",
		"Fallback",
		"Abstain",
	}
	previous := -1
	for _, title := range required {
		index := sectionIndex(value, title)
		if index <= previous {
			return false
		}
		previous = index
	}
	return true
}

func sectionIndex(value, title string) int {
	heading := title + ":\n"
	if strings.HasPrefix(value, heading) {
		return 0
	}
	index := strings.Index(value, "\n\n"+heading)
	if index < 0 {
		return -1
	}
	return index + 2
}

func checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return contracts.NewError(contracts.ErrorCodeBudgetExhausted, "operation cancelled or timed out", "context", true)
	default:
		return nil
	}
}

func invalid(message, field string) error {
	return contracts.NewError(contracts.ErrorCodeInvalidSchema, message, field, false)
}

func semanticInvalid(message, field string) error {
	return contracts.NewError(contracts.ErrorCodeSemanticInvalid, message, field, false)
}
