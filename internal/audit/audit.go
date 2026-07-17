// Package audit assembles bounded project governance results from normalized data.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/baseline"
	"github.com/nijanthan-dev/codex-prompt-better/internal/diagnosis"
	"github.com/nijanthan-dev/codex-prompt-better/internal/metrics"
	"github.com/nijanthan-dev/codex-prompt-better/internal/outlier"
	"github.com/nijanthan-dev/codex-prompt-better/internal/recommendation"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const (
	EngineVersion  = "audit-v2"
	MaxResultBytes = 50_000
)

var uuidReference = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var recommendationByFinding = map[string]string{
	"scope_attribution_gap":   "restore_scope_attribution",
	"checkpoint_gap":          "complete_checkpoint_contract",
	"passive_polling":         "wait_on_state_change",
	"repeated_unchanged_call": "reuse_unchanged_evidence",
	"missing_validation":      "validate_after_mutation",
	"privacy_redaction_gap":   "restore_redaction_coverage",
}

type Snapshot struct {
	Reference             string
	Scope                 contracts.AuditScope
	StartsAt              time.Time
	EndsAt                time.Time
	AsOf                  time.Time
	Coverage              contracts.CoverageState
	Metrics               metrics.Input
	GovernanceOverhead    metrics.Input
	PassivePollingRefs    []string
	RepeatedCallRefs      []string
	NecessaryWorkRefs     []string
	Comparisons           map[string]baseline.Comparison
	Window                contracts.AuditWindowProvenance
	Contributions         []contracts.ScopeContribution
	ConfounderStrata      []contracts.ConfounderStratum
	Guardrails            []contracts.GuardrailResult
	WorkloadEffects       []contracts.WorkloadDecomposition
	InvocationCounts      contracts.InvocationCounts
	ReportScopeCounts     contracts.ReportScopeCounts
	ReportSources         []contracts.ReportSourceFacts
	PrivacySuppressed     bool
	OmittedCount          int
	RecommendationContext recommendation.Context `json:"-"`
}

type Source interface {
	Snapshot(context.Context, contracts.AuditProjectRequest) (Snapshot, error)
}

type Engine struct {
	source Source
}

func New(source Source) (*Engine, error) {
	if source == nil {
		return nil, errors.New("audit source required")
	}
	return &Engine{source: source}, nil
}

func (engine *Engine) Run(ctx context.Context, request contracts.AuditProjectRequest) (contracts.AuditProjectResult, error) {
	if err := ValidateRequest(request); err != nil {
		return contracts.AuditProjectResult{}, err
	}
	snapshot, err := engine.source.Snapshot(ctx, request)
	if err != nil {
		return contracts.AuditProjectResult{}, err
	}
	snapshot = normalizeSnapshot(snapshot)
	calculated, err := metrics.Compute(snapshot.Metrics)
	if err != nil {
		return contracts.AuditProjectResult{}, err
	}
	calculated, err = applyComparisons(calculated, snapshot.Comparisons)
	if err != nil {
		return contracts.AuditProjectResult{}, err
	}
	overhead, err := metrics.Compute(snapshot.GovernanceOverhead)
	if err != nil {
		return contracts.AuditProjectResult{}, err
	}
	findings := detect(snapshot)
	recommendations := recommendation.Generate(findings, recommendation.Context{
		AsOf: snapshot.AsOf, QualityGuardrailsPass: guardrailsPass(snapshot.Guardrails),
		MaterialEvidenceRevision: snapshot.RecommendationContext.MaterialEvidenceRevision,
		ExistingRuleCoverage:     snapshot.RecommendationContext.ExistingRuleCoverage,
		Feedback:                 snapshot.RecommendationContext.Feedback,
		EvidenceRevisions:        findingEvidenceRevisions(findings),
	})
	outliers := outlier.Rank(calculated)
	reportFacts := buildReportFacts(snapshot, calculated, findings, recommendations, outliers)
	revisionHash, err := hash(snapshot, calculated, findings, recommendations)
	if err != nil {
		return contracts.AuditProjectResult{}, err
	}
	return contracts.AuditProjectResult{
		SchemaVersion: contracts.SchemaVersion, Kind: "result",
		AuditReference: snapshot.Reference, Scope: snapshot.Scope,
		AsOf: snapshot.AsOf.UTC(), RevisionHash: revisionHash,
		Coverage: snapshot.Coverage, Metrics: calculated, Findings: findings,
		Recommendations: recommendations, GovernanceOverhead: overhead,
		Window: snapshot.Window, Contributions: snapshot.Contributions,
		WorkloadEffects:  snapshot.WorkloadEffects,
		InvocationCounts: snapshot.InvocationCounts,
		Outliers:         outliers,
		Confounders:      snapshot.ConfounderStrata, Guardrails: snapshot.Guardrails,
		DerivationMethod: "normalized_sql+metric-v1+baseline-v1",
		OmittedCount:     snapshot.OmittedCount,
		ReportFacts:      &reportFacts,
	}, nil
}

