package compiler

import (
	"context"
	"strings"
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestImproveDeterministicIdempotentAndPreservesPaths(t *testing.T) {
	intent := "Update C:\\synthetic\\fixture and /synthetic/fixture; preserve caveat."
	req := contracts.ImprovePromptRequest{
		SchemaVersion:   contracts.SchemaVersion,
		Kind:            "request",
		Intent:          intent,
		ExecutionPolicy: contracts.ExecutionPolicyImproveOnly,
	}
	first, err := Improve(context.Background(), req, policy.HostUnknown)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Improve(context.Background(), req, policy.HostUnknown)
	if err != nil {
		t.Fatal(err)
	}
	if first.ImprovedPrompt != second.ImprovedPrompt {
		t.Fatal("non-deterministic output")
	}
	for _, value := range []string{intent, "Goal:", "Success criteria:", "Approval boundary:", "Validation:", "Stop:"} {
		if !strings.Contains(first.ImprovedPrompt, value) {
			t.Fatalf("missing %q", value)
		}
	}
	req.Intent = first.ImprovedPrompt
	third, err := Improve(context.Background(), req, policy.HostUnknown)
	if err != nil {
		t.Fatal(err)
	}
	if third.ImprovedPrompt != first.ImprovedPrompt {
		t.Fatal("improvement is not idempotent")
	}
}

func TestImproveIdempotentWithLeadingOptionalSections(t *testing.T) {
	request := improveRequest("Synthetic")
	plan := NewPlan("Synthetic")
	plan.Role = "Reviewer"
	plan.Personality = "Direct"
	plan.CollaborationStyle = "Report evidence."
	stable := "Preserve durable constraints."
	plan.StablePrefix = &stable
	request.PromptPlan = &plan

	first, err := Improve(context.Background(), request, policy.HostUnknown)
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := improveRequest(first.ImprovedPrompt)
	second, err := Improve(context.Background(), secondRequest, policy.HostUnknown)
	if err != nil {
		t.Fatal(err)
	}
	if second.ImprovedPrompt != first.ImprovedPrompt {
		t.Fatalf("compiled prompt changed:\n%s", second.ImprovedPrompt)
	}
}

func TestImproveCompletesPartialStructuredDraft(t *testing.T) {
	partial := "Goal:\nReturn a synthetic result.\n\nStop:\nStop after one result."
	result, err := Improve(
		context.Background(),
		improveRequest(partial),
		policy.HostUnknown,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ImprovedPrompt == partial {
		t.Fatal("partial draft bypassed compilation")
	}
	for _, section := range []string{
		"Success criteria:",
		"Evidence:",
		"Approval boundary:",
		"Validation:",
	} {
		if !strings.Contains(result.ImprovedPrompt, section) {
			t.Fatalf("missing %s", section)
		}
	}
}

func TestImproveCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Improve(ctx, contracts.ImprovePromptRequest{}, policy.HostUnknown)
	if err == nil {
		t.Fatal("cancelled work succeeded")
	}
}

func TestCreateGoalHouseOrder(t *testing.T) {
	plan := NewPlan("Synthetic review")
	plan.Role = "Reviewer"
	stable, dynamic := "Preserve contracts.", "Current head abcdef1."
	plan.StablePrefix = &stable
	plan.DynamicTail = &dynamic
	result, err := CreateGoal(context.Background(), contracts.CreateGoalPromptRequest{
		SchemaVersion: contracts.SchemaVersion,
		Kind:          "request",
		Objective:     "Synthetic review",
		PromptPlan:    plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(
		result.GoalPrompt,
		"Take this as a new goal:\n\nSynthetic review",
	) || !result.CheckpointRequired {
		t.Fatalf("bad goal prompt: %s", result.GoalPrompt)
	}
	order := []string{
		"Role:", "Stable prefix:", "Success criteria:", "Dynamic context:",
		"Validation:", "Output:", "Stop:",
	}
	prior := -1
	for _, marker := range order {
		index := strings.Index(result.GoalPrompt, marker)
		if index <= prior {
			t.Fatalf("%s out of order", marker)
		}
		prior = index
	}
}

func TestCreateReviewFix(t *testing.T) {
	request := reviewRequest(
		[]string{"Missing unknown-state test.", "Missing unknown-state test."},
	)
	result, err := CreateReviewFix(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FailureClasses) != 1 ||
		!result.SamePatternScanRequired ||
		!strings.Contains(result.FixPrompt, "Scan directly analogous paths") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCreateReviewFixPreservesUTF8WhenClassIsTruncated(t *testing.T) {
	finding := strings.Repeat("界", 100)
	result, err := CreateReviewFix(
		context.Background(),
		reviewRequest([]string{finding}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FailureClasses[0]) > 200 || !strings.HasSuffix(result.FailureClasses[0], "界") {
		t.Fatalf("unsafe class truncation: %q", result.FailureClasses[0])
	}
}

func TestInvalidPlanReportsFirstFieldDeterministically(t *testing.T) {
	plan := NewPlan("Synthetic")
	plan.SuccessCriteria = nil
	plan.Invariants = nil
	request := improveRequest("Synthetic")
	request.PromptPlan = &plan
	for range 100 {
		_, err := Improve(context.Background(), request, policy.HostUnknown)
		if err == nil || !strings.Contains(err.Error(), "success_criteria") {
			t.Fatalf("non-deterministic validation: %v", err)
		}
	}
}

func TestImproveRejectsInvalidUTF8InPlan(t *testing.T) {
	plan := NewPlan("Synthetic")
	plan.Invariants = []string{string([]byte{0xff})}
	request := improveRequest("Synthetic")
	request.PromptPlan = &plan
	if _, err := Improve(context.Background(), request, policy.HostUnknown); err == nil {
		t.Fatal("invalid utf-8 plan accepted")
	}
}

func TestValidateBudget(t *testing.T) {
	integer := func(value int) *int { return &value }
	valid := contracts.ExecutionBudget{
		SchemaVersion:     contracts.SchemaVersion,
		Enforcement:       "advisory",
		ActivePhases:      []string{"implementation"},
		DelegationPolicy:  "bounded",
		MaxAgentDepth:     integer(1),
		MaxConcurrency:    integer(2),
		ContextMode:       "minimal",
		ExhaustionOutcome: "stop",
	}
	if err := validateBudget(valid); err != nil {
		t.Fatalf("valid budget rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*contracts.ExecutionBudget)
	}{
		{name: "version", mutate: func(b *contracts.ExecutionBudget) { b.SchemaVersion = "2" }},
		{name: "enforcement", mutate: func(b *contracts.ExecutionBudget) { b.Enforcement = "invalid" }},
		{name: "empty phases", mutate: func(b *contracts.ExecutionBudget) { b.ActivePhases = nil }},
		{name: "duplicate phase", mutate: func(b *contracts.ExecutionBudget) {
			b.ActivePhases = []string{"review", "review"}
		}},
		{name: "unknown phase", mutate: func(b *contracts.ExecutionBudget) { b.ActivePhases = []string{"invalid"} }},
		{name: "delegation", mutate: func(b *contracts.ExecutionBudget) { b.DelegationPolicy = "invalid" }},
		{name: "missing bound", mutate: func(b *contracts.ExecutionBudget) { b.MaxAgentDepth = nil }},
		{name: "forbidden bound", mutate: func(b *contracts.ExecutionBudget) { b.DelegationPolicy = "none" }},
		{name: "retry range", mutate: func(b *contracts.ExecutionBudget) { b.MaxRetries = integer(101) }},
		{name: "concurrency range", mutate: func(b *contracts.ExecutionBudget) { b.MaxConcurrency = integer(65) }},
		{name: "context mode", mutate: func(b *contracts.ExecutionBudget) { b.ContextMode = "invalid" }},
		{name: "exhaustion", mutate: func(b *contracts.ExecutionBudget) { b.ExhaustionOutcome = "invalid" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			budget := valid
			test.mutate(&budget)
			if err := validateBudget(budget); err == nil {
				t.Fatal("invalid budget accepted")
			}
		})
	}
}

func TestImprovePreservesRepresentativeSemantics(t *testing.T) {
	tests := []struct{ name, intent, required string }{
		{name: "explicit value", intent: "Keep retry limit exactly 3.", required: "retry limit exactly 3"},
		{name: "fact", intent: "The synthetic version is 1.2.3.", required: "version is 1.2.3"},
		{name: "artifact", intent: "Return one JSON object.", required: "one JSON object"},
		{name: "length", intent: "Limit output to 80 words.", required: "80 words"},
		{name: "language", intent: "Respond in French.", required: "Respond in French"},
		{name: "caveat", intent: "State that evidence is incomplete.", required: "evidence is incomplete"},
		{name: "authorization", intent: "Plan only; do not implement.", required: "do not implement"},
		{name: "phase stop", intent: "Review once, then stop.", required: "then stop"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Improve(
				context.Background(),
				improveRequest(test.intent),
				policy.HostUnknown,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.ImprovedPrompt, test.required) {
				t.Fatalf("lost %q: %s", test.required, result.ImprovedPrompt)
			}
		})
	}
}

func improveRequest(intent string) contracts.ImprovePromptRequest {
	return contracts.ImprovePromptRequest{
		SchemaVersion:   contracts.SchemaVersion,
		Kind:            "request",
		Intent:          intent,
		ExecutionPolicy: contracts.ExecutionPolicyImproveOnly,
	}
}

func reviewRequest(findings []string) contracts.CreateReviewFixPromptRequest {
	return contracts.CreateReviewFixPromptRequest{
		SchemaVersion: contracts.SchemaVersion,
		Kind:          "request",
		Findings:      findings,
		ReviewHead:    "abcdef1",
	}
}
