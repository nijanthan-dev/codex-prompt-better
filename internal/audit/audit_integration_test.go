package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/baseline"
	"github.com/nijanthan-dev/codex-prompt-better/internal/eval"
	"github.com/nijanthan-dev/codex-prompt-better/internal/metrics"
	"github.com/nijanthan-dev/codex-prompt-better/internal/replay"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

type syntheticSource struct {
	snapshot Snapshot
}

func TestBoundResultBoundsRepeatedEvidenceReferences(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC)
	refs := make([]string, 100)
	for index := range refs {
		refs[index] = fmt.Sprintf("%08d-0000-4000-8000-000000000001", index)
	}
	request := contracts.AuditProjectRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request",
		Scope: contracts.AuditScopeProject, Reference: "00000000-0000-4000-8000-000000000001",
		ConfiguredSources: []string{"synthetic"},
		StartsAt:          at.Add(-24 * time.Hour), EndsAt: at, AsOf: at,
		Consent: contracts.AuditConsentGranted,
	}
	result := runSynthetic(t, request, Snapshot{
		Reference: request.Reference, Scope: request.Scope,
		StartsAt: request.StartsAt, EndsAt: request.EndsAt, AsOf: request.AsOf,
		Coverage: contracts.CoverageStateComplete,
		Metrics: metrics.Input{
			CompletedTurns: 10, AttributedTurns: 5, ToolCalls: 10,
			PassiveWaitCalls: 5, EvidenceRefs: refs,
			Coverage: contracts.CoverageStateComplete,
		},
		GovernanceOverhead: metrics.Input{Coverage: contracts.CoverageStateComplete},
		Comparisons:        map[string]baseline.Comparison{},
		Guardrails:         passingGuardrails(),
	})
	bounded, err := BoundResult(result)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxResultBytes {
		t.Fatalf("bounded result bytes=%d limit=%d", len(encoded), MaxResultBytes)
	}
	if bounded.OmittedCount == 0 {
		t.Fatal("bounded result did not report omissions")
	}
	if len(bounded.Metrics) != len(metrics.Definitions()) {
		t.Fatalf("bounded result dropped metrics: got=%d want=%d", len(bounded.Metrics), len(metrics.Definitions()))
	}
	if len(result.Metrics[0].EvidenceRefs) != len(refs) {
		t.Fatalf("bounding mutated canonical result: %#v", result.Metrics[0].EvidenceRefs)
	}
	if len(bounded.Findings) == 0 || len(bounded.Findings[0].EvidenceRefs) != len(refs) {
		t.Fatalf("bounded result changed finding evidence identity: %#v", bounded.Findings)
	}
	for _, recommendation := range bounded.Recommendations {
		if recommendation.Code != "no_action" && len(recommendation.EvidenceRefs) != len(refs) {
			t.Fatalf("bounded result changed recommendation evidence identity: %#v", recommendation)
		}
	}
}

func TestBoundResultReturnsStableBudgetError(t *testing.T) {
	t.Parallel()
	_, err := boundResult(contracts.AuditProjectResult{}, 1)
	var stable *contracts.StableError
	if !errors.As(err, &stable) {
		t.Fatalf("bound result error=%T want stable error", err)
	}
	if stable.Code != contracts.ErrorCodeBudgetExhausted ||
		stable.FieldPath == nil || *stable.FieldPath != "result" || stable.Retryable {
		t.Fatalf("bound result error=%#v", stable)
	}
}

func (source syntheticSource) Snapshot(_ context.Context, _ contracts.AuditProjectRequest) (Snapshot, error) {
	return source.snapshot, nil
}

