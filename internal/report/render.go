// Package report renders bounded governance results without reading storage.
package report

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const MaxResultBytes = 50_000
const timeFormat = "2006-01-02T15:04:05Z"

var activeLink = regexp.MustCompile(`(?i)\b(https?|ftp|mailto|javascript|data|file):|\bwww\.`)

type Model struct {
	Identity         string
	Scope            string
	Coverage         string
	QualityGate      string
	Revision         string
	AsOf             string
	Window           string
	Baseline         string
	Comparisons      []string
	Privacy          string
	Lineage          string
	Derivation       string
	UpstreamOmitted  int
	ScopeCounts      []count
	Sources          []source
	Metrics          []metric
	Contributions    []contribution
	Workload         []workload
	Guardrails       []guardrail
	Overhead         []metric
	Findings         []finding
	Recommendations  []recommendation
	Outliers         []outlier
	Confounders      []confounder
	Provenance       []provenance
	initialOmissions map[string]int
}

type count struct {
	Name     string
	Included int
	Excluded string
}
type source struct{ Kind, Version, Freshness, Coverage, Redaction, Knowledge string }
type metric struct {
	Label, Name, Value, Numerator, Denominator, Previous, Median string
	Unit, Status, Reason, Coverage, Confidence, Uncertainty      string
	Polarity, Exclusions                                         string
	Sample                                                       int
}
type contribution struct{ Scope, Class, Numerator, Denominator, Value, Coverage, Attribution string }
type workload struct{ Metric, Volume, Mix, Within, Residual, Status, Classes string }
type guardrail struct{ Name, State, Coverage string }
type finding struct{ Code, Classification, Confidence, Cause, Detector, Exception, Counterevidence string }
type recommendation struct{ Code, Action, Verification, Lifecycle, Impact, Contract, Scope, EvidenceConfidence, Rationale, Target, Guardrails, Risks, Approval string }
type outlier struct{ Metric, Value, Unit, Direction, Magnitude, Impact, Scope, Coverage, Window, Recommendation string }
type confounder struct{ Kind, State, Confidence string }
type provenance struct{ Field, Knowledge, Source string }

