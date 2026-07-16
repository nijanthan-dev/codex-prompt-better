// Package tools adapts frozen v1 contracts to deterministic core/store behavior.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nijanthan-dev/codex-prompt-better/internal/audit"
	"github.com/nijanthan-dev/codex-prompt-better/internal/checkpoint"
	"github.com/nijanthan-dev/codex-prompt-better/internal/compiler"
	promptlint "github.com/nijanthan-dev/codex-prompt-better/internal/lint"
	"github.com/nijanthan-dev/codex-prompt-better/internal/mcpserver"
	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
	contractschemas "github.com/nijanthan-dev/codex-prompt-better/schemas"
)

const maxResultBytes = 50_000

const (
	maxCheckpointItems = 50
	maxCheckpointItem  = 500
)

// Configuration bounds runtime behavior to the explicit integration config.
type Configuration struct {
	ExecutionPolicy          contracts.ExecutionPolicy
	SourceKinds              []string
	ProcessPurposeConfigured bool
}

type sessionState struct {
	checkpoint *contracts.GetCheckpointResult
	auditRef   string
	audit      *contracts.AuditSessionResult
	provenance []contracts.ProvenanceLabel
}

type AuditStore interface {
	AuditSession(context.Context, string, []string, *time.Time) (contracts.AuditSessionResult, []contracts.ProvenanceLabel, error)
}

type ProjectAuditStore interface {
	AuditProject(context.Context, contracts.AuditProjectRequest) (contracts.AuditProjectResult, error)
}

type Service struct {
	server          *mcpserver.Server
	audits          AuditStore
	projectAudits   ProjectAuditStore
	executionPolicy contracts.ExecutionPolicy
	allowedSources  map[string]bool
	processEnabled  bool
	mu              sync.Mutex
	state           map[string]*sessionState
}

func RegisterAll(server *mcpserver.Server, audits AuditStore) (*Service, error) {
	return RegisterWithConfig(server, audits, Configuration{ExecutionPolicy: contracts.ExecutionPolicyFollowUserIntent})
}

// RegisterWithConfig registers all tools with explicit policy and audit bounds.
func RegisterWithConfig(server *mcpserver.Server, audits AuditStore, config Configuration) (*Service, error) {
	if server == nil {
		return nil, errors.New("MCP server required")
	}
	if !policy.ValidExecutionPolicy(config.ExecutionPolicy) {
		return nil, errors.New("execution policy required")
	}
	var allowed map[string]bool
	if config.SourceKinds != nil {
		allowed = make(map[string]bool, len(config.SourceKinds))
		for _, source := range config.SourceKinds {
			allowed[source] = true
		}
	}
	service := &Service{
		server: server, audits: audits, executionPolicy: config.ExecutionPolicy,
		allowedSources: allowed, processEnabled: config.ProcessPurposeConfigured, state: map[string]*sessionState{},
	}
	if projectAudits, ok := audits.(ProjectAuditStore); ok {
		service.projectAudits = projectAudits
	}
	if err := register(service, "improve_prompt", improveDescription, service.improvePrompt); err != nil {
		return nil, err
	}
	if err := register(service, "create_goal_prompt", goalDescription, service.createGoalPrompt); err != nil {
		return nil, err
	}
	if err := register(service, "create_review_fix_prompt", reviewDescription, service.createReviewFixPrompt); err != nil {
		return nil, err
	}
	if err := register(service, "lint_prompt", lintDescription, service.lintPrompt); err != nil {
		return nil, err
	}
	if err := register(service, "get_checkpoint", checkpointDescription, service.getCheckpoint); err != nil {
		return nil, err
	}
	if err := register(service, "audit_session", auditDescription, service.auditSession); err != nil {
		return nil, err
	}
	if err := register(service, "audit_project", projectAuditDescription, service.auditProject); err != nil {
		return nil, err
	}
	if err := register(service, "render_governance_report", reportDescription, service.renderGovernanceReport); err != nil {
		return nil, err
	}
	return service, nil
}

type handler[Input, Output any] func(context.Context, string, Input) (Output, error)

