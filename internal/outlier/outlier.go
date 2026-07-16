// Package outlier ranks metric deviations without combining unlike units.
package outlier

import (
	"math"
	"sort"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func Rank(metrics []contracts.MetricResult) []contracts.NativeOutlier {
	result := []contracts.NativeOutlier{}
	for _, metric := range metrics {
		if metric.NativeValue == nil || metric.RollingMedian == nil ||
			metric.RollingMAD == nil || *metric.RollingMAD <= 0 {
			continue
		}
		magnitude := math.Abs(*metric.NativeValue-*metric.RollingMedian) / *metric.RollingMAD
		direction := "higher"
		if *metric.NativeValue < *metric.RollingMedian {
			direction = "lower"
		}
		nativeValue := *metric.NativeValue
		result = append(result, contracts.NativeOutlier{
			Metric: metric.Name, NativeValue: &nativeValue,
			NativeUnit: metric.NativeUnit, Direction: direction, Magnitude: &magnitude,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if *result[i].Magnitude == *result[j].Magnitude {
			return result[i].Metric < result[j].Metric
		}
		return *result[i].Magnitude > *result[j].Magnitude
	})
	if len(result) > 10 {
		result = result[:10]
	}
	return result
}