func Build(result contracts.AuditProjectResult) (Model, error) {
	if result.ReportFacts == nil || result.ReportFacts.Version != "report-facts-v1" {
		return Model{}, fmt.Errorf("report facts unavailable")
	}
	facts := result.ReportFacts
	labels := make(map[string]contracts.ReportMetricFacts, len(facts.Metrics))
	for _, fact := range facts.Metrics {
		if _, exists := labels[fact.Name]; exists {
			return Model{}, fmt.Errorf("report metric facts are ambiguous")
		}
		labels[fact.Name] = fact
	}
	model := Model{
		Identity: clean(facts.DisplayIdentity), Scope: clean(string(result.Scope)),
		Coverage: clean(string(result.Coverage)), QualityGate: clean(facts.QualityGateState),
		Revision: clean(result.RevisionHash), AsOf: result.AsOf.UTC().Format("2006-01-02T15:04:05Z"),
		Window:      fmt.Sprintf("%s to %s; revision %d; late evidence %t; baseline %s; counting %s; workload %s; sources %s", result.Window.StartsAt.UTC().Format(timeFormat), result.Window.EndsAt.UTC().Format(timeFormat), result.Window.Revision, result.Window.LateEvidence, clean(result.Window.BaselineVersion), clean(result.Window.CountingMode), clean(result.Window.WorkloadVersion), cleanList(result.Window.SourceVersions)),
		Baseline:    baselineSummary(facts),
		Comparisons: comparisonWindows(facts), Privacy: privacyState(facts.PrivacySuppressed),
		Lineage:    fmt.Sprintf("host calls %d; leaf calls %d; host results %d; leaf results %d; unknown calls %d", result.InvocationCounts.HostCalls, result.InvocationCounts.LeafCalls, result.InvocationCounts.HostResults, result.InvocationCounts.LeafResults, result.InvocationCounts.UnknownCalls),
		Derivation: clean(result.DerivationMethod), UpstreamOmitted: result.OmittedCount,
	}
	model.ScopeCounts = scopeCounts(facts.ScopeCounts)
	for _, value := range facts.Sources {
		model.Sources = append(model.Sources, source{clean(value.SourceKind), clean(value.Version), clean(value.Freshness), clean(string(value.Coverage)), clean(value.RedactionState), clean(value.KnowledgeState)})
	}
	metricNames := map[string]bool{}
	for _, value := range result.Metrics {
		fact, found := labels[value.Name]
		if metricNames[value.Name] || !found {
			return Model{}, fmt.Errorf("report metrics are ambiguous or incomplete")
		}
		metricNames[value.Name] = true
		model.Metrics = append(model.Metrics, metric{
			Label: clean(fact.DisplayLabel), Name: clean(value.Name), Value: number(value.NativeValue), Numerator: number(value.Numerator), Denominator: number(value.Denominator), Previous: number(value.PreviousValue), Median: number(value.RollingMedian),
			Unit: clean(value.NativeUnit), Status: clean(string(value.Status)), Reason: clean(value.StatusReason), Coverage: clean(string(value.Coverage)),
			Confidence: clean(value.Confidence), Uncertainty: clean(value.Uncertainty), Polarity: clean(fact.Polarity), Exclusions: cleanList(value.Exclusions), Sample: value.SampleCount,
		})
	}
	for _, value := range result.Contributions {
		numerator, denominator, contributionValue := finiteNumber(value.Numerator), finiteNumber(value.Denominator), number(value.Contribution)
		if facts.PrivacySuppressed || value.AttributionState == "privacy_suppressed" {
			numerator, denominator, contributionValue = "unknown", "unknown", "unknown"
		}
		model.Contributions = append(model.Contributions, contribution{clean(string(value.Scope)), clean(value.WorkloadClass), numerator, denominator, contributionValue, clean(string(value.Coverage)), clean(value.AttributionState)})
	}
	for _, value := range result.WorkloadEffects {
		model.Workload = append(model.Workload, workload{clean(value.Metric), number(value.VolumeEffect), number(value.MixEffect), number(value.WithinClassEffect), number(value.Residual), clean(value.Status), cleanList(value.Classes)})
	}
	for _, value := range result.Guardrails {
		model.Guardrails = append(model.Guardrails, guardrail{clean(value.Name), clean(value.State), clean(string(value.Coverage))})
	}
	for _, value := range result.GovernanceOverhead {
		fact := labels[value.Name]
		model.Overhead = append(model.Overhead, metric{Label: clean(fact.DisplayLabel), Name: clean(value.Name), Value: number(value.NativeValue), Previous: number(value.PreviousValue), Median: number(value.RollingMedian), Unit: clean(value.NativeUnit), Status: clean(string(value.Status)), Reason: clean(value.StatusReason), Coverage: clean(string(value.Coverage)), Confidence: clean(value.Confidence), Uncertainty: clean(value.Uncertainty), Polarity: clean(fact.Polarity)})
	}
	for _, value := range result.Findings {
		detector := ""
		if value.Detector != "" || value.DetectorVersion != "" {
			detector = value.Detector + "@" + value.DetectorVersion
		}
		model.Findings = append(model.Findings, finding{
			Code: clean(value.Code), Classification: known(value.Classification), Confidence: known(value.Confidence),
			Cause: known(value.Cause), Detector: known(detector), Exception: known(value.ExceptionCheck),
			Counterevidence: cleanList(value.Counterevidence),
		})
	}
	recommendationFacts := make(map[string]contracts.ReportRecommendationFacts, len(facts.Recommendations))
	for _, value := range facts.Recommendations {
		if _, exists := recommendationFacts[value.Code]; exists {
			return Model{}, fmt.Errorf("report recommendation facts are ambiguous")
		}
		recommendationFacts[value.Code] = value
	}
	recommendationCodes := map[string]bool{}
	for _, value := range result.Recommendations {
		fact, found := recommendationFacts[value.Code]
		if recommendationCodes[value.Code] || !found {
			return Model{}, fmt.Errorf("report recommendations are ambiguous or incomplete")
		}
		recommendationCodes[value.Code] = true
		model.Recommendations = append(model.Recommendations, recommendation{
			Code: clean(value.Code), Action: known(value.Action), Verification: known(value.Verification),
			Lifecycle: known(value.LifecycleState), Impact: known(fact.ExpectedQualityImpact),
			Contract: known(fact.ViolatedContract), Scope: known(string(fact.Scope)),
			EvidenceConfidence: known(fact.EvidenceConfidence), Rationale: known(fact.BroaderChangeRationale),
			Target: known(value.TargetSurface), Guardrails: cleanList(value.ProtectedGuardrails),
			Risks: cleanList(value.Risks), Approval: fmt.Sprintf("%t", value.ApprovalRequired),
		})
	}
	resultOutliers := map[string]bool{}
	for _, value := range result.Outliers {
		if resultOutliers[value.Metric] {
			return Model{}, fmt.Errorf("report outliers are ambiguous")
		}
		resultOutliers[value.Metric] = true
		model.Outliers = append(model.Outliers, outlier{Metric: clean(value.Metric), Value: number(value.NativeValue), Unit: clean(value.NativeUnit), Direction: clean(value.Direction), Magnitude: number(value.Magnitude)})
	}
	outlierFacts := map[string]bool{}
	for _, value := range facts.Outliers {
		if outlierFacts[value.Metric] {
			return Model{}, fmt.Errorf("report outlier facts are ambiguous")
		}
		if !resultOutliers[value.Metric] {
			return Model{}, fmt.Errorf("report outlier facts are incomplete")
		}
		outlierFacts[value.Metric] = true
		for i := range model.Outliers {
			if model.Outliers[i].Metric == clean(value.Metric) {
				model.Outliers[i].Impact = clean(value.Impact)
				model.Outliers[i].Scope = clean(string(value.Scope))
				model.Outliers[i].Coverage = clean(string(value.Coverage))
				model.Outliers[i].Window = reportWindow(value.EvidenceWindow)
				model.Outliers[i].Recommendation = clean(value.RecommendationCode)
			}
		}
	}
	if len(outlierFacts) != len(resultOutliers) {
		return Model{}, fmt.Errorf("report outlier facts are incomplete")
	}
	for _, value := range result.Confounders {
		model.Confounders = append(model.Confounders, confounder{clean(value.Kind), clean(value.State), clean(value.Confidence)})
	}
	for _, value := range facts.Provenance {
		model.Provenance = append(model.Provenance, provenance{clean(value.Field), clean(value.KnowledgeState), clean(value.Provenance)})
	}
	sortModel(&model)
	if len(model.Recommendations) > 5 {
		model.initialOmissions = map[string]int{"recommendations": len(model.Recommendations) - 5}
		model.Recommendations = model.Recommendations[:5]
	}
	return model, nil
}