func register[Input, Output any](service *Service, name, description string, handle handler[Input, Output]) error {
	inputSchema, err := contractschemas.ToolDefinition(name, "request")
	if err != nil {
		return err
	}
	outputSchema, err := contractschemas.ToolDefinition(name, "result")
	if err != nil {
		return err
	}
	service.server.SDK().AddTool(&mcp.Tool{
		Name: name, Description: description, InputSchema: inputSchema, OutputSchema: outputSchema,
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		bounded, release, err := service.server.Acquire(ctx)
		if err != nil {
			return errorResult(mapError(err)), nil
		}
		defer release()
		var input Input
		if err := decodeStrict(request.Params.Arguments, &input); err != nil {
			return errorResult(contracts.NewError(contracts.ErrorCodeInvalidSchema, "request does not match the frozen schema", "request", false)), nil
		}
		output, err := handle(bounded, sessionKey(request.Session), input)
		if contextErr := bounded.Err(); contextErr != nil {
			return errorResult(mapError(contextErr)), nil
		}
		if err != nil {
			return errorResult(mapError(err)), nil
		}
		return successResult(output), nil
	})
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func successResult(output any) *mcp.CallToolResult {
	data, err := json.Marshal(output)
	if err != nil || len(data) > maxResultBytes {
		return errorResult(contracts.NewError(
			contracts.ErrorCodeBudgetExhausted,
			"tool result exceeded its bound",
			"result",
			false,
		))
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(data)}},
		StructuredContent: output,
	}
}

func errorResult(stable *contracts.StableError) *mcp.CallToolResult {
	data, _ := json.Marshal(stable)
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

func mapError(err error) *contracts.StableError {
	var stable *contracts.StableError
	if errors.As(err, &stable) {
		return stable
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return contracts.NewError(contracts.ErrorCodeBudgetExhausted, "tool execution stopped at its bound", "request", true)
	case errors.Is(err, mcpserver.ErrBusy):
		return contracts.NewError(contracts.ErrorCodeBudgetExhausted, "tool concurrency bound reached", "request", true)
	default:
		return contracts.NewError(contracts.ErrorCodeInternal, "tool execution failed", "request", false)
	}
}

func sessionKey(session *mcp.ServerSession) string {
	if session == nil {
		return "stdio"
	}
	return fmt.Sprintf("%p", session)
}

func (service *Service) saveCheckpoint(session string, value contracts.GetCheckpointResult) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state := service.sessionStateLocked(session)
	state.checkpoint = &value
}

func (service *Service) sessionStateLocked(session string) *sessionState {
	service.pruneDisconnectedLocked()
	if state := service.state[session]; state != nil {
		return state
	}
	if len(service.state) >= 8 {
		service.state = map[string]*sessionState{}
	}
	state := &sessionState{}
	service.state[session] = state
	return state
}

func (service *Service) pruneDisconnectedLocked() {
	active := map[string]bool{}
	for session := range service.server.SDK().Sessions() {
		active[sessionKey(session)] = true
	}
	if len(active) == 0 {
		return
	}
	for key := range service.state {
		if !active[key] {
			delete(service.state, key)
		}
	}
}

func (service *Service) improvePrompt(ctx context.Context, session string, request contracts.ImprovePromptRequest) (contracts.ImprovePromptResult, error) {
	if executionPolicyRank(request.ExecutionPolicy) > executionPolicyRank(service.executionPolicy) {
		return contracts.ImprovePromptResult{}, contracts.NewError(
			contracts.ErrorCodePermissionDenied, "requested execution policy exceeds configured policy", "execution_policy", false,
		)
	}
	result, err := compiler.Improve(ctx, request, policy.HostUnknown)
	if err != nil {
		return contracts.ImprovePromptResult{}, err
	}
	plan := request.PromptPlan
	if plan == nil {
		generated := compiler.NewPlan(request.Intent)
		plan = &generated
	}
	service.saveCheckpoint(session, checkpointForImprove(request, result, *plan))
	return result, nil
}

func (service *Service) createGoalPrompt(ctx context.Context, session string, request contracts.CreateGoalPromptRequest) (contracts.CreateGoalPromptResult, error) {
	result, err := compiler.CreateGoal(ctx, request)
	if err != nil {
		return contracts.CreateGoalPromptResult{}, err
	}
	service.saveCheckpoint(session, contracts.GetCheckpointResult{
		SchemaVersion: contracts.SchemaVersion, Kind: "result", Objective: bounded(request.Objective, 1000),
		AcceptedDecisions: []string{"House goal format selected."},
		Constraints:       boundedCheckpointList(request.PromptPlan.Invariants), EvidenceRefs: []string{},
		Completed: []string{"Goal prompt compiled."}, Validation: []string{"Frozen v1 goal contract passed."},
		Blockers: []string{}, NextAction: "Start the goal only when the requested boundary permits it.",
		RemainingGates: boundedCheckpointList(request.PromptPlan.Gates),
	})
	return result, nil
}

