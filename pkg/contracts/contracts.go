package contracts

import "fmt"

// SchemaVersion is the frozen v1 schema version.
const SchemaVersion = "1.0.0"

// ExecutionPolicy controls whether Prompt Better may recommend execution.
type ExecutionPolicy string

const (
	// ExecutionPolicyImproveOnly returns an improved prompt and stops.
	ExecutionPolicyImproveOnly ExecutionPolicy = "improve_only"
	// ExecutionPolicyAskBeforeExecute requires approval before execution.
	ExecutionPolicyAskBeforeExecute ExecutionPolicy = "ask_before_execute"
	// ExecutionPolicyFollowUserIntent preserves authorized execution intent.
	ExecutionPolicyFollowUserIntent ExecutionPolicy = "follow_user_intent"
)

// PolicyOutcome is the effective policy decision after host permission checks.
type PolicyOutcome string

const (
	// PolicyOutcomeReturnOnly returns a prompt without execution.
	PolicyOutcomeReturnOnly PolicyOutcome = "return_only"
	// PolicyOutcomeApprovalRequired requires explicit approval.
	PolicyOutcomeApprovalRequired PolicyOutcome = "approval_required"
	// PolicyOutcomeExecutionRecommended permits an in-scope host recommendation.
	PolicyOutcomeExecutionRecommended PolicyOutcome = "execution_recommended"
	// PolicyOutcomeDenied records a host permission denial.
	PolicyOutcomeDenied PolicyOutcome = "denied"
)

// PromptPlan is the deterministic intermediate representation for a prompt.
type PromptPlan struct {
	SchemaVersion        string   `json:"schema_version"`
	Role                 string   `json:"role,omitempty"`
	Personality          string   `json:"personality,omitempty"`
	CollaborationStyle   string   `json:"collaboration_style,omitempty"`
	Goal                 string   `json:"goal"`
	SuccessCriteria      []string `json:"success_criteria"`
	Invariants           []string `json:"invariants"`
	DecisionRules        []string `json:"decision_rules"`
	EvidenceRequirements []string `json:"evidence_requirements"`
	Tools                []string `json:"tools"`
	OutputContract       string   `json:"output_contract"`
	OutputLanguage       string   `json:"output_language,omitempty"`
	ApprovalBoundary     string   `json:"approval_boundary"`
	PhaseScope           string   `json:"phase_scope"`
	StopRules            []string `json:"stop_rules"`
	FallbackRules        []string `json:"fallback_rules"`
	AbstainRules         []string `json:"abstain_rules"`
	ValidationBar        []string `json:"validation_bar"`
	ArtifactPriorities   []string `json:"artifact_priorities,omitempty"`
	StablePrefix         *string  `json:"stable_prefix,omitempty"`
	DynamicTail          *string  `json:"dynamic_tail,omitempty"`
	Scope                []string `json:"scope,omitempty"`
	NonGoals             []string `json:"non_goals,omitempty"`
	Gates                []string `json:"gates,omitempty"`
}

// BoundaryDecision is one sanitized, provenance-backed policy decision.
type BoundaryDecision struct {
	Category     string   `json:"category"`
	Outcome      string   `json:"outcome"`
	Risk         int      `json:"risk"`
	RiskLevel    string   `json:"risk_level"`
	Confidence   float64  `json:"confidence"`
	SourceRef    string   `json:"source_ref"`
	PackID       string   `json:"pack_id"`
	RuleID       string   `json:"rule_id"`
	Version      string   `json:"version"`
	Explanation  string   `json:"explanation"`
	ConflictRefs []string `json:"conflict_refs"`
}

// ExecutionBudget describes advisory or host-enforced execution bounds.
type ExecutionBudget struct {
	SchemaVersion          string   `json:"schema_version"`
	Enforcement            string   `json:"enforcement"`
	MaxUsefulToolLoops     *int     `json:"max_useful_tool_loops,omitempty"`
	MaxRetries             *int     `json:"max_retries,omitempty"`
	MaxRetrievalExpansions *int     `json:"max_retrieval_expansions,omitempty"`
	ActivePhases           []string `json:"active_phases"`
	DelegationPolicy       string   `json:"delegation_policy"`
	MaxAgentDepth          *int     `json:"max_agent_depth,omitempty"`
	MaxConcurrency         *int     `json:"max_concurrency,omitempty"`
	ContextMode            string   `json:"context_mode,omitempty"`
	ExhaustionOutcome      string   `json:"exhaustion_outcome"`
}