func RenderBounded(model Model, format contracts.ReportFormat, base contracts.RenderGovernanceReportResult) (contracts.RenderGovernanceReportResult, error) {
	omitted := make(map[string]int, len(model.initialOmissions))
	for section, count := range model.initialOmissions {
		omitted[section] = count
	}
	for {
		base.Rendered = Render(model, format)
		base.OmittedCount = sum(omitted)
		base.SectionOmissions = omissions(omitted)
		encoded, err := json.Marshal(base)
		if err != nil {
			return contracts.RenderGovernanceReportResult{}, err
		}
		if len(encoded) <= MaxResultBytes {
			return base, nil
		}
		section := truncate(&model)
		if section == "" {
			return contracts.RenderGovernanceReportResult{}, fmt.Errorf("report envelope exceeds byte budget")
		}
		omitted[section]++
	}
}

func Render(model Model, format contracts.ReportFormat) string {
	switch format {
	case contracts.ReportFormatChat:
		return renderChat(model)
	case contracts.ReportFormatTable:
		return renderTable(model)
	default:
		return renderMarkdown(model)
	}
}

func renderMarkdown(m Model) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Governance report\n\n- Identity: %s\n- Scope: `%s`\n- Coverage: `%s`\n- Quality gate: `%s`\n- Privacy: `%s`\n- As of: `%s`\n- Revision: `%s`\n- Window: %s\n- Baseline: %s\n- Lineage: %s\n- Derivation: %s\n- Upstream omissions: %d\n", md(m.Identity), md(m.Scope), md(m.Coverage), md(m.QualityGate), md(m.Privacy), md(m.AsOf), md(m.Revision), md(m.Window), md(m.Baseline), md(m.Lineage), md(m.Derivation), m.UpstreamOmitted)
	writeMarkdownSections(&b, m)
	return b.String()
}

