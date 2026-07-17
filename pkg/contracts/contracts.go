package contracts

import (
	"fmt"
	"time"
)

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

// AuditScope is a bounded governance aggregation level.
type AuditScope string

const (
	AuditScopePortfolio  AuditScope = "portfolio"
	AuditScopeProject    AuditScope = "project"
	AuditScopeTask       AuditScope = "task"
	AuditScopeTrajectory AuditScope = "trajectory"
)

// MetricStatus is a coverage- and guardrail-aware comparison outcome.
type MetricStatus string

const (
	MetricStatusImproved     MetricStatus = "improved"
	MetricStatusWorsened     MetricStatus = "worsened"
	MetricStatusFlat         MetricStatus = "flat"
	MetricStatusMixed        MetricStatus = "mixed"
	MetricStatusInsufficient MetricStatus = "insufficient"
)

// AuditProjectRequest is the additive project governance request.
type AuditProjectRequest struct {
	SchemaVersion     string       `json:"schema_version"`
	Kind              string       `json:"kind"`
	Scope             AuditScope   `json:"scope"`
	Reference         string       `json:"reference"`
	ConfiguredSources []string     `json:"configured_sources"`
	StartsAt          time.Time    `json:"starts_at"`
	EndsAt            time.Time    `json:"ends_at"`
	AsOf              time.Time    `json:"as_of"`
	Consent           AuditConsent `json:"consent"`
}

// MetricResult is one transparent native-unit governance calculation.
type MetricResult struct {
	Name                     string        `json:"name"`
	Version                  string        `json:"version"`
	NativeValue              *float64      `json:"native_value"`
	NativeUnit               string        `json:"native_unit"`
	Numerator                *float64      `json:"numerator"`
	Denominator              *float64      `json:"denominator"`
	SampleCount              int           `json:"sample_count"`
	PreviousValue            *float64      `json:"previous_value"`
	RollingMedian            *float64      `json:"rolling_median"`
	RollingMAD               *float64      `json:"rolling_mad"`
	BaselineSampleCount      int           `json:"baseline_sample_count"`
	AbsoluteChangePrevious   *float64      `json:"absolute_change_previous"`
	PercentageChangePrevious *float64      `json:"percentage_change_previous"`
	AbsoluteChangeRolling    *float64      `json:"absolute_change_rolling"`
	PercentageChangeRolling  *float64      `json:"percentage_change_rolling"`
	WorkloadAdjustedResidual *float64      `json:"workload_adjusted_residual"`
	PracticalThreshold       float64       `json:"practical_threshold"`
	Coverage                 CoverageState `json:"coverage"`
	Status                   MetricStatus  `json:"status"`
	StatusReason             string        `json:"status_reason"`
	Confidence               string        `json:"confidence"`
	Uncertainty              string        `json:"uncertainty"`
	Exclusions               []string      `json:"exclusions"`
	EvidenceRefs             []string      `json:"evidence_refs"`
}

// AuditFinding is an evidence-backed, non-causal governance observation.
type AuditFinding struct {
	Code            string   `json:"code"`
	Detector        string   `json:"detector"`
	DetectorVersion string   `json:"detector_version"`
	Cause           string   `json:"cause"`
	ExceptionCheck  string   `json:"exception_check"`
	Classification  string   `json:"classification"`
	Confidence      string   `json:"confidence"`
	EvidenceRefs    []string `json:"evidence_refs"`
	Counterevidence []string `json:"counterevidence"`
}

// AuditRecommendation is preview-only guidance with an explicit verification.
type AuditRecommendation struct {
	Code                string   `json:"code"`
	PolicyVersion       string   `json:"policy_version"`
	LifecycleState      string   `json:"lifecycle_state"`
	TargetSurface       string   `json:"target_surface"`
	Action              string   `json:"action"`
	ExpectedMovement    string   `json:"expected_movement"`
	ProtectedGuardrails []string `json:"protected_guardrails"`
	ApprovalRequired    bool     `json:"approval_required"`
	Verification        string   `json:"verification"`
	Risks               []string `json:"risks"`
	EvidenceRefs        []string `json:"evidence_refs"`
}

type AuditWindowProvenance struct {
	StartsAt        time.Time `json:"starts_at"`
	EndsAt          time.Time `json:"ends_at"`
	AsOf            time.Time `json:"as_of"`
	Timezone        string    `json:"timezone"`
	BaselineVersion string    `json:"baseline_version"`
	CountingMode    string    `json:"counting_mode"`
	WorkloadVersion string    `json:"workload_version"`
	SourceVersions  []string  `json:"source_versions"`
	Revision        int       `json:"revision"`
	LateEvidence    bool      `json:"late_evidence"`
}

type ScopeContribution struct {
	Scope            AuditScope    `json:"scope"`
	Reference        string        `json:"reference"`
	WorkloadClass    string        `json:"workload_class"`
	Numerator        float64       `json:"numerator"`
	Denominator      float64       `json:"denominator"`
	Contribution     *float64      `json:"contribution"`
	Coverage         CoverageState `json:"coverage"`
	AttributionState string        `json:"attribution_state"`
}

type WorkloadDecomposition struct {
	Metric            string   `json:"metric"`
	VolumeEffect      *float64 `json:"volume_effect"`
	MixEffect         *float64 `json:"mix_effect"`
	WithinClassEffect *float64 `json:"within_class_effect"`
	Residual          *float64 `json:"residual"`
	Status            string   `json:"status"`
	Classes           []string `json:"classes"`
}

