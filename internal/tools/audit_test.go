package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

type fakeAuditStore struct {
	sessionID string
	sources   []string
	asOf      *time.Time
}

func TestAuditSession_RejectsSourceOutsideServerAllowlist(t *testing.T) {
	server := newTestServer(t)
	service, err := RegisterConfigured(server, &fakeAuditStore{}, []string{"git"})
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

func TestRegisterAll_AdvertisesSevenFrozenTools(t *testing.T) {
	server := newTestServer(t)
	if _, err := RegisterAll(server, &fakeAuditStore{}); err != nil {
		t.Fatal(err)
	}
	ctx, session := connectTestClient(t, server)
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 7 {
		t.Fatalf("tool list=%#v error=%v", listed, err)
	}
	for _, name := range []string{
		"improve_prompt", "create_goal_prompt", "create_review_fix_prompt", "lint_prompt",
		"get_checkpoint", "audit_session", "render_governance_report",
	} {
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