func (service *Service) createReviewFixPrompt(ctx context.Context, session string, request contracts.CreateReviewFixPromptRequest) (contracts.CreateReviewFixPromptResult, error) {
	result, err := compiler.CreateReviewFix(ctx, request)
	if err != nil {
		return contracts.CreateReviewFixPromptResult{}, err
	}
	service.saveCheckpoint(session, contracts.GetCheckpointResult{
		SchemaVersion: contracts.SchemaVersion, Kind: "result",
		Objective:         "Fix review findings for head " + request.ReviewHead + ".",
		AcceptedDecisions: boundedCheckpointList(result.FailureClasses),
		Constraints:       []string{"Scan directly analogous paths for every failure class."}, EvidenceRefs: []string{},
		Completed: []string{"Review-fix prompt compiled."}, Validation: []string{"Review head and bounded findings passed validation."},
		Blockers: []string{}, NextAction: "Apply and validate the review fixes at the exact reviewed head.",
		RemainingGates: []string{"Focused regression proof", "Same-pattern scan", "Latest-head validation"},
	})
	return result, nil
}

func (service *Service) lintPrompt(_ context.Context, session string, request contracts.LintPromptRequest) (contracts.LintPromptResult, error) {
	result, err := promptlint.CheckPrompt(request)
	if err != nil {
		return contracts.LintPromptResult{}, err
	}
	validation := make([]string, 0, len(result.Diagnostics)+1)
	if len(result.Diagnostics) == 0 {
		validation = append(validation, "No prompt lint findings.")
	}
	for _, diagnostic := range result.Diagnostics {
		validation = append(validation, diagnostic.Severity+":"+diagnostic.Code)
		if len(validation) == 50 {
			break
		}
	}
	service.saveCheckpoint(session, contracts.GetCheckpointResult{
		SchemaVersion: contracts.SchemaVersion, Kind: "result", Objective: "Review a prompt candidate.",
		AcceptedDecisions: []string{fmt.Sprintf("Prompt validity: %t.", result.Valid)},
		Constraints:       []string{"Preserve authorization, evidence, validation, and stop boundaries."}, EvidenceRefs: []string{},
		Completed: []string{"Prompt lint completed."}, Validation: validation, Blockers: []string{},
		NextAction: "Address material diagnostics before using the prompt.", RemainingGates: []string{},
	})
	return result, nil
}

func (service *Service) getCheckpoint(_ context.Context, session string, request contracts.GetCheckpointRequest) (contracts.GetCheckpointResult, error) {
	if request.SchemaVersion != contracts.SchemaVersion || request.Kind != "request" || len(request.Reference) < 1 || len(request.Reference) > 128 {
		return contracts.GetCheckpointResult{}, contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid checkpoint request", "request", false)
	}
	if request.Reference != "latest" {
		return contracts.GetCheckpointResult{}, contracts.NewError(contracts.ErrorCodeNotFound, "checkpoint reference unavailable", "reference", false)
	}
	service.mu.Lock()
	service.pruneDisconnectedLocked()
	state := service.state[session]
	var result *contracts.GetCheckpointResult
	if state != nil && state.checkpoint != nil {
		copy := *state.checkpoint
		result = &copy
	}
	service.mu.Unlock()
	if result == nil {
		return contracts.GetCheckpointResult{}, contracts.NewError(contracts.ErrorCodeNotFound, "checkpoint reference unavailable", "reference", false)
	}
	if _, err := checkpoint.Render(checkpoint.Checkpoint{
		Objective: result.Objective, AcceptedDecisions: result.AcceptedDecisions,
		Constraints: result.Constraints, EvidenceRefs: result.EvidenceRefs, Completed: result.Completed,
		Validation: result.Validation, Blockers: result.Blockers, NextAction: result.NextAction,
		RemainingGates: result.RemainingGates,
	}); err != nil {
		return contracts.GetCheckpointResult{}, err
	}
	return *result, nil
}