type InvocationCounts struct {
	HostCalls    int `json:"host_calls"`
	LeafCalls    int `json:"leaf_calls"`
	HostResults  int `json:"host_results"`
	LeafResults  int `json:"leaf_results"`
	UnknownCalls int `json:"unknown_calls"`
}

type NativeOutlier struct {
	Metric      string   `json:"metric"`
	NativeValue *float64 `json:"native_value"`
	NativeUnit  string   `json:"native_unit"`
	Direction   string   `json:"direction"`
	Magnitude   *float64 `json:"magnitude"`
}

type ConfounderStratum struct {
	Kind       string `json:"kind"`
	State      string `json:"state"`
	Matched    bool   `json:"matched"`
	Confidence string `json:"confidence"`
	Provenance string `json:"provenance"`
}

type GuardrailResult struct {
	Name         string        `json:"name"`
	State        string        `json:"state"`
	Coverage     CoverageState `json:"coverage"`
	EvidenceRefs []string      `json:"evidence_refs"`
}

// GovernanceReportFacts is the additive, normalized handoff consumed by #9.
// It contains no raw evidence or renderer-specific presentation.
type GovernanceReportFacts struct {
	Version           string                      `json:"version"`
	DisplayIdentity   string                      `json:"display_identity"`
	PrivacySuppressed bool                        `json:"privacy_suppressed"`
	PreviousWindow    *ReportWindowReference      `json:"previous_window"`
	RollingWindows    []ReportWindowReference     `json:"rolling_windows"`
	ScopeCounts       ReportScopeCounts           `json:"scope_counts"`
	Sources           []ReportSourceFacts         `json:"sources"`
	Metrics           []ReportMetricFacts         `json:"metrics"`
	QualityGateState  string                      `json:"quality_gate_state"`
	Outliers          []ReportOutlierFacts        `json:"outliers"`
	Recommendations   []ReportRecommendationFacts `json:"recommendations"`
	Provenance        []ReportProvenanceFact      `json:"provenance"`
}

type ReportWindowReference struct {
	StartsAt        time.Time     `json:"starts_at"`
	EndsAt          time.Time     `json:"ends_at"`
	AsOf            time.Time     `json:"as_of"`
	Coverage        CoverageState `json:"coverage"`
	BaselineVersion string        `json:"baseline_version"`
	SourceVersions  []string      `json:"source_versions"`
}

type ReportScopeCounts struct {
	IncludedTrajectories int  `json:"included_trajectories"`
	ExcludedTrajectories *int `json:"excluded_trajectories"`
	IncludedTurns        int  `json:"included_turns"`
	ExcludedTurns        *int `json:"excluded_turns"`
	IncludedToolCalls    int  `json:"included_tool_calls"`
	ExcludedToolCalls    *int `json:"excluded_tool_calls"`
	IncludedEvidence     int  `json:"included_evidence"`
	ExcludedEvidence     *int `json:"excluded_evidence"`
}

type ReportSourceFacts struct {
	SourceKind     string        `json:"source_kind"`
	Version        string        `json:"version"`
	Freshness      string        `json:"freshness"`
	Coverage       CoverageState `json:"coverage"`
	RedactionState string        `json:"redaction_state"`
	KnowledgeState string        `json:"knowledge_state"`
}

type ReportMetricFacts struct {
	Name         string `json:"name"`
	DisplayLabel string `json:"display_label"`
	Polarity     string `json:"polarity"`
}

type ReportOutlierFacts struct {
	Metric             string                `json:"metric"`
	Scope              AuditScope            `json:"scope"`
	Impact             string                `json:"impact"`
	Coverage           CoverageState         `json:"coverage"`
	EvidenceWindow     ReportWindowReference `json:"evidence_window"`
	RecommendationCode string                `json:"recommendation_code"`
}

type ReportRecommendationFacts struct {
	Code                   string     `json:"code"`
	ViolatedContract       string     `json:"violated_contract"`
	Scope                  AuditScope `json:"scope"`
	EvidenceConfidence     string     `json:"evidence_confidence"`
	ExpectedQualityImpact  string     `json:"expected_quality_impact"`
	BroaderChangeRationale string     `json:"broader_change_rationale"`
}

type ReportProvenanceFact struct {
	Field          string `json:"field"`
	KnowledgeState string `json:"knowledge_state"`
	Provenance     string `json:"provenance"`
}

// AuditProjectResult is the bounded additive governance result.
type AuditProjectResult struct {
	SchemaVersion      string                  `json:"schema_version"`
	Kind               string                  `json:"kind"`
	AuditReference     string                  `json:"audit_reference"`
	Scope              AuditScope              `json:"scope"`
	AsOf               time.Time               `json:"as_of"`
	RevisionHash       string                  `json:"revision_hash"`
	Coverage           CoverageState           `json:"coverage"`
	Metrics            []MetricResult          `json:"metrics"`
	Findings           []AuditFinding          `json:"findings"`
	Recommendations    []AuditRecommendation   `json:"recommendations"`
	GovernanceOverhead []MetricResult          `json:"governance_overhead"`
	Window             AuditWindowProvenance   `json:"window"`
	Contributions      []ScopeContribution     `json:"contributions"`
	WorkloadEffects    []WorkloadDecomposition `json:"workload_effects"`
	InvocationCounts   InvocationCounts        `json:"invocation_counts"`
	Outliers           []NativeOutlier         `json:"outliers"`
	Confounders        []ConfounderStratum     `json:"confounders"`
	Guardrails         []GuardrailResult       `json:"guardrails"`
	DerivationMethod   string                  `json:"derivation_method"`
	OmittedCount       int                     `json:"omitted_count"`
	ReportFacts        *GovernanceReportFacts  `json:"report_facts,omitempty"`
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
