// Package baseline computes deterministic robust governance comparisons.
package baseline

import (
	"math"
	"sort"

	"github.com/nijanthan-dev/codex-prompt-better/internal/metrics"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const Version = "baseline-v1"

type Window struct {
	Value          *float64
	Coverage       contracts.CoverageState
	MetricVersion  string
	Comparable     bool
	GuardrailsPass bool
}

type Comparison struct {
	Previous           Window
	Rolling            []Window
	Seasonal           []Window
	WorkloadExpected   *float64
	ConfoundersMatched bool
	HysteresisActive   bool
	PersistenceCount   int
	PrivacySuppressed  bool
}

func Apply(current contracts.MetricResult, definition metrics.Definition,
	comparison Comparison,
) (contracts.MetricResult, error) {
	current.PracticalThreshold = definition.PracticalChange
	current.Status = contracts.MetricStatusInsufficient
	current.StatusReason = "comparison_unavailable"
	current.Confidence = "unknown"
	if current.NativeValue == nil || current.Coverage != contracts.CoverageStateComplete {
		current.StatusReason = "current_incomplete"
		return current, nil
	}
	if comparison.PrivacySuppressed {
		current.StatusReason = "privacy_suppressed"
		current.Uncertainty = "small_cohort"
		return current, nil
	}
	if !compatible(current, comparison.Previous) {
		current.StatusReason = "previous_incompatible"
		return current, nil
	}
	rolling := compatibleValues(current, comparison.Rolling)
	if len(rolling) < 3 {
		seasonal := compatibleValues(current, comparison.Seasonal)
		if len(seasonal) < 3 {
			current.StatusReason = "baseline_sample_not_met"
			current.BaselineSampleCount = len(rolling)
			return current, nil
		}
		rolling = seasonal
		current.StatusReason = "seasonal_last_n_fallback"
	}
	median := Median(rolling)
	deviations := make([]float64, len(rolling))
	for index, value := range rolling {
		deviations[index] = math.Abs(value - median)
	}
	mad := Median(deviations)
	current.PreviousValue = clone(comparison.Previous.Value)
	current.RollingMedian = &median
	current.RollingMAD = &mad
	current.BaselineSampleCount = len(rolling)
	current.AbsoluteChangePrevious, current.PercentageChangePrevious =
		change(*current.NativeValue, *comparison.Previous.Value)
	current.AbsoluteChangeRolling, current.PercentageChangeRolling =
		change(*current.NativeValue, median)
	if comparison.WorkloadExpected != nil {
		residual := *current.NativeValue - *comparison.WorkloadExpected
		current.WorkloadAdjustedResidual = &residual
	}
	if !comparison.Previous.GuardrailsPass {
		current.StatusReason = "guardrail_regressed_or_unknown"
		return current, nil
	}
	if !comparison.ConfoundersMatched {
		current.Status = contracts.MetricStatusMixed
		current.StatusReason = "confounder_unmatched"
		current.Confidence = "low"
		return current, nil
	}
	effectPrevious := relativeEffect(*current.NativeValue, *comparison.Previous.Value)
	effectRolling := relativeEffect(*current.NativeValue, median)
	noise := definition.PracticalChange
	if median != 0 && mad/median > noise {
		noise = mad / median
	}
	if math.Abs(effectPrevious) <= noise && math.Abs(effectRolling) <= noise {
		current.Status = contracts.MetricStatusFlat
		current.StatusReason = "within_practical_or_noise_threshold"
		current.Confidence = confidence(len(rolling), mad, median)
		return current, nil
	}
	if signsConflict(effectPrevious, effectRolling) || definition.Polarity == metrics.PolarityContextual {
		current.Status = contracts.MetricStatusMixed
		current.StatusReason = "contextual_or_baselines_conflict"
		current.Confidence = confidence(len(rolling), mad, median)
		return current, nil
	}
	if comparison.HysteresisActive || comparison.PersistenceCount < definition.MinimumPersistence {
		current.Status = contracts.MetricStatusFlat
		current.StatusReason = "hysteresis_or_persistence"
		current.Confidence = "medium"
		return current, nil
	}
	improved := effectRolling > 0
	if definition.Polarity == metrics.PolarityLowerBetter {
		improved = effectRolling < 0
	}
	if improved {
		current.Status = contracts.MetricStatusImproved
		current.StatusReason = "meaningful_matched_change"
	} else {
		current.Status = contracts.MetricStatusWorsened
		current.StatusReason = "meaningful_matched_change"
	}
	current.Confidence = confidence(len(rolling), mad, median)
	return current, nil
}

func Median(values []float64) float64 {
	copyValues := append([]float64{}, values...)
	sort.Float64s(copyValues)
	middle := len(copyValues) / 2
	if len(copyValues)%2 == 0 {
		return (copyValues[middle-1] + copyValues[middle]) / 2
	}
	return copyValues[middle]
}

func compatible(current contracts.MetricResult, window Window) bool {
	return window.Value != nil && window.Coverage == contracts.CoverageStateComplete &&
		window.MetricVersion == current.Version && window.Comparable
}

func compatibleValues(current contracts.MetricResult, windows []Window) []float64 {
	values := []float64{}
	for _, window := range windows {
		if compatible(current, window) {
			values = append(values, *window.Value)
		}
	}
	return values
}

func change(current, baseline float64) (*float64, *float64) {
	absolute := current - baseline
	if baseline == 0 {
		return &absolute, nil
	}
	percentage := absolute / baseline
	return &absolute, &percentage
}

func relativeEffect(current, baseline float64) float64 {
	if baseline == 0 {
		if current == 0 {
			return 0
		}
		return math.Copysign(math.Inf(1), current)
	}
	return (current - baseline) / baseline
}

func signsConflict(first, second float64) bool {
	return first != 0 && second != 0 && math.Signbit(first) != math.Signbit(second)
}

func confidence(sample int, mad, median float64) string {
	if sample >= 7 && (median == 0 || mad/math.Abs(median) <= 0.10) {
		return "high"
	}
	if sample >= 3 {
		return "medium"
	}
	return "low"
}

func clone(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