func (service *Service) auditSession(ctx context.Context, session string, request contracts.AuditSessionRequest) (contracts.AuditSessionResult, error) {
	if request.SchemaVersion != contracts.SchemaVersion || request.Kind != "request" || len(request.Reference) < 1 || len(request.Reference) > 128 {
		return contracts.AuditSessionResult{}, contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid audit request", "request", false)
	}
	if request.Consent == contracts.AuditConsentDenied {
		return contracts.AuditSessionResult{}, contracts.NewError(contracts.ErrorCodePermissionDenied, "audit consent denied", "consent", false)
	}
	if request.Consent != contracts.AuditConsentGranted {
		return contracts.AuditSessionResult{}, contracts.NewError(contracts.ErrorCodeApprovalRequired, "explicit audit consent required", "consent", false)
	}
	if err := validateSources(request.ConfiguredSources); err != nil {
		return contracts.AuditSessionResult{}, err
	}
	if service.allowedSources != nil {
		for _, source := range request.ConfiguredSources {
			if !service.allowedSources[source] {
				return contracts.AuditSessionResult{}, contracts.NewError(contracts.ErrorCodePermissionDenied, "audit source is not configured", "configured_sources", false)
			}
			if source == "process" && !service.processEnabled {
				return contracts.AuditSessionResult{}, contracts.NewError(contracts.ErrorCodePermissionDenied, "process audit purpose is not configured", "configured_sources", false)
			}
		}
	}
	sessionID, asOf, err := parseAuditReference(request.Reference)
	if err != nil {
		return contracts.AuditSessionResult{}, err
	}
	if service.audits == nil {
		return contracts.AuditSessionResult{}, contracts.NewError(contracts.ErrorCodeCoverageIncomplete, "audit store unavailable", "reference", true)
	}
	result, provenance, err := service.audits.AuditSession(ctx, sessionID, request.ConfiguredSources, asOf)
	if err != nil {
		return contracts.AuditSessionResult{}, err
	}
	service.mu.Lock()
	state := service.sessionStateLocked(session)
	copy := result
	state.auditRef = request.Reference
	state.audit = &copy
	state.provenance = append([]contracts.ProvenanceLabel{}, provenance...)
	service.mu.Unlock()
	return result, nil
}

func (service *Service) auditProject(ctx context.Context, _ string, request contracts.AuditProjectRequest) (contracts.AuditProjectResult, error) {
	if err := audit.ValidateRequest(request); err != nil {
		return contracts.AuditProjectResult{}, err
	}
	if service.allowedSources != nil {
		for _, source := range request.ConfiguredSources {
			if !service.allowedSources[source] {
				return contracts.AuditProjectResult{}, contracts.NewError(
					contracts.ErrorCodePermissionDenied, "audit source is not configured",
					"configured_sources", false)
			}
			if source == "process" && !service.processEnabled {
				return contracts.AuditProjectResult{}, contracts.NewError(
					contracts.ErrorCodePermissionDenied, "process audit purpose is not configured",
					"configured_sources", false)
			}
		}
	}
	if service.projectAudits == nil {
		return contracts.AuditProjectResult{}, contracts.NewError(
			contracts.ErrorCodeCoverageIncomplete,
			"project audit store unavailable",
			"reference",
			true,
		)
	}
	return service.projectAudits.AuditProject(ctx, request)
}

func validateSources(sources []string) error {
	if len(sources) == 0 || len(sources) > 20 {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "configured_sources must contain 1 to 20 items", "configured_sources", false)
	}
	seen := map[string]bool{}
	for _, source := range sources {
		if len(source) == 0 || len(source) > 80 || seen[source] {
			return contracts.NewError(contracts.ErrorCodeInvalidSchema, "configured_sources contains an invalid item", "configured_sources", false)
		}
		seen[source] = true
	}
	return nil
}

