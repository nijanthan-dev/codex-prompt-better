// Package workload performs unit-safe workload classification and decomposition.
package workload

import (
	"math"
	"sort"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const Version = "workload-v1"

type ClassValue struct {
	Class       string
	Count       float64
	Numerator   float64
	Denominator float64
}

// Decompose separates total volume, class mix, and within-class rate effects.
// The identity is exact up to floating-point rounding.
func Decompose(metric string, previous, current []ClassValue) contracts.WorkloadDecomposition {
	classes := classUnion(previous, current)
	if len(classes) == 0 || !validInputs(previous) || !validInputs(current) {
		return contracts.WorkloadDecomposition{Metric: metric, Status: "insufficient", Classes: []string{}}
	}
	prev := index(previous)
	curr := index(current)
	prevCount, currCount := totalCount(previous), totalCount(current)
	if prevCount <= 0 || currCount <= 0 {
		return contracts.WorkloadDecomposition{Metric: metric, Status: "insufficient", Classes: classes}
	}
	prevTotal, currTotal := totalNumerator(previous), totalNumerator(current)
	prevRate := prevTotal / prevCount
	volume := (currCount - prevCount) * prevRate
	mix, within := 0.0, 0.0
	for _, class := range classes {
		p := prev[class]
		c := curr[class]
		pShare := p.Count / prevCount
		cShare := c.Count / currCount
		pRate := safeRate(p)
		cRate := safeRate(c)
		mix += currCount * (cShare - pShare) * pRate
		within += currCount * cShare * (cRate - pRate)
	}
	change := currTotal - prevTotal
	residual := change - volume - mix - within
	status := "decomposed"
	if aggregateDirection(previous, current) != withinDirection(prev, curr, classes) {
		status = "simpson_mixed"
	}
	return contracts.WorkloadDecomposition{
		Metric: metric, VolumeEffect: pointer(volume), MixEffect: pointer(mix),
		WithinClassEffect: pointer(within), Residual: pointer(residual),
		Status: status, Classes: classes,
	}
}

func validInputs(values []ClassValue) bool {
	for _, value := range values {
		if value.Class == "" || !finite(value.Count) || !finite(value.Numerator) ||
			!finite(value.Denominator) || value.Count < 0 || value.Numerator < 0 ||
			value.Denominator < 0 || (value.Numerator > 0 && value.Denominator == 0) {
			return false
		}
	}
	return true
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func classUnion(groups ...[]ClassValue) []string {
	seen := map[string]bool{}
	for _, group := range groups {
		for _, value := range group {
			if value.Class != "" {
				seen[value.Class] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for class := range seen {
		result = append(result, class)
	}
	sort.Strings(result)
	return result
}

func index(values []ClassValue) map[string]ClassValue {
	result := map[string]ClassValue{}
	for _, value := range values {
		current := result[value.Class]
		current.Class = value.Class
		current.Count += value.Count
		current.Numerator += value.Numerator
		current.Denominator += value.Denominator
		result[value.Class] = current
	}
	return result
}

func totalCount(values []ClassValue) float64 {
	total := 0.0
	for _, value := range values {
		total += value.Count
	}
	return total
}

func totalNumerator(values []ClassValue) float64 {
	total := 0.0
	for _, value := range values {
		total += value.Numerator
	}
	return total
}

func safeRate(value ClassValue) float64 {
	if value.Denominator <= 0 {
		return 0
	}
	return value.Numerator / value.Denominator
}

func aggregateDirection(previous, current []ClassValue) int {
	return direction(totalNumerator(current)/totalCount(current) - totalNumerator(previous)/totalCount(previous))
}

func withinDirection(previous, current map[string]ClassValue, classes []string) int {
	value := 0.0
	for _, class := range classes {
		value += safeRate(current[class]) - safeRate(previous[class])
	}
	return direction(value)
}

func direction(value float64) int {
	if math.Abs(value) < 1e-12 {
		return 0
	}
	if value < 0 {
		return -1
	}
	return 1
}

func pointer(value float64) *float64 { return &value }