func buildReportFacts(snapshot Snapshot, calculated []contracts.MetricResult,
	findings []contracts.AuditFinding, recommendations []contracts.AuditRecommendation,
	outliers []contracts.NativeOutlier,
) contracts.GovernanceReportFacts {
	duration := snapshot.EndsAt.Sub(snapshot.StartsAt)
	previous := contracts.ReportWindowReference{
		StartsAt: snapshot.StartsAt.Add(-duration).UTC(), EndsAt: snapshot.StartsAt.UTC(),
		AsOf: snapshot.AsOf.UTC(), Coverage: comparisonCoverage(calculated),
		BaselineVersion: snapshot.Window.BaselineVersion,
		SourceVersions:  append([]string{}, snapshot.Window.SourceVersions...),
	}
	rolling := make([]contracts.ReportWindowReference, 0, 7)
	for offset := 2; offset <= 8; offset++ {
		end := snapshot.EndsAt.Add(-time.Duration(offset-1) * duration)
		rolling = append(rolling, contracts.ReportWindowReference{
			StartsAt: end.Add(-duration).UTC(), EndsAt: end.UTC(),
			AsOf: snapshot.AsOf.UTC(), Coverage: comparisonCoverage(calculated),
			BaselineVersion: snapshot.Window.BaselineVersion,
			SourceVersions:  append([]string{}, snapshot.Window.SourceVersions...),
		})
	}
	definitions := map[string]metrics.Definition{}
	for _, definition := range metrics.Definitions() {
		definitions[definition.Name] = definition
	}
	metricFacts := make([]contracts.ReportMetricFacts, 0, len(calculated))
	metricByName := map[string]contracts.MetricResult{}
	for _, metric := range calculated {
		definition := definitions[metric.Name]
		metricFacts = append(metricFacts, contracts.ReportMetricFacts{
			Name: metric.Name, DisplayLabel: definition.DisplayLabel,
			Polarity: string(definition.Polarity),
		})
		metricByName[metric.Name] = metric
	}
	findingByRecommendation := map[string]contracts.AuditFinding{}
	for _, finding := range findings {
		findingByRecommendation[recommendationByFinding[finding.Code]] = finding
	}
	recommendationFacts := make([]contracts.ReportRecommendationFacts, 0, len(recommendations))
	for _, item := range recommendations {
		finding := findingByRecommendation[item.Code]
		contract := finding.Cause
		confidence := finding.Confidence
		if item.Code == "no_action" {
			contract, confidence = "no violated contract with actionable evidence", "unknown"
		}
		recommendationFacts = append(recommendationFacts, contracts.ReportRecommendationFacts{
			Code: item.Code, ViolatedContract: contract, Scope: snapshot.Scope,
			EvidenceConfidence: confidence, ExpectedQualityImpact: item.ExpectedMovement,
			BroaderChangeRationale: "No model change; use the smallest scoped corrective action and verify it against protected guardrails.",
		})
	}
	outlierFacts := make([]contracts.ReportOutlierFacts, 0, len(outliers))
	for _, item := range outliers {
		metric := metricByName[item.Metric]
		outlierFacts = append(outlierFacts, contracts.ReportOutlierFacts{
			Metric: item.Metric, Scope: snapshot.Scope, Impact: string(metric.Status),
			Coverage: metric.Coverage, EvidenceWindow: contracts.ReportWindowReference{
				StartsAt: snapshot.StartsAt.UTC(), EndsAt: snapshot.EndsAt.UTC(),
				AsOf: snapshot.AsOf.UTC(), Coverage: metric.Coverage,
				BaselineVersion: snapshot.Window.BaselineVersion,
				SourceVersions:  append([]string{}, snapshot.Window.SourceVersions...),
			}, RecommendationCode: linkedRecommendation(definitions[item.Metric], findings, recommendations),
		})
	}
	return contracts.GovernanceReportFacts{
		Version: "report-facts-v1", DisplayIdentity: snapshot.Reference,
		PrivacySuppressed: snapshot.PrivacySuppressed,
		PreviousWindow:    &previous, RollingWindows: rolling,
		ScopeCounts: snapshot.ReportScopeCounts,
		Sources:     snapshot.ReportSources, Metrics: metricFacts,
		QualityGateState: qualityGateState(snapshot.Guardrails),
		Outliers:         outlierFacts, Recommendations: recommendationFacts,
		Provenance: []contracts.ReportProvenanceFact{
			{Field: "metrics", KnowledgeState: "derived", Provenance: "normalized_sql+metric-v1"},
			{Field: "window", KnowledgeState: "observed", Provenance: "normalized_runtime"},
			{Field: "recommendations", KnowledgeState: "derived", Provenance: recommendation.Version},
		},
	}
}