var sessionReference = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[1-5][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

func parseAuditReference(reference string) (string, *time.Time, error) {
	if strings.HasPrefix(reference, "current:") {
		reference = strings.TrimPrefix(reference, "current:")
	}
	if strings.HasPrefix(reference, "as-of:") {
		value := strings.TrimPrefix(reference, "as-of:")
		separator := strings.LastIndex(value, "@")
		if separator < 1 {
			return "", nil, contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid as-of audit reference", "reference", false)
		}
		at, err := time.Parse(time.RFC3339Nano, value[:separator])
		if err != nil || !sessionReference.MatchString(value[separator+1:]) {
			return "", nil, contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid as-of audit reference", "reference", false)
		}
		return value[separator+1:], &at, nil
	}
	if !sessionReference.MatchString(reference) {
		return "", nil, contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid audit reference", "reference", false)
	}
	return reference, nil, nil
}

func (service *Service) renderGovernanceReport(_ context.Context, session string, request contracts.RenderGovernanceReportRequest) (contracts.RenderGovernanceReportResult, error) {
	if request.SchemaVersion != contracts.SchemaVersion || request.Kind != "request" || len(request.AuditReference) < 1 || len(request.AuditReference) > 128 {
		return contracts.RenderGovernanceReportResult{}, contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid governance report request", "request", false)
	}
	if request.Format != contracts.ReportFormatChat && request.Format != contracts.ReportFormatMarkdown && request.Format != contracts.ReportFormatTable {
		return contracts.RenderGovernanceReportResult{}, contracts.NewError(contracts.ErrorCodeInvalidSchema, "unsupported report format", "format", false)
	}
	service.mu.Lock()
	service.pruneDisconnectedLocked()
	state := service.state[session]
	var audit *contracts.AuditSessionResult
	var provenance []contracts.ProvenanceLabel
	if state != nil && state.audit != nil && state.auditRef == request.AuditReference {
		copy := *state.audit
		audit = &copy
		provenance = append([]contracts.ProvenanceLabel{}, state.provenance...)
	}
	service.mu.Unlock()
	if audit == nil {
		return contracts.RenderGovernanceReportResult{}, contracts.NewError(contracts.ErrorCodeNotFound, "audit reference unavailable in this MCP session", "audit_reference", false)
	}
	if len(provenance) == 0 {
		provenance = []contracts.ProvenanceLabel{contracts.ProvenanceUnknown}
	}
	rendered := renderAudit(*audit, request.Format)
	if len(rendered) == 0 || len(rendered) > maxResultBytes {
		return contracts.RenderGovernanceReportResult{}, contracts.NewError(contracts.ErrorCodeInternal, "governance report rendering failed", "audit_reference", false)
	}
	return contracts.RenderGovernanceReportResult{
		SchemaVersion: contracts.SchemaVersion, Kind: "result", Format: request.Format,
		Rendered: rendered, Coverage: audit.Coverage, ProvenanceLabels: provenance,
	}, nil
}

func renderAudit(audit contracts.AuditSessionResult, format contracts.ReportFormat) string {
	var output strings.Builder
	switch format {
	case contracts.ReportFormatChat:
		fmt.Fprintf(&output, "Coverage: %s. Evidence references: %d.", audit.Coverage, len(audit.EvidenceRefs))
		for _, finding := range audit.Findings {
			fmt.Fprintf(&output, " %s.", finding)
		}
	case contracts.ReportFormatMarkdown:
		fmt.Fprintf(&output, "## PromptBetter governance report\n\n- Coverage: `%s`\n- Evidence references: %d\n- Redaction applied: %t\n", audit.Coverage, len(audit.EvidenceRefs), audit.RedactionApplied)
		if len(audit.Findings) > 0 {
			output.WriteString("- Findings:\n")
			for _, finding := range audit.Findings {
				fmt.Fprintf(&output, "  - %s\n", finding)
			}
		}
	case contracts.ReportFormatTable:
		output.WriteString("field | value\n--- | ---\n")
		fmt.Fprintf(&output, "coverage | %s\nevidence_refs | %d\nredaction_applied | %t\n", audit.Coverage, len(audit.EvidenceRefs), audit.RedactionApplied)
		for index, finding := range audit.Findings {
			fmt.Fprintf(&output, "finding_%d | %s\n", index+1, finding)
		}
	}
	return strings.TrimSpace(output.String())
}

func checkpointForImprove(request contracts.ImprovePromptRequest, result contracts.ImprovePromptResult, plan contracts.PromptPlan) contracts.GetCheckpointResult {
	validation := append([]string{}, result.Diagnostics...)
	if len(validation) == 0 {
		validation = []string{"Frozen v1 request and policy validation passed."}
	}
	validation = boundedCheckpointList(validation)
	return contracts.GetCheckpointResult{
		SchemaVersion: contracts.SchemaVersion, Kind: "result", Objective: bounded(request.Intent, 1000),
		AcceptedDecisions: []string{fmt.Sprintf("Execution policy outcome: %s.", result.PolicyOutcome)},
		Constraints:       boundedCheckpointList(plan.Invariants), EvidenceRefs: []string{},
		Completed: []string{"Improved prompt compiled."}, Validation: validation, Blockers: []string{},
		NextAction: "Use the improved prompt at the authorized boundary.", RemainingGates: boundedCheckpointList(plan.Gates),
	}
}

func executionPolicyRank(value contracts.ExecutionPolicy) int {
	switch value {
	case contracts.ExecutionPolicyImproveOnly:
		return 1
	case contracts.ExecutionPolicyAskBeforeExecute:
		return 2
	case contracts.ExecutionPolicyFollowUserIntent:
		return 3
	default:
		return 0
	}
}

func boundedCheckpointList(values []string) []string {
	if len(values) > maxCheckpointItems {
		values = values[:maxCheckpointItems]
	}
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = bounded(value, maxCheckpointItem)
	}
	return result
}