func renderChat(m Model) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Governance report\nIdentity: %s\nScope: %s | Coverage: %s | Quality gate: %s | Privacy: %s\nAs of: %s | Revision: %s\nWindow: %s\nBaseline: %s\nLineage: %s\nDerivation: %s | Upstream omissions: %d\n", m.Identity, m.Scope, m.Coverage, m.QualityGate, m.Privacy, m.AsOf, m.Revision, m.Window, m.Baseline, m.Lineage, m.Derivation, m.UpstreamOmitted)
	for _, value := range m.Comparisons {
		fmt.Fprintf(&b, "Comparison window: %s\n", value)
	}
	for _, v := range m.ScopeCounts {
		fmt.Fprintf(&b, "Scope: %s included=%d excluded=%s\n", v.Name, v.Included, v.Excluded)
	}
	for _, v := range m.Sources {
		fmt.Fprintf(&b, "Source: %s version=%s freshness=%s coverage=%s redaction=%s knowledge=%s\n", v.Kind, v.Version, v.Freshness, v.Coverage, v.Redaction, v.Knowledge)
	}
	for _, r := range m.Recommendations {
		fmt.Fprintf(&b, "Next action: %s — %s. Target: %s. Contract: %s. Scope: %s. Lifecycle: %s. Evidence: %s. Quality impact: %s. Approval required: %s. Guardrails: %s. Risks: %s. Broader-change rationale: %s. Verify: %s\n", r.Code, r.Action, r.Target, r.Contract, r.Scope, r.Lifecycle, r.EvidenceConfidence, r.Impact, r.Approval, r.Guardrails, r.Risks, r.Rationale, r.Verification)
	}
	for _, v := range m.Metrics {
		fmt.Fprintf(&b, "Metric: %s = %s %s; raw=%s/%s; sample=%d; previous=%s; median=%s (%s: %s, %s, confidence=%s, uncertainty=%s, polarity=%s, exclusions=%s)\n", label(v), v.Value, v.Unit, v.Numerator, v.Denominator, v.Sample, v.Previous, v.Median, v.Status, v.Reason, v.Coverage, v.Confidence, v.Uncertainty, v.Polarity, v.Exclusions)
	}
	for _, v := range m.Contributions {
		fmt.Fprintf(&b, "Contribution: %s/%s = %s; raw=%s/%s (%s, %s)\n", v.Scope, v.Class, v.Value, v.Numerator, v.Denominator, v.Coverage, v.Attribution)
	}
	for _, v := range m.Workload {
		fmt.Fprintf(&b, "Workload: %s volume=%s mix=%s within=%s residual=%s (%s; classes=%s)\n", v.Metric, v.Volume, v.Mix, v.Within, v.Residual, v.Status, v.Classes)
	}
	for _, v := range m.Guardrails {
		fmt.Fprintf(&b, "Guardrail: %s = %s (%s)\n", v.Name, v.State, v.Coverage)
	}
	for _, v := range m.Overhead {
		fmt.Fprintf(&b, "Governance overhead (excluded): %s = %s %s (%s)\n", label(v), v.Value, v.Unit, v.Coverage)
	}
	for _, v := range m.Findings {
		fmt.Fprintf(&b, "Finding: %s — %s; cause=%s; detector=%s; exception=%s; counterevidence=%s (%s)\n", v.Code, v.Classification, v.Cause, v.Detector, v.Exception, v.Counterevidence, v.Confidence)
	}
	for _, v := range m.Outliers {
		fmt.Fprintf(&b, "Outlier: %s = %s %s (%s); dimensionless magnitude=%s; impact=%s; scope=%s; coverage=%s; window=%s; action=%s\n", v.Metric, v.Value, v.Unit, v.Direction, v.Magnitude, v.Impact, v.Scope, v.Coverage, v.Window, v.Recommendation)
	}
	for _, v := range m.Confounders {
		fmt.Fprintf(&b, "Confounder: %s = %s (%s)\n", v.Kind, v.State, v.Confidence)
	}
	for _, v := range m.Provenance {
		fmt.Fprintf(&b, "Provenance: %s = %s / %s\n", v.Field, v.Knowledge, v.Source)
	}
	return b.String()
}