// ImprovePromptRequest is the improve_prompt v1 request.
type ImprovePromptRequest struct {
	SchemaVersion   string           `json:"schema_version"`
	Kind            string           `json:"kind"`
	Intent          string           `json:"intent"`
	ExecutionPolicy ExecutionPolicy  `json:"execution_policy"`
	PromptPlan      *PromptPlan      `json:"prompt_plan,omitempty"`
	Budget          *ExecutionBudget `json:"budget,omitempty"`
}

// ImprovePromptResult is the improve_prompt v1 result.
type ImprovePromptResult struct {
	SchemaVersion     string             `json:"schema_version"`
	Kind              string             `json:"kind"`
	ImprovedPrompt    string             `json:"improved_prompt"`
	PolicyOutcome     PolicyOutcome      `json:"policy_outcome"`
	Diagnostics       []string           `json:"diagnostics"`
	BoundaryDecisions []BoundaryDecision `json:"boundary_decisions,omitempty"`
}

// CreateGoalPromptRequest is the create_goal_prompt v1 request.
type CreateGoalPromptRequest struct {
	SchemaVersion string     `json:"schema_version"`
	Kind          string     `json:"kind"`
	Objective     string     `json:"objective"`
	PromptPlan    PromptPlan `json:"prompt_plan"`
}

// CreateGoalPromptResult is the create_goal_prompt v1 result.
type CreateGoalPromptResult struct {
	SchemaVersion      string `json:"schema_version"`
	Kind               string `json:"kind"`
	GoalPrompt         string `json:"goal_prompt"`
	CheckpointRequired bool   `json:"checkpoint_required"`
}

// CreateReviewFixPromptRequest is the create_review_fix_prompt v1 request.
type CreateReviewFixPromptRequest struct {
	SchemaVersion string   `json:"schema_version"`
	Kind          string   `json:"kind"`
	Findings      []string `json:"findings"`
	ReviewHead    string   `json:"review_head"`
}

// CreateReviewFixPromptResult is the create_review_fix_prompt v1 result.
type CreateReviewFixPromptResult struct {
	SchemaVersion           string   `json:"schema_version"`
	Kind                    string   `json:"kind"`
	FixPrompt               string   `json:"fix_prompt"`
	FailureClasses          []string `json:"failure_classes"`
	SamePatternScanRequired bool     `json:"same_pattern_scan_required"`
}

// LintPromptRequest is the lint_prompt v1 request.
type LintPromptRequest struct {
	SchemaVersion string `json:"schema_version"`
	Kind          string `json:"kind"`
	Candidate     string `json:"candidate"`
}

// Diagnostic is one actionable prompt lint finding.
type Diagnostic struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Location    string `json:"location"`
	Rationale   string `json:"rationale"`
	Remediation string `json:"remediation"`
}

// LintPromptResult is the lint_prompt v1 result.
type LintPromptResult struct {
	SchemaVersion     string             `json:"schema_version"`
	Kind              string             `json:"kind"`
	Valid             bool               `json:"valid"`
	Diagnostics       []Diagnostic       `json:"diagnostics"`
	BoundaryDecisions []BoundaryDecision `json:"boundary_decisions,omitempty"`
}

// GetCheckpointRequest is the get_checkpoint v1 request.
type GetCheckpointRequest struct {
	SchemaVersion string `json:"schema_version"`
	Kind          string `json:"kind"`
	Reference     string `json:"reference"`
}