func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit]
}

const improveDescription = "Deterministically compiles a bounded task intent into an improved prompt. Use when the user wants prompt refinement without automatic execution. Input is the frozen v1 improve_prompt request; output includes improved_prompt, policy_outcome, diagnostics, and boundary_decisions. Intent is limited to 8000 bytes and output to 16000 bytes. Invalid, unsafe, canceled, timed-out, or overloaded calls return a sanitized stable error."

const goalDescription = "Creates a bounded house-format goal prompt from an explicit objective and prompt plan. Use when the user asks for a new goal artifact; it does not start or execute the goal. Input is the frozen v1 create_goal_prompt request; output includes goal_prompt and checkpoint_required. Objective is limited to 4000 bytes and output to 16000 bytes. Invalid, canceled, timed-out, or overloaded calls return a sanitized stable error."

const reviewDescription = "Creates a bounded remediation prompt for findings tied to an exact commit head. Use after review findings exist and same-pattern scanning is required; it does not modify code or resolve threads. Input is the frozen v1 create_review_fix_prompt request; output includes fix_prompt, failure_classes, and same_pattern_scan_required. Accepts 1 to 100 findings and a 7 to 64 character lowercase commit hash. Invalid, canceled, timed-out, or overloaded calls return a sanitized stable error."

const lintDescription = "Deterministically lints one bounded prompt candidate for actionable contract and safety gaps. Use before relying on a prompt or after editing it; it does not rewrite or execute the prompt. Input is the frozen v1 lint_prompt request; output includes valid, diagnostics, and boundary_decisions. Candidate is limited to 16000 bytes and diagnostics to 100 items. Invalid, canceled, timed-out, or overloaded calls return a sanitized stable error."

const checkpointDescription = "Returns the latest bounded checkpoint created by a compiler or lint tool in this MCP session. Use for user-controlled continuation after a major milestone; it never retrieves another client or a persisted raw prompt. Input is the frozen v1 get_checkpoint request with reference exactly latest; output includes objective, decisions, constraints, evidence, completed work, validation, blockers, next action, and remaining gates. Lists are capped by the schema. Missing, invalid, canceled, timed-out, or overloaded calls return a sanitized stable error."

const auditDescription = "Reads bounded normalized evidence for one explicitly referenced local session. Use only after explicit consent and with the configured source-kind allowlist; it never starts collection or reads raw prompts. Input is the frozen v1 audit_session request; references are a session UUID, current:UUID, or as-of:RFC3339@UUID. Output includes coverage, safe evidence_refs, bounded findings, and redaction_applied. Missing dimensions, denied consent, unavailable sources, cancellation, timeout, and overload return sanitized stable errors."

const projectAuditDescription = "Audits one explicitly consented local portfolio, project, task, or trajectory window from normalized evidence. It never starts collection, executes recommendations, or changes host state. Output is bounded, versioned, redacted, and preserves unknown coverage."

const reportDescription = "Renders the audit cached under the exact same reference in this MCP session as chat, Markdown, or a compact table. Use after audit_session when the user wants a bounded human-readable coverage report; it does not calculate #8 metrics or persist a #9 artifact. Input is the frozen v1 render_governance_report request; output includes format, rendered, coverage, and provenance_labels. Rendered output is capped at 50000 bytes. Missing audit state, invalid format, cancellation, timeout, and overload return sanitized stable errors."