func renderTable(m Model) string {
	var b strings.Builder
	b.WriteString("section | name | value | state\n--- | --- | --- | ---\n")
	fmt.Fprintf(&b, "summary | identity | %s | %s\nsummary | scope | %s | %s\nsummary | quality gate | %s | %s\n", cell(m.Identity), cell(m.Coverage), cell(m.Scope), cell(m.Coverage), cell(m.QualityGate), cell(m.Coverage))
	for _, v := range m.Sources {
		fmt.Fprintf(&b, "source | %s %s | %s | %s\n", cell(v.Kind), cell(v.Version), cell(v.Freshness), cell(v.Coverage))
	}
	for _, v := range m.Recommendations {
		fmt.Fprintf(&b, "recommendation | %s | %s | %s\n", cell(v.Code), cell(v.Action), cell(v.Lifecycle))
	}
	for _, v := range m.Metrics {
		fmt.Fprintf(&b, "metric | %s | %s %s | %s\n", cell(label(v)), cell(v.Value), cell(v.Unit), cell(v.Status))
	}
	for _, v := range m.Contributions {
		fmt.Fprintf(&b, "contribution | %s / %s | %s | %s\n", cell(v.Scope), cell(v.Class), cell(v.Value), cell(v.Coverage))
	}
	for _, v := range m.Guardrails {
		fmt.Fprintf(&b, "guardrail | %s | %s | %s\n", cell(v.Name), cell(v.State), cell(v.Coverage))
	}
	for _, v := range m.Overhead {
		fmt.Fprintf(&b, "overhead excluded | %s | %s %s | %s\n", cell(label(v)), cell(v.Value), cell(v.Unit), cell(v.Coverage))
	}
	for _, v := range m.Findings {
		fmt.Fprintf(&b, "finding | %s | %s | %s\n", cell(v.Code), cell(v.Classification), cell(v.Confidence))
	}
	for _, v := range m.Workload {
		fmt.Fprintf(&b, "workload | %s | mix %s; within %s | %s\n", cell(v.Metric), cell(v.Mix), cell(v.Within), cell(v.Status))
	}
	for _, v := range m.Outliers {
		fmt.Fprintf(&b, "outlier | %s | %s %s | %s\n", cell(v.Metric), cell(v.Value), cell(v.Unit), cell(v.Direction))
	}
	for _, v := range m.Confounders {
		fmt.Fprintf(&b, "confounder | %s | %s | %s\n", cell(v.Kind), cell(v.State), cell(v.Confidence))
	}
	for _, v := range m.Provenance {
		fmt.Fprintf(&b, "provenance | %s | %s | %s\n", cell(v.Field), cell(v.Knowledge), cell(v.Source))
	}
	b.WriteString("\nText alternative:\n")
	b.WriteString(wrapText(renderChat(m), 72))
	return b.String()
}

