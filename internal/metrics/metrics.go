// Package metrics calculates transparent governance metrics from normalized data.
package metrics

import (
	"errors"
	"sort"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const DefinitionVersion = "metric-v1"

type Polarity string

const (
	PolarityContextual   Polarity = "contextual"
	PolarityLowerBetter  Polarity = "lower_better"
	PolarityHigherBetter Polarity = "higher_better"
	PolarityGuardrail    Polarity = "guardrail"
)

type Definition struct {
	Name               string
	DisplayLabel       string
	Version            string
	Intent             string
	Formula            string
	Numerator          string
	Denominator        string
	NativeUnit         string
	Polarity           Polarity
	MinimumSample      int
	MinimumCoverage    float64
	PracticalChange    float64
	Hysteresis         float64
	MinimumPersistence int
	CooldownWindows    int
	EvidenceRequired   []string
	Exclusions         []string
	Confounders        []string
	AllowedDiagnoses   []string
	Guardrails         []string
	Limitations        []string
	MisuseRisk         string
}

type Input struct {
	CompletedTurns       int
	AttributedTurns      int
	BoundaryDecisions    int
	BoundaryViolations   int
	Checkpoints          int
	CompleteCheckpoints  int
	TotalTokens          float64
	NonCachedInputTokens float64
	ToolCalls            int
	PassiveWaitCalls     int
	ToolResults          int
	OversizedResults     int
	RepeatedCalls        int
	ToolErrors           int
	ObservableMutations  int
	ValidatedMutations   int
	AcceptedEvidence     int
	RedactedEvidence     int
	CommentaryMessages   int
	WallClockSeconds     float64
	ActiveSeconds        float64
	ActiveRuntimeKnown   bool
	Coverage             contracts.CoverageState
	EvidenceRefs         []string
	Exclusions           []string
}

var definitions = registry()

func registry() []Definition {
	quality := []string{"completion_coverage", "validation_presence", "permission_boundaries", "privacy_coverage"}
	return []Definition{
		newDefinition("scope_attribution_coverage", "Scope attribution coverage", "Measure completed turns with explicit task/project attribution.", "attributed completed turns / completed turns", "attributed completed turns", "completed turns", "ratio", PolarityHigherBetter, 1, 0.02, []string{"turns", "tasks", "sessions"}, []string{"privacy_suppressed_small_cohorts"}, []string{"adapter_coverage"}, []string{"scope_attribution_gap"}, quality, []string{"unknown_attribution_is_not_failure"}, "Must not infer ownership or rank people."),
		newDefinition("boundary_violation_rate", "Boundary violation rate", "Measure observed denied or violated boundary decisions.", "denied or violated boundaries / observed boundary decisions", "denied or violated boundaries", "observed boundary decisions", "ratio", PolarityLowerBetter, 1, 0.01, []string{"boundaries"}, []string{"unknown_boundary_outcome"}, []string{"policy_version", "risk_class"}, []string{"boundary_violation"}, quality, []string{"host_boundary_remains_authoritative"}, "Never converts a metric into permission."),
		newDefinition("checkpoint_completeness", "Checkpoint completeness", "Measure structured checkpoints containing completion, next action, and gate state.", "complete checkpoints / observed checkpoints", "complete checkpoints", "observed checkpoints", "ratio", PolarityHigherBetter, 1, 0.05, []string{"checkpoints"}, []string{"read_only_sessions_without_checkpoint_requirement"}, []string{"workflow_phase"}, []string{"checkpoint_gap"}, quality, []string{"counts_do_not_score_prose_quality"}, "Checkpoint volume is not a quality score."),
		newDefinition("tokens_per_turn", "Tokens per turn", "Show native token workload per completed turn.", "valid total-token deltas / completed turns", "valid total-token deltas", "completed turns", "tokens/turn", PolarityContextual, 1, 0.05, []string{"usage_observations", "turns"}, []string{"incomplete_turns"}, []string{"model", "tokenizer", "workload_class"}, nil, quality, []string{"contextual_not_quality"}, "Must not trigger model or effort downgrade."),
		newDefinition("non_cached_input_per_turn", "Non-cached input per turn", "Measure native non-cached input per completed turn.", "valid non-cached input deltas / completed turns", "valid non-cached input deltas", "completed turns", "tokens/turn", PolarityLowerBetter, 1, 0.05, []string{"usage_observations", "cache_observations", "turns"}, []string{"unknown_cache_accounting"}, []string{"model", "tool_schema", "instruction", "memory", "media"}, []string{"cache_churn", "context_growth"}, quality, []string{"quality_guardrail_required"}, "API and subscription accounting must remain separate."),
		newDefinition("passive_waits_per_100_tool_calls", "Passive waits per 100 tool calls", "Detect avoidable passive polling while preserving required waits.", "classified passive waits / valid logical tool calls * 100", "classified passive waits", "valid logical tool calls", "calls/100_tool_calls", PolarityLowerBetter, 5, 0.10, []string{"tool_calls", "state_epochs", "stop_events"}, []string{"required_external_waits", "user_requested_cadence"}, []string{"external_wait", "approval_state"}, []string{"passive_polling"}, quality, []string{"classification_may_be_unknown"}, "User-requested polling cadence is never waste."),
		newDefinition("oversized_outputs_per_100_tool_results", "Oversized outputs per 100 tool results", "Identify oversized text results without treating binary size as text.", "text results above versioned threshold / valid text tool results * 100", "oversized text tool results", "valid text tool results", "results/100_tool_results", PolarityLowerBetter, 5, 0.10, []string{"items", "tool_calls"}, []string{"binary_media", "necessary_complete_output"}, []string{"output_modality", "workload_class"}, []string{"oversized_output"}, quality, []string{"threshold_is_versioned"}, "Required evidence must not be truncated."),
		newDefinition("repeated_calls_per_100_tool_calls", "Repeated calls per 100 tool calls", "Measure unchanged repeated logical calls within one state epoch.", "repeat candidates / valid logical tool calls * 100", "same canonical call in unchanged state epoch", "valid logical tool calls", "calls/100_tool_calls", PolarityLowerBetter, 5, 0.10, []string{"tool_calls", "state_epochs"}, []string{"state_changed", "retry_after_changed_arguments", "final_gate_validation"}, []string{"repository_state", "review_state", "external_version"}, []string{"repeated_unchanged_call"}, quality, []string{"canonicalization_is_redacted"}, "Post-mutation or final-gate validation is not a duplicate."),
		newDefinition("commentary_per_turn", "Commentary per turn", "Show user-visible coordination volume.", "commentary items / completed turns", "user-visible commentary items", "completed turns", "messages/turn", PolarityContextual, 1, 0.10, []string{"items", "turns"}, nil, []string{"workload_class", "user_update_requirement"}, nil, quality, []string{"never_automatically_waste"}, "Necessary communication must not be suppressed."),
		newDefinition("wall_clock_runtime_per_turn", "Wall-clock runtime per turn", "Show elapsed runtime per completed turn.", "unioned elapsed duration / completed turns", "unioned elapsed response duration", "completed turns", "seconds/turn", PolarityContextual, 1, 0.10, []string{"responses", "turns"}, []string{"incomplete_turns"}, []string{"host_load", "external_service", "workload_class"}, nil, quality, []string{"host_load_is_a_confounder"}, "Latency alone does not imply poor quality."),
		newDefinition("active_runtime_per_turn", "Active runtime per turn", "Show elapsed runtime excluding observable passive blocking.", "unioned active intervals / completed turns", "unioned active intervals", "completed turns", "seconds/turn", PolarityContextual, 1, 0.10, []string{"phases", "tool_calls", "turns"}, []string{"external_blocking", "approval_wait", "user_wait"}, []string{"host_load", "parallelism", "workload_class"}, nil, quality, []string{"unobservable_waits_remain_unknown"}, "Parallel intervals must be unioned, not summed."),
		newDefinition("tool_error_rate", "Tool error rate", "Guard against efficiency gains caused by failed tool work.", "failed logical calls / valid logical calls", "failed logical calls", "valid logical calls", "ratio", PolarityGuardrail, 5, 0.02, []string{"tool_calls"}, nil, []string{"tool_family", "external_service"}, []string{"tool_reliability"}, nil, []string{"unknown_outcomes_block_promotion"}, "Errors must not be hidden to improve resource metrics."),
		newDefinition("validation_presence", "Required validation presence", "Guard that mutations receive structured validation.", "validated mutations / observable mutations", "validated mutations", "observable mutations", "ratio", PolarityGuardrail, 1, 0.01, []string{"state_epochs", "tool_calls", "checkpoints"}, nil, []string{"mutation_kind"}, []string{"missing_validation"}, nil, []string{"structured_events_only"}, "Required final/security/release validation is necessary work."),
		newDefinition("privacy_redaction_coverage", "Privacy redaction coverage", "Guard that accepted evidence satisfies redaction requirements.", "redacted accepted evidence / accepted evidence", "redacted accepted evidence", "accepted evidence", "ratio", PolarityGuardrail, 1, 0.01, []string{"evidence_artifacts"}, nil, []string{"source_kind"}, []string{"privacy_gap"}, nil, []string{"suppressed_small_cohorts"}, "Privacy suppression must return unknown, never a fabricated pass."),
	}
}

func newDefinition(name, label, intent, formula, numerator, denominator, unit string,
	polarity Polarity, minimum int, practical float64, evidence, exclusions,
	confounders, diagnoses, guardrails, limitations []string, misuse string,
) Definition {
	return Definition{
		Name: name, DisplayLabel: label, Version: DefinitionVersion, Intent: intent,
		Formula: formula, Numerator: numerator, Denominator: denominator,
		NativeUnit: unit, Polarity: polarity, MinimumSample: minimum,
		MinimumCoverage: 0.90, PracticalChange: practical, Hysteresis: practical / 2,
		MinimumPersistence: 2, CooldownWindows: 3,
		EvidenceRequired: evidence, Exclusions: exclusions, Confounders: confounders,
		AllowedDiagnoses: diagnoses, Guardrails: guardrails, Limitations: limitations,
		MisuseRisk: misuse,
	}
}

func Definitions() []Definition {
	result := append([]Definition{}, definitions...)
	for index := range result {
		result[index].EvidenceRequired = append([]string{}, result[index].EvidenceRequired...)
		result[index].Exclusions = append([]string{}, result[index].Exclusions...)
		result[index].Confounders = append([]string{}, result[index].Confounders...)
		result[index].AllowedDiagnoses = append([]string{}, result[index].AllowedDiagnoses...)
		result[index].Guardrails = append([]string{}, result[index].Guardrails...)
		result[index].Limitations = append([]string{}, result[index].Limitations...)
	}
	return result
}

func Compute(input Input) ([]contracts.MetricResult, error) {
	if hasNegativeCount(input) {
		return nil, errors.New("metric counts must not be negative")
	}
	results := []contracts.MetricResult{
		ratio("scope_attribution_coverage", float64(input.AttributedTurns), input.CompletedTurns, input),
		ratio("boundary_violation_rate", float64(input.BoundaryViolations), input.BoundaryDecisions, input),
		ratio("checkpoint_completeness", float64(input.CompleteCheckpoints), input.Checkpoints, input),
		ratio("tokens_per_turn", input.TotalTokens, input.CompletedTurns, input),
		ratio("non_cached_input_per_turn", input.NonCachedInputTokens, input.CompletedTurns, input),
		perHundred("passive_waits_per_100_tool_calls", input.PassiveWaitCalls, input.ToolCalls, input),
		perHundred("oversized_outputs_per_100_tool_results", input.OversizedResults, input.ToolResults, input),
		perHundred("repeated_calls_per_100_tool_calls", input.RepeatedCalls, input.ToolCalls, input),
		ratio("tool_error_rate", float64(input.ToolErrors), input.ToolCalls, input),
		ratio("validation_presence", float64(input.ValidatedMutations), input.ObservableMutations, input),
		ratio("privacy_redaction_coverage", float64(input.RedactedEvidence), input.AcceptedEvidence, input),
		ratio("commentary_per_turn", float64(input.CommentaryMessages), input.CompletedTurns, input),
		ratio("wall_clock_runtime_per_turn", input.WallClockSeconds, input.CompletedTurns, input),
		ratio("active_runtime_per_turn", input.ActiveSeconds, input.CompletedTurns, input),
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results, nil
}

func hasNegativeCount(input Input) bool {
	return input.CompletedTurns < 0 || input.AttributedTurns < 0 ||
		input.BoundaryDecisions < 0 || input.BoundaryViolations < 0 ||
		input.Checkpoints < 0 || input.CompleteCheckpoints < 0 ||
		input.ToolCalls < 0 || input.ToolResults < 0 ||
		input.PassiveWaitCalls < 0 || input.OversizedResults < 0 ||
		input.RepeatedCalls < 0 || input.ToolErrors < 0 ||
		input.ObservableMutations < 0 || input.ValidatedMutations < 0 ||
		input.AcceptedEvidence < 0 || input.RedactedEvidence < 0 ||
		input.CommentaryMessages < 0
}

func perHundred(name string, numerator, denominator int, input Input) contracts.MetricResult {
	result := ratio(name, float64(numerator), denominator, input)
	if result.NativeValue != nil {
		value := *result.NativeValue * 100
		result.NativeValue = &value
	}
	return result
}

func ratio(name string, numerator float64, denominator int, input Input) contracts.MetricResult {
	definition := definition(name)
	result := contracts.MetricResult{
		Name: name, Version: DefinitionVersion, NativeUnit: definition.NativeUnit,
		SampleCount: denominator, Coverage: input.Coverage,
		Status: contracts.MetricStatusInsufficient, Uncertainty: "none",
		StatusReason: "comparison_unavailable", Confidence: "unknown",
		PracticalThreshold: definition.PracticalChange,
		Exclusions:         append(append([]string{}, definition.Exclusions...), input.Exclusions...),
		EvidenceRefs:       append([]string{}, input.EvidenceRefs...),
	}
	if input.Coverage != contracts.CoverageStateComplete {
		result.Uncertainty = "coverage_incomplete"
		return result
	}
	if name == "active_runtime_per_turn" && !input.ActiveRuntimeKnown {
		result.Uncertainty = "active_runtime_unavailable"
		return result
	}
	if denominator < definition.MinimumSample {
		result.Uncertainty = "minimum_sample_not_met"
		return result
	}
	value := numerator / float64(denominator)
	denominatorValue := float64(denominator)
	result.NativeValue = &value
	result.Numerator = &numerator
	result.Denominator = &denominatorValue
	result.Status = contracts.MetricStatusFlat
	return result
}

func definition(name string) Definition {
	for _, item := range definitions {
		if item.Name == name {
			return item
		}
	}
	panic("unknown metric definition")
}
