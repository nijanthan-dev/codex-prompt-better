package tools

import (
	"context"
	"errors"
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

type fakeAuditStore struct {
	sessionID string
	sources   []string
	asOf      *time.Time
}

func (store *fakeAuditStore) AuditProject(_ context.Context, request contracts.AuditProjectRequest) (contracts.AuditProjectResult, error) {
	return contracts.AuditProjectResult{
		SchemaVersion: contracts.SchemaVersion, Kind: "result",
		AuditReference: request.Reference, Scope: request.Scope, AsOf: request.AsOf,
		RevisionHash: strings.Repeat("a", 64), Coverage: contracts.CoverageStateComplete,
		Metrics: []contracts.MetricResult{}, Findings: []contracts.AuditFinding{},
		Recommendations: []contracts.AuditRecommendation{{
			Code: "no_action", Action: "Keep the current workflow.",
			Verification: "Re-audit comparable evidence.", EvidenceRefs: []string{},
		}},
		GovernanceOverhead: []contracts.MetricResult{},
	}, nil
}

func TestAllTools_TimeoutAndOverloadAreStable(t *testing.T) {
	requests := validToolRequests()
	for _, mode := range []string{"timeout", "overload"} {
		t.Run(mode, func(t *testing.T) {
			timeout := time.Second
			if mode == "timeout" {
				timeout = time.Nanosecond
			}
			server, err := mcpserver.New(mcpserver.Options{
				MaxInputBytes: 64 * 1024, MaxConcurrent: 1, Timeout: timeout,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := RegisterAll(server, &fakeAuditStore{}); err != nil {
				t.Fatal(err)
			}
			var release func()
			if mode == "overload" {
				_, release, err = server.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			ctx, session := connectTestClient(t, server)
			for name, arguments := range requests {
				result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
				if err != nil || !result.IsError {
					t.Fatalf("%s result=%#v err=%v", name, result, err)
				}
				if text := result.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, string(contracts.ErrorCodeBudgetExhausted)) {
					t.Fatalf("%s unstable bound error=%s", name, text)
				}
			}
		})
	}
}

func validToolRequests() map[string]map[string]any {
	plan := compiler.NewPlan("Synthetic goal.")
	reference := "00000000-0000-4000-8000-000000000001"
	return map[string]map[string]any{
		"improve_prompt": {
			"schema_version": contracts.SchemaVersion, "kind": "request", "intent": "Synthetic.",
			"execution_policy": "improve_only",
		},
		"create_goal_prompt": {
			"schema_version": contracts.SchemaVersion, "kind": "request", "objective": "Synthetic.", "prompt_plan": plan,
		},
		"create_review_fix_prompt": {
			"schema_version": contracts.SchemaVersion, "kind": "request", "findings": []string{"Synthetic."}, "review_head": "abcdef1",
		},
		"lint_prompt": {
			"schema_version": contracts.SchemaVersion, "kind": "request", "candidate": "Goal:\nSynthetic.",
		},
		"get_checkpoint": {
			"schema_version": contracts.SchemaVersion, "kind": "request", "reference": "latest",
		},
		"audit_session": {
			"schema_version": contracts.SchemaVersion, "kind": "request", "reference": reference,
			"consent": "granted", "configured_sources": []string{"git"},
		},
		"audit_project": {
			"schema_version": contracts.SchemaVersion, "kind": "request",
			"scope": "project", "reference": reference,
			"configured_sources": []string{"git"},
			"starts_at":          "2026-01-01T00:00:00Z", "ends_at": "2026-01-02T00:00:00Z",
			"as_of": "2026-01-02T00:00:00Z", "consent": "granted",
		},
		"render_governance_report": {
			"schema_version": contracts.SchemaVersion, "kind": "request", "audit_reference": reference, "format": "chat",
		},
	}
}

func TestAuditProjectRejectsSourceOutsideServerAllowlist(t *testing.T) {
	server := newTestServer(t)
	service, err := RegisterWithConfig(server, &fakeAuditStore{}, Configuration{
		ExecutionPolicy: contracts.ExecutionPolicyFollowUserIntent,
		SourceKinds:     []string{"git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.auditProject(context.Background(), "synthetic",
		contracts.AuditProjectRequest{
			SchemaVersion: contracts.SchemaVersion, Kind: "request",
			Scope: contracts.AuditScopeProject, Reference: "00000000-0000-0000-0000-000000000001",
			ConfiguredSources: []string{"process"},
			StartsAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			EndsAt:            time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			AsOf:              time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			Consent:           contracts.AuditConsentGranted,
		}); err == nil {
		t.Fatal("unconfigured project audit source accepted")
	}
}

func TestAuditSession_RejectsSourceOutsideServerAllowlist(t *testing.T) {
	server := newTestServer(t)
	service, err := RegisterWithConfig(server, &fakeAuditStore{}, Configuration{
		ExecutionPolicy: contracts.ExecutionPolicyFollowUserIntent,
		SourceKinds:     []string{"git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.auditSession(context.Background(), "one", contracts.AuditSessionRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request", Reference: "00000000-0000-4000-8000-000000000001",
		Consent: contracts.AuditConsentGranted, ConfiguredSources: []string{"github"},
	})
	var stable *contracts.StableError
	if !errors.As(err, &stable) || stable.Code != contracts.ErrorCodePermissionDenied {
		t.Fatalf("expected permission denial, got %v", err)
	}
}

func TestAuditSession_RejectsProcessWithoutConfiguredPurpose(t *testing.T) {
	server := newTestServer(t)
	service, err := RegisterWithConfig(server, &fakeAuditStore{}, Configuration{
		ExecutionPolicy: contracts.ExecutionPolicyImproveOnly,
		SourceKinds:     []string{"process"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.auditSession(context.Background(), "one", contracts.AuditSessionRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request", Reference: "00000000-0000-4000-8000-000000000001",
		Consent: contracts.AuditConsentGranted, ConfiguredSources: []string{"process"},
	})
	var stable *contracts.StableError
	if !errors.As(err, &stable) || stable.Code != contracts.ErrorCodePermissionDenied {
		t.Fatalf("expected purpose denial, got %v", err)
	}
}

func (store *fakeAuditStore) AuditSession(_ context.Context, sessionID string, sources []string, asOf *time.Time) (contracts.AuditSessionResult, []contracts.ProvenanceLabel, error) {
	store.sessionID = sessionID
	store.sources = append([]string{}, sources...)
	store.asOf = asOf
	return contracts.AuditSessionResult{
		SchemaVersion: contracts.SchemaVersion, Kind: "result", Coverage: contracts.CoverageStateComplete,
		EvidenceRefs: []string{"00000000-0000-4000-8000-000000000002"}, Findings: []string{"evidence_count:1"},
		RedactionApplied: true,
	}, []contracts.ProvenanceLabel{contracts.ProvenanceRuntimeObserved}, nil
}

func TestAuditSession_ConsentReferenceSourcesAndAsOf(t *testing.T) {
	store := &fakeAuditStore{}
	server := newTestServer(t)
	if _, err := RegisterAll(server, store); err != nil {
		t.Fatal(err)
	}
	ctx, session := connectTestClient(t, server)
	reference := "as-of:2026-07-01T00:00:00Z@00000000-0000-4000-8000-000000000001"
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "audit_session", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "reference": reference,
		"consent": "granted", "configured_sources": []string{"codex_jsonl"},
	}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("success result=%#v error=%v", result, err)
	}
	if store.sessionID != "00000000-0000-4000-8000-000000000001" || store.asOf == nil || len(store.sources) != 1 {
		t.Fatalf("store request session=%q sources=%v asOf=%v", store.sessionID, store.sources, store.asOf)
	}
	denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "audit_session", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "reference": "00000000-0000-4000-8000-000000000001",
		"consent": "denied", "configured_sources": []string{"codex_jsonl"},
	}})
	if err != nil || !denied.IsError {
		t.Fatalf("denied result=%#v error=%v", denied, err)
	}
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "audit_session", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "reference": "invalid",
		"consent": "granted", "configured_sources": []string{"codex_jsonl", "codex_jsonl"},
	}})
	if err != nil || !invalid.IsError {
		t.Fatalf("invalid result=%#v error=%v", invalid, err)
	}
}

