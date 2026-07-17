package report

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestRenderFormatsAreDeterministicSanitizedAndEquivalent(t *testing.T) {
	result := fixture()
	model, err := Build(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []contracts.ReportFormat{contracts.ReportFormatMarkdown, contracts.ReportFormatTable, contracts.ReportFormatChat} {
		first := Render(model, format)
		second := Render(model, format)
		if first != second || !strings.Contains(first, "synthetic") || !strings.Contains(first, "quality") {
			t.Fatalf("format %s unstable or incomplete: %q", format, first)
		}
		for _, unsafe := range []string{"\x1b", "\u202e", "<script>"} {
			if strings.Contains(first, unsafe) {
				t.Fatalf("format %s retained unsafe %q", format, unsafe)
			}
		}
		for _, blank := range []string{"target: ;", "cause ;", "guardrails: ;", "risks: ;"} {
			if strings.Contains(first, blank) {
				t.Fatalf("format %s rendered missing fact as blank: %q", format, blank)
			}
		}
		for _, fact := range []string{"synthetic", "project", "complete", "pass", "quality", "0.5", "review", "derived", "1/2", "workload", "dimensionless magnitude", "redacted aggregate", "previous:", "high", "higher", "better", "smallest change"} {
			if !strings.Contains(first, fact) {
				t.Fatalf("format %s omitted semantic fact %q: %q", format, fact, first)
			}
		}
		if format == contracts.ReportFormatTable {
			for _, line := range strings.Split(first, "\n") {
				if len([]rune(line)) > 120 {
					t.Fatalf("table line too wide: %d %q", len([]rune(line)), line)
				}
			}
		}
	}
}

func TestRenderIsIndependentOfLocalTimeZone(t *testing.T) {
	result := fixture()
	original := time.Local
	t.Cleanup(func() { time.Local = original })
	time.Local = time.FixedZone("synthetic-west", -7*60*60)
	west, err := Build(result)
	if err != nil {
		t.Fatal(err)
	}
	time.Local = time.FixedZone("synthetic-east", 11*60*60)
	east, err := Build(result)
	if err != nil {
		t.Fatal(err)
	}
	if Render(west, contracts.ReportFormatMarkdown) != Render(east, contracts.ReportFormatMarkdown) {
		t.Fatal("local time zone changed report output")
	}
}

func TestRenderBoundedCountsOmissionsAcrossCompleteEnvelope(t *testing.T) {
	result := fixture()
	for i := 0; i < 500; i++ {
		result.Findings = append(result.Findings, contracts.AuditFinding{Code: strings.Repeat("x", 400), Classification: "warning", Confidence: "high"})
	}
	model, err := Build(result)
	if err != nil {
		t.Fatal(err)
	}
	base := contracts.RenderGovernanceReportResult{SchemaVersion: contracts.SchemaVersion, Kind: "result", Format: contracts.ReportFormatMarkdown, Coverage: result.Coverage, ProvenanceLabels: []contracts.ProvenanceLabel{contracts.ProvenanceDerived}}
	bounded, err := RenderBounded(model, contracts.ReportFormatMarkdown, base)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(bounded)
	if len(encoded) > MaxResultBytes || bounded.OmittedCount == 0 || len(bounded.SectionOmissions) == 0 {
		t.Fatalf("bytes=%d omissions=%#v", len(encoded), bounded.SectionOmissions)
	}
}

func TestBuildPreservesUnknownDistinctFromZeroAndCapsActions(t *testing.T) {
	result := fixture()
	zero := 0
	result.ReportFacts.ScopeCounts = contracts.ReportScopeCounts{IncludedTurns: 0, ExcludedTurns: &zero, ExcludedEvidence: nil}
	for i := 0; i < 7; i++ {
		code := fmt.Sprintf("action-%d", i)
		result.Recommendations = append(result.Recommendations, contracts.AuditRecommendation{Code: code, Action: "act", Verification: "verify"})
		result.ReportFacts.Recommendations = append(result.ReportFacts.Recommendations, contracts.ReportRecommendationFacts{Code: code})
	}
	model, err := Build(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Recommendations) != 5 || model.initialOmissions["recommendations"] != 3 {
		t.Fatalf("recommendations=%d omissions=%v", len(model.Recommendations), model.initialOmissions)
	}
	if model.Recommendations[0].Code != "review" || model.Recommendations[1].Code != "action-0" {
		t.Fatalf("upstream action priority changed: %#v", model.Recommendations)
	}
	rendered := Render(model, contracts.ReportFormatMarkdown)
	if !strings.Contains(rendered, "turns: included 0; excluded 0") || !strings.Contains(rendered, "evidence: included 0; excluded unknown") {
		t.Fatalf("zero/unknown collapsed: %s", rendered)
	}
}

func TestChatDoesNotDuplicateReportFamilies(t *testing.T) {
	result := fixture()
	model, err := Build(result)
	if err != nil {
		t.Fatal(err)
	}
	rendered := Render(model, contracts.ReportFormatChat)
	if strings.Count(rendered, "Governance report\n") != 1 {
		t.Fatalf("summary count=%d", strings.Count(rendered, "Governance report\n"))
	}
	for _, prefix := range []string{"Contribution:", "Guardrail:", "Governance overhead (excluded):"} {
		if strings.Count(rendered, prefix) != 1 {
			t.Fatalf("%s count=%d", prefix, strings.Count(rendered, prefix))
		}
	}
}

func TestBuildRejectsAmbiguousFactsAndTreatsNonFiniteAsUnknown(t *testing.T) {
	result := fixture()
	result.ReportFacts.Metrics = append(result.ReportFacts.Metrics, result.ReportFacts.Metrics[0])
	if _, err := Build(result); err == nil {
		t.Fatal("duplicate metric facts accepted")
	}
	result = fixture()
	result.Metrics[0].NativeValue = pointer(math.NaN())
	model, err := Build(result)
	if err != nil {
		t.Fatal(err)
	}
	if model.Metrics[0].Value != "unknown" {
		t.Fatalf("non-finite value=%q", model.Metrics[0].Value)
	}
}

func TestBuildSuppressesRareCohortContributionCounts(t *testing.T) {
	result := fixture()
	result.ReportFacts.PrivacySuppressed = true
	result.Contributions[0].AttributionState = "privacy_suppressed"
	model, err := Build(result)
	if err != nil {
		t.Fatal(err)
	}
	value := model.Contributions[0]
	if value.Value != "unknown" || value.Numerator != "unknown" || value.Denominator != "unknown" {
		t.Fatalf("suppressed contribution leaked raw counts: %#v", value)
	}
}

func FuzzRenderRejectsPresentationControls(f *testing.F) {
	f.Add("safe")
	f.Add("\x1b[31m<script>[x](javascript:alert(1))\u202e")
	f.Add("İ[x](JaVaScRiPt:alert(1))")
	f.Add("::code-comment{file=secret}")
	f.Add("[remote](https://example.invalid) mailto:private@example.invalid www.example.invalid")
	f.Fuzz(func(t *testing.T, value string) {
		result := fixture()
		result.ReportFacts.DisplayIdentity = value
		model, err := Build(result)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []contracts.ReportFormat{contracts.ReportFormatMarkdown, contracts.ReportFormatTable, contracts.ReportFormatChat} {
			rendered := Render(model, format)
			for _, forbidden := range []string{"\x1b", "\u202e", "<script>", "javascript:", "https:", "mailto:", "www.", "::code-comment"} {
				if strings.Contains(strings.ToLower(rendered), forbidden) {
					t.Fatalf("unsafe %q retained in %s", forbidden, format)
				}
			}
		}
	})
}

func fixture() contracts.AuditProjectResult {
	value := 0.5
	one, two := 1.0, 2.0
	window := contracts.ReportWindowReference{StartsAt: time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), AsOf: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Coverage: contracts.CoverageStateComplete, BaselineVersion: "baseline-v1", SourceVersions: []string{"source-v1"}}
	return contracts.AuditProjectResult{
		AuditReference: "00000000-0000-4000-8000-000000000001", Scope: contracts.AuditScopeProject,
		AsOf: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), RevisionHash: strings.Repeat("a", 64), Coverage: contracts.CoverageStateComplete,
		Metrics:            []contracts.MetricResult{{Name: "quality", NativeValue: &value, Numerator: &one, Denominator: &two, SampleCount: 2, PreviousValue: &value, RollingMedian: &value, NativeUnit: "ratio", Status: contracts.MetricStatusFlat, StatusReason: "within threshold", Coverage: contracts.CoverageStateComplete, Confidence: "high", Uncertainty: "low"}},
		Findings:           []contracts.AuditFinding{{Code: "synthetic\u202e<script>", Classification: "warning", Confidence: "high"}},
		Recommendations:    []contracts.AuditRecommendation{{Code: "review", Action: "Review safely", Verification: "Re-audit", LifecycleState: "active"}},
		Contributions:      []contracts.ScopeContribution{{Scope: contracts.AuditScopeProject, WorkloadClass: "workload", Numerator: 1, Denominator: 2, Contribution: &value, Coverage: contracts.CoverageStateComplete, AttributionState: "attributed"}},
		WorkloadEffects:    []contracts.WorkloadDecomposition{{Metric: "quality", VolumeEffect: &value, MixEffect: &value, WithinClassEffect: &value, Residual: &value, Status: "matched", Classes: []string{"workload"}}},
		Guardrails:         []contracts.GuardrailResult{{Name: "quality", State: "pass", Coverage: contracts.CoverageStateComplete}},
		GovernanceOverhead: []contracts.MetricResult{{Name: "overhead", NativeValue: &one, NativeUnit: "tokens", Coverage: contracts.CoverageStateComplete}},
		Outliers:           []contracts.NativeOutlier{{Metric: "quality", NativeValue: &value, NativeUnit: "ratio", Direction: "high", Magnitude: &two}},
		Confounders:        []contracts.ConfounderStratum{{Kind: "workload", State: "matched", Confidence: "high"}},
		InvocationCounts:   contracts.InvocationCounts{HostCalls: 1, LeafCalls: 1}, DerivationMethod: "normalized", OmittedCount: 1,
		Window:      contracts.AuditWindowProvenance{StartsAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), AsOf: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), BaselineVersion: "baseline-v1", CountingMode: "logical", WorkloadVersion: "workload-v1", SourceVersions: []string{"source-v1"}, Revision: 1},
		ReportFacts: &contracts.GovernanceReportFacts{Version: "report-facts-v1", DisplayIdentity: "synthetic\x1b[31m", PrivacySuppressed: false, PreviousWindow: &window, RollingWindows: []contracts.ReportWindowReference{window}, Sources: []contracts.ReportSourceFacts{{SourceKind: "synthetic", Version: "source-v1", Freshness: "current", Coverage: contracts.CoverageStateComplete, RedactionState: "complete", KnowledgeState: "observed"}}, QualityGateState: "pass", Metrics: []contracts.ReportMetricFacts{{Name: "quality", DisplayLabel: "quality", Polarity: "higher_better"}}, Recommendations: []contracts.ReportRecommendationFacts{{Code: "review", ViolatedContract: "quality", Scope: contracts.AuditScopeProject, EvidenceConfidence: "high", ExpectedQualityImpact: "improve quality", BroaderChangeRationale: "smallest change"}}, Outliers: []contracts.ReportOutlierFacts{{Metric: "quality", Scope: contracts.AuditScopeProject, Impact: "flat", Coverage: contracts.CoverageStateComplete, EvidenceWindow: window, RecommendationCode: "review"}}, Provenance: []contracts.ReportProvenanceFact{{Field: "quality", KnowledgeState: "observed", Provenance: "derived"}}},
	}
}

func pointer(value float64) *float64 { return &value }