// GetCheckpointResult is the get_checkpoint v1 result.
type GetCheckpointResult struct {
	SchemaVersion     string   `json:"schema_version"`
	Kind              string   `json:"kind"`
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

// AuditConsent is the explicit local audit authorization state.
type AuditConsent string

const (
	AuditConsentGranted AuditConsent = "granted"
	AuditConsentDenied  AuditConsent = "denied"
	AuditConsentUnknown AuditConsent = "unknown"
)

// CoverageState is the bounded evidence coverage state.
type CoverageState string

const (
	CoverageStateComplete CoverageState = "complete"
	CoverageStatePartial  CoverageState = "partial"
	CoverageStateMissing  CoverageState = "missing"
	CoverageStateUnknown  CoverageState = "unknown"
)

// AuditSessionRequest is the audit_session v1 request.
type AuditSessionRequest struct {
	SchemaVersion     string       `json:"schema_version"`
	Kind              string       `json:"kind"`
	Reference         string       `json:"reference"`
	Consent           AuditConsent `json:"consent"`
	ConfiguredSources []string     `json:"configured_sources"`
}

// AuditSessionResult is the audit_session v1 result.
type AuditSessionResult struct {
	SchemaVersion    string        `json:"schema_version"`
	Kind             string        `json:"kind"`
	Coverage         CoverageState `json:"coverage"`
	EvidenceRefs     []string      `json:"evidence_refs"`
	Findings         []string      `json:"findings"`
	RedactionApplied bool          `json:"redaction_applied"`
}

// ReportFormat is a supported governance report rendering.
type ReportFormat string

const (
	ReportFormatChat     ReportFormat = "chat"
	ReportFormatMarkdown ReportFormat = "markdown"
	ReportFormatTable    ReportFormat = "table"
)

// ProvenanceLabel is a bounded report provenance classification.
type ProvenanceLabel string

const (
	ProvenanceOfficialCurrent        ProvenanceLabel = "official_current"
	ProvenanceStaffClarification     ProvenanceLabel = "staff_clarification"
	ProvenancePractitionerHypothesis ProvenanceLabel = "practitioner_hypothesis"
	ProvenanceRuntimeObserved        ProvenanceLabel = "runtime_observed"
	ProvenanceDerived                ProvenanceLabel = "derived"
	ProvenanceUnknown                ProvenanceLabel = "unknown"
)

// RenderGovernanceReportRequest is the render_governance_report v1 request.
type RenderGovernanceReportRequest struct {
	SchemaVersion  string       `json:"schema_version"`
	Kind           string       `json:"kind"`
	AuditReference string       `json:"audit_reference"`
	Format         ReportFormat `json:"format"`
}

// RenderGovernanceReportResult is the render_governance_report v1 result.
type RenderGovernanceReportResult struct {
	SchemaVersion    string            `json:"schema_version"`
	Kind             string            `json:"kind"`
	Format           ReportFormat      `json:"format"`
	Rendered         string            `json:"rendered"`
	Coverage         CoverageState     `json:"coverage"`
	ProvenanceLabels []ProvenanceLabel `json:"provenance_labels"`
}

// ErrorCode is a stable, sanitized v1 error classification.
type ErrorCode string

const (
	// ErrorCodeInvalidSchema identifies malformed request data.
	ErrorCodeInvalidSchema ErrorCode = "invalid_schema"
	// ErrorCodeSemanticInvalid identifies contradictory or unsafe semantics.
	ErrorCodeSemanticInvalid ErrorCode = "semantic_invalid"
	// ErrorCodePermissionDenied identifies host permission denial.
	ErrorCodePermissionDenied ErrorCode = "permission_denied"
	// ErrorCodeApprovalRequired identifies a required explicit approval.
	ErrorCodeApprovalRequired ErrorCode = "approval_required"
	// ErrorCodeUnsupportedCapability identifies a known unsupported capability.
	ErrorCodeUnsupportedCapability ErrorCode = "unsupported_capability"
	// ErrorCodeUnknownCapability identifies an unknown capability state.
	ErrorCodeUnknownCapability ErrorCode = "unknown_capability"
	// ErrorCodeCoverageIncomplete identifies incomplete required evidence.
	ErrorCodeCoverageIncomplete ErrorCode = "coverage_incomplete"
	// ErrorCodeSourceConflict identifies conflicting evidence sources.
	ErrorCodeSourceConflict ErrorCode = "source_conflict"
	// ErrorCodeBudgetExhausted identifies cancellation or exhausted bounds.
	ErrorCodeBudgetExhausted ErrorCode = "budget_exhausted"
	// ErrorCodeSensitivePayload identifies disallowed sensitive content.
	ErrorCodeSensitivePayload ErrorCode = "sensitive_payload"
	// ErrorCodeNotFound identifies unavailable explicit local input.
	ErrorCodeNotFound ErrorCode = "not_found"
	// ErrorCodeInternal identifies a sanitized internal failure.
	ErrorCodeInternal ErrorCode = "internal_error"
)

// StableError is the v1 sanitized error envelope.
type StableError struct {
	SchemaVersion string    `json:"schema_version"`
	Code          ErrorCode `json:"code"`
	Message       string    `json:"message"`
	FieldPath     *string   `json:"field_path,omitempty"`
	Retryable     bool      `json:"retryable"`
	EvidenceRefs  []string  `json:"evidence_refs,omitempty"`
}

func (e *StableError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// NewError constructs a stable error without raw input content.
func NewError(code ErrorCode, message, field string, retryable bool) *StableError {
	e := &StableError{SchemaVersion: SchemaVersion, Code: code, Message: message, Retryable: retryable}
	if field != "" {
		e.FieldPath = &field
	}
	return e
}