func writeMarkdownSections(b *strings.Builder, m Model) {
	if len(m.Comparisons) > 0 {
		b.WriteString("\n### Comparison windows\n")
		for _, value := range m.Comparisons {
			fmt.Fprintf(b, "- %s\n", md(value))
		}
	}
	if len(m.ScopeCounts) > 0 {
		b.WriteString("\n### Scope and drill-down\n")
		for _, v := range m.ScopeCounts {
			fmt.Fprintf(b, "- %s: included %d; excluded %s\n", md(v.Name), v.Included, md(v.Excluded))
		}
	}
	if len(m.Sources) > 0 {
		b.WriteString("\n### Sources\n")
		for _, v := range m.Sources {
			fmt.Fprintf(b, "- %s %s: freshness %s; coverage %s; redaction %s; knowledge %s\n", md(v.Kind), md(v.Version), md(v.Freshness), md(v.Coverage), md(v.Redaction), md(v.Knowledge))
		}
	}
	if len(m.Recommendations) > 0 {
		b.WriteString("\n### Next actions\n")
		for _, v := range m.Recommendations {
			fmt.Fprintf(b, "- **%s:** %s; target: %s; contract: %s; scope: %s; evidence: %s; quality impact: %s; approval required: %s; guardrails: %s; risks: %s; broader-change rationale: %s; verify: %s\n", md(v.Code), md(v.Action), md(v.Target), md(v.Contract), md(v.Scope), md(v.EvidenceConfidence), md(v.Impact), md(v.Approval), md(v.Guardrails), md(v.Risks), md(v.Rationale), md(v.Verification))
		}
	}
	if len(m.Metrics) > 0 {
		b.WriteString("\n### Metrics\n\nmetric | value | status | coverage\n--- | ---: | --- | ---\n")
		for _, v := range m.Metrics {
			fmt.Fprintf(b, "%s | %s %s; raw %s/%s; sample %d (previous %s; median %s) | %s: %s; confidence %s; uncertainty %s; polarity %s; exclusions %s | %s\n", md(label(v)), md(v.Value), md(v.Unit), md(v.Numerator), md(v.Denominator), v.Sample, md(v.Previous), md(v.Median), md(v.Status), md(v.Reason), md(v.Confidence), md(v.Uncertainty), md(v.Polarity), md(v.Exclusions), md(v.Coverage))
		}
	}
	if len(m.Contributions) > 0 {
		b.WriteString("\n### Contributions\n")
		for _, v := range m.Contributions {
			fmt.Fprintf(b, "- %s / %s: %s; raw %s/%s (%s; %s)\n", md(v.Scope), md(v.Class), md(v.Value), md(v.Numerator), md(v.Denominator), md(v.Coverage), md(v.Attribution))
		}
	}
	if len(m.Workload) > 0 {
		b.WriteString("\n### Workload and mix explanation\n")
		for _, v := range m.Workload {
			fmt.Fprintf(b, "- %s: volume %s; mix %s; within-class %s; residual %s; status %s; classes %s\n", md(v.Metric), md(v.Volume), md(v.Mix), md(v.Within), md(v.Residual), md(v.Status), md(v.Classes))
		}
	}
	if len(m.Guardrails) > 0 {
		b.WriteString("\n### Guardrails\n")
		for _, v := range m.Guardrails {
			fmt.Fprintf(b, "- %s: %s (%s)\n", md(v.Name), md(v.State), md(v.Coverage))
		}
	}
	if len(m.Overhead) > 0 {
		b.WriteString("\n### Governance overhead (excluded from outcome)\n")
		for _, v := range m.Overhead {
			fmt.Fprintf(b, "- %s: %s %s (%s)\n", md(label(v)), md(v.Value), md(v.Unit), md(v.Coverage))
		}
	}
	if len(m.Findings) > 0 {
		b.WriteString("\n### Findings\n")
		for _, v := range m.Findings {
			fmt.Fprintf(b, "- **%s:** %s; cause %s; detector %s; exception %s; counterevidence %s (%s)\n", md(v.Code), md(v.Classification), md(v.Cause), md(v.Detector), md(v.Exception), md(v.Counterevidence), md(v.Confidence))
		}
	}
	if len(m.Outliers) > 0 {
		b.WriteString("\n### Outliers\n")
		for _, v := range m.Outliers {
			fmt.Fprintf(b, "- %s: %s %s, %s; dimensionless magnitude %s; impact %s; scope %s; coverage %s; window %s; linked action %s\n", md(v.Metric), md(v.Value), md(v.Unit), md(v.Direction), md(v.Magnitude), md(v.Impact), md(v.Scope), md(v.Coverage), md(v.Window), md(v.Recommendation))
		}
	}
	if len(m.Confounders) > 0 {
		b.WriteString("\n### Confounders\n")
		for _, v := range m.Confounders {
			fmt.Fprintf(b, "- %s: %s (%s)\n", md(v.Kind), md(v.State), md(v.Confidence))
		}
	}
	if len(m.Provenance) > 0 {
		b.WriteString("\n### Provenance\n")
		for _, v := range m.Provenance {
			fmt.Fprintf(b, "- %s: %s / %s\n", md(v.Field), md(v.Knowledge), md(v.Source))
		}
	}
}

func clean(value string) string {
	value = strings.ToValidUTF8(value, "�")
	var b strings.Builder
	space := false
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u202a' || r == '\u202b' || r == '\u202d' || r == '\u202e' || r == '\u2066' || r == '\u2067' || r == '\u2068' || r == '\u2069' {
			space = true
			continue
		}
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	value = strings.NewReplacer("<", "‹", ">", "›").Replace(strings.TrimSpace(b.String()))
	value = strings.ReplaceAll(value, "::", "∶∶")
	return neutralizeSchemes(value)
}

// Sanitize removes presentation controls from an untrusted display value.
func Sanitize(value string) string { return clean(value) }

// SanitizeMarkdown removes presentation controls and escapes Markdown syntax.
func SanitizeMarkdown(value string) string { return md(value) }