func TestAuditReplayEvaluation_Integration(t *testing.T) {
	at := time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC)
	request := contracts.AuditProjectRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request",
		Scope: contracts.AuditScopeProject, Reference: "00000000-0000-0000-0000-000000000001",
		ConfiguredSources: []string{"synthetic"},
		StartsAt:          at.Add(-24 * time.Hour), EndsAt: at, AsOf: at,
		Consent: contracts.AuditConsentGranted,
	}
	baseline := runSynthetic(t, request, Snapshot{
		Reference: request.Reference, Scope: contracts.AuditScopeProject,
		StartsAt: request.StartsAt, EndsAt: request.EndsAt, AsOf: request.AsOf,
		Coverage: contracts.CoverageStateComplete,
		Metrics: metrics.Input{
			CompletedTurns: 10, TotalTokens: 1000, NonCachedInputTokens: 600,
			ToolCalls: 20, PassiveWaitCalls: 4, ToolResults: 20, OversizedResults: 2,
			RepeatedCalls: 4, CommentaryMessages: 5, WallClockSeconds: 100,
			ActiveSeconds: 70, ActiveRuntimeKnown: true,
			Coverage:     contracts.CoverageStateComplete,
			EvidenceRefs: []string{"baseline-evidence"},
		},
		GovernanceOverhead: metrics.Input{
			CompletedTurns: 1, TotalTokens: 10, ToolCalls: 1, ToolResults: 1,
			Coverage: contracts.CoverageStateComplete,
		},
		PassivePollingRefs: []string{"baseline-poll"},
		RepeatedCallRefs:   []string{"baseline-repeat"},
		Comparisons:        comparisons(100, 60, 20, 10, 20, 5, 10, 7),
		Guardrails:         passingGuardrails(),
	})
	candidateSnapshot := Snapshot{
		Reference: request.Reference, Scope: contracts.AuditScopeProject,
		StartsAt: request.StartsAt, EndsAt: request.EndsAt, AsOf: request.AsOf,
		Coverage: contracts.CoverageStateComplete,
		Metrics: metrics.Input{
			CompletedTurns: 10, TotalTokens: 1000, NonCachedInputTokens: 500,
			ToolCalls: 20, PassiveWaitCalls: 1, ToolResults: 20, OversizedResults: 1,
			RepeatedCalls: 1, CommentaryMessages: 5, WallClockSeconds: 100,
			ActiveSeconds: 70, ActiveRuntimeKnown: true,
			Coverage:     contracts.CoverageStateComplete,
			EvidenceRefs: []string{"candidate-evidence"},
		},
		GovernanceOverhead: metrics.Input{
			CompletedTurns: 1, TotalTokens: 10, ToolCalls: 1, ToolResults: 1,
			Coverage: contracts.CoverageStateComplete,
		},
		Comparisons: comparisons(100, 60, 20, 10, 20, 5, 10, 7),
		Guardrails:  passingGuardrails(),
	}
	candidate := runSynthetic(t, request, candidateSnapshot)
	repeated := runSynthetic(t, request, candidateSnapshot)
	if candidate.RevisionHash != repeated.RevisionHash {
		t.Fatalf("replay is not deterministic: %q != %q", candidate.RevisionHash, repeated.RevisionHash)
	}
	comparison, err := replay.Compare(baseline, candidate)
	if err != nil || comparison.Outcome != "mixed" || comparison.QualityGateState != "pass" {
		t.Fatalf("comparison=%#v error=%v", comparison, err)
	}
	run, err := eval.NewRun("eval-v1", candidateSnapshot,
		map[string]string{"mode": "synthetic"}, eval.Components{
			Model: "synthetic-model", Compiler: "compiler-v1",
			Policy: "policy-v1", Metrics: metrics.Definitions(),
		}, comparison)
	if err != nil || run.Decision != "hold" || len(run.RunHash) != 64 {
		t.Fatalf("evaluation=%#v error=%v", run, err)
	}
	if len(candidate.Recommendations) != 1 || candidate.Recommendations[0].Code != "no_action" {
		t.Fatalf("unexpected recommendations: %#v", candidate.Recommendations)
	}
	if len(candidate.GovernanceOverhead) == 0 {
		t.Fatal("governance overhead not reported separately")
	}
	if candidate.ReportFacts == nil || candidate.ReportFacts.Version != "report-facts-v1" ||
		candidate.ReportFacts.DisplayIdentity != request.Reference ||
		candidate.ReportFacts.QualityGateState != "pass" ||
		len(candidate.ReportFacts.Metrics) != len(candidate.Metrics) ||
		len(candidate.ReportFacts.RollingWindows) != 7 ||
		candidate.ReportFacts.ScopeCounts.IncludedTurns != 10 {
		t.Fatalf("report handoff incomplete: %#v", candidate.ReportFacts)
	}
	if candidate.ReportFacts.ScopeCounts.ExcludedTrajectories != nil ||
		candidate.ReportFacts.ScopeCounts.ExcludedTurns != nil ||
		candidate.ReportFacts.ScopeCounts.ExcludedToolCalls != nil ||
		candidate.ReportFacts.ScopeCounts.ExcludedEvidence != nil {
		t.Fatalf("unknown exclusions were fabricated: %#v", candidate.ReportFacts.ScopeCounts)
	}
	if repeated.ReportFacts == nil || candidate.RevisionHash != repeated.RevisionHash {
		t.Fatalf("report facts changed replay identity: %#v %#v", candidate.ReportFacts, repeated.ReportFacts)
	}
}