func comparisonCoverage(metrics []contracts.MetricResult) contracts.CoverageState {
	for _, metric := range metrics {
		if metric.PreviousValue != nil || metric.RollingMedian != nil {
			return metric.Coverage
		}
	}
	return contracts.CoverageStateUnknown
}

func qualityGateState(guardrails []contracts.GuardrailResult) string {
	if len(guardrails) == 0 {
		return "unknown"
	}
	state := "pass"
	for _, guardrail := range guardrails {
		if guardrail.State == "fail" {
			return "fail"
		}
		if guardrail.State != "pass" {
			state = "unknown"
		}
	}
	return state
}

func linkedRecommendation(definition metrics.Definition, findings []contracts.AuditFinding,
	recommendations []contracts.AuditRecommendation,
) string {
	allowed := map[string]bool{}
	for _, code := range definition.AllowedDiagnoses {
		allowed[code] = true
	}
	for _, finding := range findings {
		if !allowed[finding.Code] {
			continue
		}
		code := recommendationByFinding[finding.Code]
		for _, item := range recommendations {
			if item.Code == code {
				return code
			}
		}
	}
	return ""
}

// BoundResult enforces the encoded audit result budget without removing
// finding or recommendation evidence used by lifecycle decisions.
func BoundResult(result contracts.AuditProjectResult) (contracts.AuditProjectResult, error) {
	return boundResult(result, MaxResultBytes)
}

func boundResult(result contracts.AuditProjectResult, limit int) (contracts.AuditProjectResult, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return contracts.AuditProjectResult{}, errors.New("encode bounded audit result")
	}
	var bounded contracts.AuditProjectResult
	if err := json.Unmarshal(encoded, &bounded); err != nil {
		return contracts.AuditProjectResult{}, errors.New("clone bounded audit result")
	}
	for {
		encoded, err = json.Marshal(bounded)
		if err != nil {
			return contracts.AuditProjectResult{}, errors.New("encode bounded audit result")
		}
		if len(encoded) <= limit {
			return bounded, nil
		}
		if omitOneEvidenceReference(&bounded) ||
			omitLast(&bounded.Contributions, &bounded.OmittedCount) ||
			omitLast(&bounded.Confounders, &bounded.OmittedCount) ||
			omitLast(&bounded.WorkloadEffects, &bounded.OmittedCount) ||
			omitLast(&bounded.Outliers, &bounded.OmittedCount) {
			continue
		}
		return contracts.AuditProjectResult{}, contracts.NewError(
			contracts.ErrorCodeBudgetExhausted,
			"audit result exceeds output budget",
			"result",
			false,
		)
	}
}