func TestRenderGovernanceReport_SameReferenceFormatsAndIsolation(t *testing.T) {
	store := &fakeAuditStore{}
	server := newTestServer(t)
	if _, err := RegisterAll(server, store); err != nil {
		t.Fatal(err)
	}
	ctx, session := connectTestClient(t, server)
	reference := "00000000-0000-4000-8000-000000000001"
	if result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "audit_session", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "reference": reference,
		"consent": "granted", "configured_sources": []string{"codex_jsonl"},
	}}); err != nil || result.IsError {
		t.Fatalf("audit result=%#v error=%v", result, err)
	}
	for _, format := range []string{"chat", "markdown", "table"} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "render_governance_report", Arguments: map[string]any{
			"schema_version": contracts.SchemaVersion, "kind": "request", "audit_reference": reference, "format": format,
		}})
		if err != nil || result.IsError || result.StructuredContent == nil {
			t.Fatalf("format %s result=%#v error=%v", format, result, err)
		}
	}
	missing, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "render_governance_report", Arguments: map[string]any{
		"schema_version": contracts.SchemaVersion, "kind": "request", "audit_reference": "00000000-0000-4000-8000-000000000099", "format": "chat",
	}})
	if err != nil || !missing.IsError {
		t.Fatalf("missing result=%#v error=%v", missing, err)
	}
}

func TestRegisterAll_PreservesSevenFrozenToolsAndAddsProjectAudit(t *testing.T) {
	server := newTestServer(t)
	if _, err := RegisterAll(server, &fakeAuditStore{}); err != nil {
		t.Fatal(err)
	}
	ctx, session := connectTestClient(t, server)
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 8 {
		t.Fatalf("tool list=%#v error=%v", listed, err)
	}
	expected := []string{
		"improve_prompt", "create_goal_prompt", "create_review_fix_prompt", "lint_prompt",
		"get_checkpoint", "audit_session", "audit_project", "render_governance_report",
	}
	for _, name := range expected {
		if !hasTool(listed.Tools, name) {
			t.Fatalf("tool %s missing", name)
		}
	}
	for _, tool := range listed.Tools {
		if tool.Description == "" || tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Fatalf("incomplete tool description/schema: %#v", tool)
		}
	}
}