func md(value string) string {
	return strings.NewReplacer("\\", "\\\\", "|", "\\|", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)", "<", "&lt;", ">", "&gt;").Replace(clean(value))
}
func cell(value string) string {
	runes := []rune(md(value))
	if len(runes) <= 28 {
		return string(runes)
	}
	return string(runes[:27]) + "…"
}
func wrapText(value string, width int) string {
	var output strings.Builder
	for _, line := range strings.Split(value, "\n") {
		for len([]rune(line)) > width {
			runes := []rune(line)
			cut := width
			for cut > width/2 && runes[cut] != ' ' {
				cut--
			}
			if cut == width/2 {
				cut = width
			}
			output.WriteString(strings.TrimSpace(string(runes[:cut])))
			output.WriteByte('\n')
			line = strings.TrimSpace(string(runes[cut:]))
		}
		if line != "" {
			output.WriteString(line)
			output.WriteByte('\n')
		}
	}
	return strings.TrimSuffix(output.String(), "\n")
}
func number(value *float64) string {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return "unknown"
	}
	return fmt.Sprintf("%.6g", *value)
}
func finiteNumber(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "unknown"
	}
	return fmt.Sprintf("%.6g", value)
}
func label(v metric) string {
	if v.Label != "" {
		return v.Label
	}
	return v.Name
}
func sortModel(m *Model) {
	sort.Slice(m.ScopeCounts, func(i, j int) bool { return m.ScopeCounts[i].Name < m.ScopeCounts[j].Name })
	sort.Slice(m.Sources, func(i, j int) bool { return sourceKey(m.Sources[i]) < sourceKey(m.Sources[j]) })
	sort.Slice(m.Metrics, func(i, j int) bool { return metricKey(m.Metrics[i]) < metricKey(m.Metrics[j]) })
	sort.Slice(m.Findings, func(i, j int) bool { return findingKey(m.Findings[i]) < findingKey(m.Findings[j]) })
	sort.Slice(m.Outliers, func(i, j int) bool { return outlierKey(m.Outliers[i]) < outlierKey(m.Outliers[j]) })
	sort.Slice(m.Confounders, func(i, j int) bool { return confounderKey(m.Confounders[i]) < confounderKey(m.Confounders[j]) })
	sort.Slice(m.Provenance, func(i, j int) bool { return provenanceKey(m.Provenance[i]) < provenanceKey(m.Provenance[j]) })
	sort.Slice(m.Contributions, func(i, j int) bool {
		return contributionKey(m.Contributions[i]) < contributionKey(m.Contributions[j])
	})
	sort.Slice(m.Workload, func(i, j int) bool { return workloadKey(m.Workload[i]) < workloadKey(m.Workload[j]) })
	sort.Slice(m.Guardrails, func(i, j int) bool { return guardrailKey(m.Guardrails[i]) < guardrailKey(m.Guardrails[j]) })
	sort.Slice(m.Overhead, func(i, j int) bool { return metricKey(m.Overhead[i]) < metricKey(m.Overhead[j]) })
}