func omitOneEvidenceReference(result *contracts.AuditProjectResult) bool {
	for index := len(result.Metrics) - 1; index >= 0; index-- {
		if omitLast(&result.Metrics[index].EvidenceRefs, &result.OmittedCount) {
			return true
		}
	}
	for index := len(result.Findings) - 1; index >= 0; index-- {
		if omitLast(&result.Findings[index].Counterevidence, &result.OmittedCount) {
			return true
		}
	}
	for index := len(result.Guardrails) - 1; index >= 0; index-- {
		if omitLast(&result.Guardrails[index].EvidenceRefs, &result.OmittedCount) {
			return true
		}
	}
	return false
}

func omitLast[T any](values *[]T, omitted *int) bool {
	if len(*values) == 0 {
		return false
	}
	*values = (*values)[:len(*values)-1]
	(*omitted)++
	return true
}

func findingEvidenceRevisions(findings []contracts.AuditFinding) map[string]string {
	result := map[string]string{}
	for _, finding := range findings {
		data, _ := json.Marshal(finding.EvidenceRefs)
		digest := sha256.Sum256(data)
		result[finding.Code] = hex.EncodeToString(digest[:])
	}
	return result
}

func normalizeSnapshot(snapshot Snapshot) Snapshot {
	if snapshot.Window.StartsAt.IsZero() {
		snapshot.Window = contracts.AuditWindowProvenance{
			StartsAt: snapshot.StartsAt.UTC(), EndsAt: snapshot.EndsAt.UTC(),
			AsOf: snapshot.AsOf.UTC(), Timezone: "UTC",
			BaselineVersion: "baseline-v1", CountingMode: "logical-invocation-v1",
			WorkloadVersion: "workload-v1", SourceVersions: []string{},
			Revision: 1,
		}
	}
	if snapshot.Contributions == nil {
		snapshot.Contributions = []contracts.ScopeContribution{}
	}
	if snapshot.ConfounderStrata == nil {
		snapshot.ConfounderStrata = []contracts.ConfounderStratum{}
	}
	if snapshot.Guardrails == nil {
		snapshot.Guardrails = []contracts.GuardrailResult{}
	}
	if snapshot.WorkloadEffects == nil {
		snapshot.WorkloadEffects = []contracts.WorkloadDecomposition{}
	}
	if snapshot.RecommendationContext.MaterialEvidenceRevision == "" {
		snapshot.RecommendationContext.MaterialEvidenceRevision = snapshot.Reference
	}
	if snapshot.ReportScopeCounts.IncludedTurns == 0 && snapshot.Metrics.CompletedTurns > 0 {
		snapshot.ReportScopeCounts.IncludedTurns = snapshot.Metrics.CompletedTurns
		snapshot.ReportScopeCounts.IncludedToolCalls = snapshot.Metrics.ToolCalls
		snapshot.ReportScopeCounts.IncludedEvidence = snapshot.Metrics.AcceptedEvidence
	}
	return snapshot
}

func applyComparisons(results []contracts.MetricResult,
	comparisons map[string]baseline.Comparison,
) ([]contracts.MetricResult, error) {
	definitions := map[string]metrics.Definition{}
	for _, definition := range metrics.Definitions() {
		definitions[definition.Name] = definition
	}
	for index := range results {
		comparison, ok := comparisons[results[index].Name]
		if !ok {
			results[index].Status = contracts.MetricStatusInsufficient
			results[index].StatusReason = "comparison_unavailable"
			results[index].Confidence = "unknown"
			continue
		}
		result, err := baseline.Apply(results[index], definitions[results[index].Name], comparison)
		if err != nil {
			return nil, err
		}
		results[index] = result
	}
	return results, nil
}

