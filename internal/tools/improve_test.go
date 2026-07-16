package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nijanthan-dev/codex-prompt-better/internal/compiler"
	"github.com/nijanthan-dev/codex-prompt-better/internal/mcpserver"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestImprovePrompt_SuccessSchemaErrorAndLimit(t *testing.T) {
	server := newTestServer(t)
	if _, err := RegisterAll(server, nil); err != nil {
		t.Fatal(err)
	}
	ctx, session := connectTestClient(t, server)
	tools, err := session.ListTools(ctx, nil)
	if err != nil || !hasTool(tools.Tools, "improve_prompt") {
		t.Fatalf("tool list=%#v error=%v", tools, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "improve_prompt", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "intent": "Return a synthetic result.",
		"execution_policy": "improve_only",
	}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("success result=%#v error=%v", result, err)
	}
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "improve_prompt", Arguments: map[string]any{"unknown": true}})
	if err != nil || !invalid.IsError || !strings.Contains(invalid.Content[0].(*mcp.TextContent).Text, "invalid_schema") {
		t.Fatalf("invalid result=%#v error=%v", invalid, err)
	}
	oversized, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "improve_prompt", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "intent": strings.Repeat("x", 8001),
		"execution_policy": "improve_only",
	}})
	if err != nil || !oversized.IsError {
		t.Fatalf("oversized result=%#v error=%v", oversized, err)
	}
}

func TestImprovePrompt_ConfiguredPolicyIsMaximum(t *testing.T) {
	server := newTestServer(t)
	service, err := RegisterWithConfig(server, nil, Configuration{
		ExecutionPolicy: contracts.ExecutionPolicyImproveOnly,
		SourceKinds:     []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.improvePrompt(context.Background(), "synthetic", contracts.ImprovePromptRequest{
		SchemaVersion:   contracts.SchemaVersion,
		Kind:            "request",
		Intent:          "Return a synthetic result.",
		ExecutionPolicy: contracts.ExecutionPolicyAskBeforeExecute,
	})
	var stable *contracts.StableError
	if !errors.As(err, &stable) || stable.Code != contracts.ErrorCodePermissionDenied {
		t.Fatalf("broader policy error=%v", err)
	}
}

func TestImprovePrompt_InvalidPolicyRemainsSchemaError(t *testing.T) {
	server := newTestServer(t)
	service, err := RegisterWithConfig(server, nil, Configuration{
		ExecutionPolicy: contracts.ExecutionPolicyImproveOnly,
		SourceKinds:     []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.improvePrompt(context.Background(), "synthetic", contracts.ImprovePromptRequest{
		SchemaVersion:   contracts.SchemaVersion,
		Kind:            "request",
		Intent:          "Return a synthetic result.",
		ExecutionPolicy: "invalid",
	})
	var stable *contracts.StableError
	if !errors.As(err, &stable) || stable.Code != contracts.ErrorCodeInvalidSchema {
		t.Fatalf("invalid policy error=%v", err)
	}
}

func hasTool(tools []*mcp.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func TestCreateGoalPrompt_SuccessErrorAndLimit(t *testing.T) {
	server := newTestServer(t)
	if _, err := RegisterAll(server, nil); err != nil {
		t.Fatal(err)
	}
	ctx, session := connectTestClient(t, server)
	plan := compiler.NewPlan("Create a synthetic goal.")
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_goal_prompt", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "objective": "Create a synthetic goal.",
		"prompt_plan": plan,
	}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("success result=%#v error=%v", result, err)
	}
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_goal_prompt", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "objective": strings.Repeat("x", 4001),
		"prompt_plan": plan,
	}})
	if err != nil || !invalid.IsError {
		t.Fatalf("invalid result=%#v error=%v", invalid, err)
	}
}

func TestCreateReviewFixPrompt_SuccessErrorAndLimit(t *testing.T) {
	server := newTestServer(t)
	if _, err := RegisterAll(server, nil); err != nil {
		t.Fatal(err)
	}
	ctx, session := connectTestClient(t, server)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_review_fix_prompt", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request",
		"findings": []string{"Synthetic validation gap."}, "review_head": "abcdef1",
	}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("success result=%#v error=%v", result, err)
	}
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_review_fix_prompt", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request",
		"findings": []string{"Synthetic validation gap."}, "review_head": "not-a-head",
	}})
	if err != nil || !invalid.IsError {
		t.Fatalf("invalid result=%#v error=%v", invalid, err)
	}
}

func TestLintPrompt_SuccessErrorAndLimit(t *testing.T) {
	server := newTestServer(t)
	if _, err := RegisterAll(server, nil); err != nil {
		t.Fatal(err)
	}
	ctx, session := connectTestClient(t, server)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "lint_prompt", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "candidate": "Goal:\nReturn a synthetic result.",
	}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("success result=%#v error=%v", result, err)
	}
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "lint_prompt", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "candidate": strings.Repeat("x", 16001),
	}})
	if err != nil || !invalid.IsError {
		t.Fatalf("invalid result=%#v error=%v", invalid, err)
	}
}