func sourceKey(v source) string {
	return strings.Join([]string{v.Kind, v.Version, v.Freshness, v.Coverage, v.Redaction, v.Knowledge}, "\x00")
}
func metricKey(v metric) string {
	return strings.Join([]string{v.Name, v.Value, v.Status, v.Coverage}, "\x00")
}
func findingKey(v finding) string {
	return strings.Join([]string{v.Code, v.Detector, v.Classification, v.Confidence}, "\x00")
}
func outlierKey(v outlier) string {
	return strings.Join([]string{v.Metric, v.Scope, v.Value, v.Direction}, "\x00")
}
func confounderKey(v confounder) string {
	return strings.Join([]string{v.Kind, v.State, v.Confidence}, "\x00")
}
func provenanceKey(v provenance) string {
	return strings.Join([]string{v.Field, v.Knowledge, v.Source}, "\x00")
}
func contributionKey(v contribution) string {
	return strings.Join([]string{v.Scope, v.Class, v.Numerator, v.Denominator, v.Value, v.Coverage, v.Attribution}, "\x00")
}
func workloadKey(v workload) string {
	return strings.Join([]string{v.Metric, v.Status, v.Classes, v.Volume, v.Mix, v.Within, v.Residual}, "\x00")
}
func guardrailKey(v guardrail) string {
	return strings.Join([]string{v.Name, v.State, v.Coverage}, "\x00")
}
func sum(values map[string]int) int {
	total := 0
	for _, v := range values {
		total += v
	}
	return total
}
func omissions(values map[string]int) []contracts.ReportSectionOmission {
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	result := make([]contracts.ReportSectionOmission, 0, len(names))
	for _, n := range names {
		result = append(result, contracts.ReportSectionOmission{Section: n, Count: values[n]})
	}
	return result
}
func truncate(model *Model) string {
	if n := len(model.Confounders); n > 0 {
		model.Confounders = model.Confounders[:n-1]
		return "confounders"
	}
	if n := len(model.Outliers); n > 0 {
		model.Outliers = model.Outliers[:n-1]
		return "outliers"
	}
	if n := len(model.Findings); n > 0 {
		model.Findings = model.Findings[:n-1]
		return "findings"
	}
	if n := len(model.Metrics); n > 0 {
		model.Metrics = model.Metrics[:n-1]
		return "metrics"
	}
	if n := len(model.Contributions); n > 0 {
		model.Contributions = model.Contributions[:n-1]
		return "contributions"
	}
	if n := len(model.Workload); n > 0 {
		model.Workload = model.Workload[:n-1]
		return "workload"
	}
	if n := len(model.Sources); n > 0 {
		model.Sources = model.Sources[:n-1]
		return "sources"
	}
	if n := len(model.Overhead); n > 0 {
		model.Overhead = model.Overhead[:n-1]
		return "overhead"
	}
	if n := len(model.Comparisons); n > 1 {
		model.Comparisons = model.Comparisons[:n-1]
		return "comparison_windows"
	}
	if n := len(model.Provenance); n > 1 {
		model.Provenance = model.Provenance[:n-1]
		return "provenance"
	}
	if n := len(model.Recommendations); n > 1 {
		model.Recommendations = model.Recommendations[:n-1]
		return "recommendations"
	}
	return ""
}

func scopeCounts(value contracts.ReportScopeCounts) []count {
	return []count{
		{"trajectories", value.IncludedTrajectories, optionalInt(value.ExcludedTrajectories)},
		{"turns", value.IncludedTurns, optionalInt(value.ExcludedTurns)},
		{"tool calls", value.IncludedToolCalls, optionalInt(value.ExcludedToolCalls)},
		{"evidence", value.IncludedEvidence, optionalInt(value.ExcludedEvidence)},
	}
}

func optionalInt(value *int) string {
	if value == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *value)
}

func baselineSummary(facts *contracts.GovernanceReportFacts) string {
	previous := "unknown"
	if facts.PreviousWindow != nil {
		previous = facts.PreviousWindow.AsOf.UTC().Format(timeFormat) + " / " + clean(string(facts.PreviousWindow.Coverage))
	}
	return fmt.Sprintf("previous %s; rolling windows %d", previous, len(facts.RollingWindows))
}

func comparisonWindows(facts *contracts.GovernanceReportFacts) []string {
	result := make([]string, 0, len(facts.RollingWindows)+1)
	if facts.PreviousWindow != nil {
		result = append(result, "previous: "+reportWindow(*facts.PreviousWindow))
	}
	for index, value := range facts.RollingWindows {
		result = append(result, fmt.Sprintf("rolling %d: %s", index+1, reportWindow(value)))
	}
	return result
}

func reportWindow(value contracts.ReportWindowReference) string {
	return fmt.Sprintf("%s to %s; as of %s; coverage %s; baseline %s; sources %s", value.StartsAt.UTC().Format(timeFormat), value.EndsAt.UTC().Format(timeFormat), value.AsOf.UTC().Format(timeFormat), clean(string(value.Coverage)), clean(value.BaselineVersion), cleanList(value.SourceVersions))
}

func privacyState(suppressed bool) string {
	if suppressed {
		return "suppressed"
	}
	return "redacted aggregate"
}

func sortedStrings(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	return result
}

func cleanList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return clean(strings.Join(sortedStrings(values), ", "))
}

func known(value string) string {
	value = clean(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func neutralizeSchemes(value string) string {
	return activeLink.ReplaceAllStringFunc(value, func(match string) string {
		if strings.EqualFold(match, "www.") {
			return "www∶"
		}
		return strings.TrimSuffix(match, ":") + "∶"
	})
}