func ValidateRequest(request contracts.AuditProjectRequest) error {
	validScope := request.Scope == contracts.AuditScopePortfolio ||
		request.Scope == contracts.AuditScopeProject ||
		request.Scope == contracts.AuditScopeTask ||
		request.Scope == contracts.AuditScopeTrajectory
	if request.SchemaVersion != contracts.SchemaVersion || request.Kind != "request" ||
		!validScope || request.Reference == "" || len(request.Reference) > 128 ||
		len(request.ConfiguredSources) == 0 || len(request.ConfiguredSources) > 20 {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid project audit request", "request", false)
	}
	seenSources := map[string]bool{}
	for _, source := range request.ConfiguredSources {
		if source == "" || len(source) > 80 || seenSources[source] {
			return contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid project audit sources", "configured_sources", false)
		}
		seenSources[source] = true
	}
	if request.Scope == contracts.AuditScopePortfolio {
		if request.Reference != "all" {
			return contracts.NewError(contracts.ErrorCodeInvalidSchema,
				"portfolio reference must be all", "reference", false)
		}
	} else if !uuidReference.MatchString(request.Reference) {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema,
			"scope reference must be a UUID", "reference", false)
	}
	if request.Consent == contracts.AuditConsentDenied {
		return contracts.NewError(contracts.ErrorCodePermissionDenied, "audit consent denied", "consent", false)
	}
	if request.Consent != contracts.AuditConsentGranted {
		return contracts.NewError(contracts.ErrorCodeApprovalRequired, "explicit audit consent required", "consent", false)
	}
	if request.StartsAt.IsZero() || !request.StartsAt.Before(request.EndsAt) ||
		request.AsOf.Before(request.StartsAt) {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid audit window", "starts_at", false)
	}
	return nil
}