func TestGetCheckpoint_LatestNotFoundAndSessionIsolation(t *testing.T) {
	server := newTestServer(t)
	if _, err := RegisterAll(server, nil); err != nil {
		t.Fatal(err)
	}
	ctx, first := connectTestClient(t, server)
	missing, err := first.CallTool(ctx, &mcp.CallToolParams{Name: "get_checkpoint", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "reference": "latest",
	}})
	if err != nil || !missing.IsError || !strings.Contains(missing.Content[0].(*mcp.TextContent).Text, "not_found") {
		t.Fatalf("missing result=%#v error=%v", missing, err)
	}
	if _, err := first.CallTool(ctx, &mcp.CallToolParams{Name: "improve_prompt", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "intent": "Return a synthetic result.",
		"execution_policy": "improve_only",
	}}); err != nil {
		t.Fatal(err)
	}
	latest, err := first.CallTool(ctx, &mcp.CallToolParams{Name: "get_checkpoint", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "reference": "latest",
	}})
	if err != nil || latest.IsError || latest.StructuredContent == nil {
		t.Fatalf("latest result=%#v error=%v", latest, err)
	}
	unknown, err := first.CallTool(ctx, &mcp.CallToolParams{Name: "get_checkpoint", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "reference": "other",
	}})
	if err != nil || !unknown.IsError {
		t.Fatalf("unknown result=%#v error=%v", unknown, err)
	}

	_, second := connectTestClient(t, server)
	isolated, err := second.CallTool(ctx, &mcp.CallToolParams{Name: "get_checkpoint", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "reference": "latest",
	}})
	if err != nil || !isolated.IsError {
		t.Fatalf("isolated result=%#v error=%v", isolated, err)
	}
}

func TestCheckpoint_BoundsPromptPlanItemsToResultSchema(t *testing.T) {
	plan := compiler.NewPlan("Create a synthetic goal.")
	plan.Invariants = []string{strings.Repeat("i", 1000)}
	plan.Gates = []string{strings.Repeat("g", 1000)}
	diagnostics := make([]string, 100)
	for index := range diagnostics {
		diagnostics[index] = fmt.Sprintf("diagnostic-%d", index)
	}
	checkpoint := checkpointForImprove(
		contracts.ImprovePromptRequest{Intent: "synthetic"},
		contracts.ImprovePromptResult{PolicyOutcome: contracts.PolicyOutcomeReturnOnly, Diagnostics: diagnostics},
		plan,
	)
	if len(checkpoint.Constraints[0]) != maxCheckpointItem || len(checkpoint.RemainingGates[0]) != maxCheckpointItem {
		t.Fatalf("checkpoint item lengths=%d,%d", len(checkpoint.Constraints[0]), len(checkpoint.RemainingGates[0]))
	}
	if len(checkpoint.Validation) != maxCheckpointItems {
		t.Fatalf("checkpoint validation items=%d", len(checkpoint.Validation))
	}
}

func TestCheckpoint_BoundsReviewFailureClasses(t *testing.T) {
	service := &Service{server: newTestServer(t), state: map[string]*sessionState{}}
	findings := make([]string, 100)
	for index := range findings {
		findings[index] = fmt.Sprintf("failure class %d", index)
	}
	if _, err := service.createReviewFixPrompt(context.Background(), "synthetic", contracts.CreateReviewFixPromptRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request", ReviewHead: "abcdef1", Findings: findings,
	}); err != nil {
		t.Fatal(err)
	}
	if got := len(service.state["synthetic"].checkpoint.AcceptedDecisions); got != maxCheckpointItems {
		t.Fatalf("review checkpoint decisions=%d", got)
	}
}

func TestSuccessResult_EncodesOversizeAsStableToolError(t *testing.T) {
	result := successResult(map[string]string{"value": strings.Repeat("x", maxResultBytes)})
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("oversize result=%#v", result)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	var stable contracts.StableError
	if err := json.Unmarshal([]byte(text), &stable); err != nil {
		t.Fatalf("decode result error: %v", err)
	}
	if stable.Code != contracts.ErrorCodeBudgetExhausted ||
		stable.FieldPath == nil || *stable.FieldPath != "result" || stable.Retryable {
		t.Fatalf("unstable result error=%#v", stable)
	}
}

func TestSessionCache_ExistingSessionDoesNotEvictAtCapacity(t *testing.T) {
	service := &Service{server: newTestServer(t), state: map[string]*sessionState{}}
	for index := 0; index < 8; index++ {
		service.state[fmt.Sprintf("session-%d", index)] = &sessionState{}
	}
	checkpoint := contracts.GetCheckpointResult{SchemaVersion: contracts.SchemaVersion, Kind: "result", Objective: "synthetic"}
	service.saveCheckpoint("session-0", checkpoint)
	if len(service.state) != 8 || service.state["session-0"].checkpoint == nil {
		t.Fatalf("existing session evicted: %#v", service.state)
	}
	service.saveCheckpoint("session-new", checkpoint)
	if len(service.state) != 1 || service.state["session-new"] == nil {
		t.Fatalf("bounded eviction failed: %#v", service.state)
	}
}

func TestSessionCache_PrunesDisconnectedClient(t *testing.T) {
	server := newTestServer(t)
	service, err := RegisterAll(server, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, first := connectTestClient(t, server)
	callImprove(t, ctx, first, "first")
	service.mu.Lock()
	var oldKey string
	for key := range service.state {
		oldKey = key
	}
	service.mu.Unlock()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		active := 0
		for range server.SDK().Sessions() {
			active++
		}
		if active == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	ctx, second := connectTestClient(t, server)
	callImprove(t, ctx, second, "second")
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.state[oldKey] != nil || len(service.state) != 1 {
		t.Fatalf("disconnected state retained: %#v", service.state)
	}
}

func callImprove(t *testing.T, ctx context.Context, session *mcp.ClientSession, intent string) {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "improve_prompt", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "intent": intent, "execution_policy": "improve_only",
	}})
	if err != nil || result.IsError {
		t.Fatalf("improve result=%#v err=%v", result, err)
	}
}

func newTestServer(t *testing.T) *mcpserver.Server {
	t.Helper()
	server, err := mcpserver.New(mcpserver.Options{
		MaxInputBytes: 64 * 1024, MaxConcurrent: 4, Timeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func connectTestClient(t *testing.T, server *mcpserver.Server) (context.Context, *mcp.ClientSession) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = server.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "synthetic-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return ctx, session
}