func TestValidateRequestRejectsNonUUIDAndPortfolioAlias(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	request := contracts.AuditProjectRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request",
		Scope: contracts.AuditScopeProject, Reference: "not-a-uuid",
		ConfiguredSources: []string{"synthetic"},
		StartsAt:          at.Add(-time.Hour), EndsAt: at, AsOf: at,
		Consent: contracts.AuditConsentGranted,
	}
	if err := ValidateRequest(request); err == nil {
		t.Fatal("non-UUID project reference accepted")
	}
	request.Scope = contracts.AuditScopePortfolio
	request.Reference = "everything"
	if err := ValidateRequest(request); err == nil {
		t.Fatal("non-canonical portfolio reference accepted")
	}
}

func passingGuardrails() []contracts.GuardrailResult {
	return []contracts.GuardrailResult{
		{Name: "completion", State: "pass", Coverage: contracts.CoverageStateComplete},
		{Name: "privacy", State: "pass", Coverage: contracts.CoverageStateComplete},
		{Name: "validation", State: "pass", Coverage: contracts.CoverageStateComplete},
	}
}

func comparisons(values ...float64) map[string]baseline.Comparison {
	names := []string{
		"tokens_per_turn", "non_cached_input_per_turn",
		"passive_waits_per_100_tool_calls", "oversized_outputs_per_100_tool_results",
		"repeated_calls_per_100_tool_calls", "commentary_per_turn",
		"wall_clock_runtime_per_turn", "active_runtime_per_turn",
	}
	result := map[string]baseline.Comparison{}
	for index, name := range names {
		value := values[index]
		previous := baseline.Window{
			Value: &value, Coverage: contracts.CoverageStateComplete,
			MetricVersion: metrics.DefinitionVersion, Comparable: true, GuardrailsPass: true,
		}
		rolling := []baseline.Window{}
		for _, offset := range []float64{-0.01, 0, 0.01} {
			item := value * (1 + offset)
			rolling = append(rolling, baseline.Window{
				Value: &item, Coverage: contracts.CoverageStateComplete,
				MetricVersion: metrics.DefinitionVersion, Comparable: true,
			})
		}
		result[name] = baseline.Comparison{
			Previous: previous, Rolling: rolling, ConfoundersMatched: true,
			PersistenceCount: 2,
		}
	}
	return result
}

func runSynthetic(t *testing.T, request contracts.AuditProjectRequest, snapshot Snapshot) contracts.AuditProjectResult {
	t.Helper()
	engine, err := New(syntheticSource{snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