func detect(snapshot Snapshot) []contracts.AuditFinding {
	confoundersMatched := true
	for _, confounder := range snapshot.ConfounderStrata {
		confoundersMatched = confoundersMatched && confounder.Matched
	}
	return diagnosis.Evaluate([]diagnosis.Observation{
		{
			Code: "scope_attribution_gap", Cause: "completed turns lack explicit normalized task attribution",
			ExceptionCheck: "privacy-suppressed and genuinely unknown attribution remain unknown",
			Classification: "uncertain", Coverage: snapshot.Coverage,
			PositiveCount: snapshot.Metrics.CompletedTurns - snapshot.Metrics.AttributedTurns,
			Denominator:   snapshot.Metrics.CompletedTurns, MinimumSample: 1,
			EvidenceRefs: snapshot.Metrics.EvidenceRefs, ConfounderMatched: confoundersMatched,
		},
		{
			Code: "boundary_violation", Cause: "structured boundary decision denied or recorded a violation",
			ExceptionCheck: "host-authoritative denial is necessary and never bypassed",
			Classification: "necessary", Coverage: snapshot.Coverage,
			PositiveCount: snapshot.Metrics.BoundaryViolations,
			Denominator:   snapshot.Metrics.BoundaryDecisions, MinimumSample: 1,
			EvidenceRefs: snapshot.Metrics.EvidenceRefs, RequiredWork: true,
			ConfounderMatched: confoundersMatched,
		},
		{
			Code: "checkpoint_gap", Cause: "structured checkpoint omitted completion, next action, or gate state",
			ExceptionCheck: "checkpoints not required for read-only bounded work",
			Classification: "avoidable", Coverage: snapshot.Coverage,
			PositiveCount: snapshot.Metrics.Checkpoints - snapshot.Metrics.CompleteCheckpoints,
			Denominator:   snapshot.Metrics.Checkpoints, MinimumSample: 1,
			EvidenceRefs: snapshot.Metrics.EvidenceRefs, ConfounderMatched: confoundersMatched,
		},
		{
			Code: "passive_polling", Cause: "unchanged-state passive polling",
			ExceptionCheck: "required waits and user cadence excluded",
			Classification: "avoidable", Coverage: snapshot.Coverage,
			PositiveCount: snapshot.Metrics.PassiveWaitCalls,
			Denominator:   snapshot.Metrics.ToolCalls, MinimumSample: 5, MinimumRate: 0.10,
			EvidenceRefs:    mergeRefs(snapshot.PassivePollingRefs, snapshot.Metrics.EvidenceRefs),
			Counterevidence: snapshot.NecessaryWorkRefs, ConfounderMatched: confoundersMatched,
		},
		{
			Code: "repeated_unchanged_call", Cause: "same canonical call in one state epoch",
			ExceptionCheck: "mutations, changed arguments, retries, and final gates excluded",
			Classification: "avoidable", Coverage: snapshot.Coverage,
			PositiveCount: snapshot.Metrics.RepeatedCalls,
			Denominator:   snapshot.Metrics.ToolCalls, MinimumSample: 5, MinimumRate: 0.10,
			EvidenceRefs:    mergeRefs(snapshot.RepeatedCallRefs, snapshot.Metrics.EvidenceRefs),
			Counterevidence: snapshot.NecessaryWorkRefs, ConfounderMatched: confoundersMatched,
		},
		{
			Code: "tool_error_burst", Cause: "tool failures exceeded the guarded rate",
			ExceptionCheck: "external and expected failures remain confounders",
			Classification: "uncertain", Coverage: snapshot.Coverage,
			PositiveCount: snapshot.Metrics.ToolErrors,
			Denominator:   snapshot.Metrics.ToolCalls, MinimumSample: 5, MinimumRate: 0.05,
			EvidenceRefs: snapshot.Metrics.EvidenceRefs, ConfounderMatched: confoundersMatched,
		},
		{
			Code: "missing_validation", Cause: "observable mutations lack structured validation",
			ExceptionCheck: "read-only and no-op state epochs excluded",
			Classification: "avoidable", Coverage: snapshot.Coverage,
			PositiveCount: snapshot.Metrics.ObservableMutations - snapshot.Metrics.ValidatedMutations,
			Denominator:   snapshot.Metrics.ObservableMutations, MinimumSample: 1,
			EvidenceRefs: snapshot.Metrics.EvidenceRefs, ConfounderMatched: confoundersMatched,
		},
		{
			Code: "privacy_redaction_gap", Cause: "accepted evidence lacks a complete redaction state",
			ExceptionCheck: "not-needed redaction is an explicit passing state",
			Classification: "avoidable", Coverage: snapshot.Coverage,
			PositiveCount: snapshot.Metrics.AcceptedEvidence - snapshot.Metrics.RedactedEvidence,
			Denominator:   snapshot.Metrics.AcceptedEvidence, MinimumSample: 1,
			EvidenceRefs: snapshot.Metrics.EvidenceRefs, ConfounderMatched: confoundersMatched,
		},
	})
}

func mergeRefs(groups ...[]string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, group := range groups {
		for _, value := range group {
			if value != "" && !seen[value] && len(result) < 100 {
				seen[value] = true
				result = append(result, value)
			}
		}
	}
	sort.Strings(result)
	return result
}

func guardrailsPass(guardrails []contracts.GuardrailResult) bool {
	if len(guardrails) == 0 {
		return false
	}
	for _, guardrail := range guardrails {
		if guardrail.State != "pass" {
			return false
		}
	}
	return true
}

func hash(snapshot Snapshot, results []contracts.MetricResult, findings []contracts.AuditFinding, recommendations []contracts.AuditRecommendation) (string, error) {
	snapshot.Window.Revision = 0
	snapshot.Window.LateEvidence = false
	payload := struct {
		Version         string
		Snapshot        Snapshot
		Results         []contracts.MetricResult
		Findings        []contracts.AuditFinding
		Recommendations []contracts.AuditRecommendation
	}{
		Version: EngineVersion, Snapshot: snapshot, Results: results,
		Findings: findings, Recommendations: recommendations,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("encode audit revision")
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
