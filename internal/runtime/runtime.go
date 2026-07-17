// Package runtime normalizes observable execution intervals and modalities.
package runtime

import (
	"math"
	"sort"
	"time"
)

type Interval struct {
	Start time.Time
	End   time.Time
	Class string
}

func UnionDuration(intervals []Interval, included map[string]bool) time.Duration {
	selected := []Interval{}
	for _, interval := range intervals {
		if included[interval.Class] && interval.End.After(interval.Start) {
			selected = append(selected, interval)
		}
	}
	if len(selected) == 0 {
		return 0
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Start.Before(selected[j].Start) })
	start, end := selected[0].Start, selected[0].End
	var total time.Duration
	for _, interval := range selected[1:] {
		if interval.Start.After(end) {
			total += end.Sub(start)
			start, end = interval.Start, interval.End
			continue
		}
		if interval.End.After(end) {
			end = interval.End
		}
	}
	return total + end.Sub(start)
}

type Output struct {
	Modality string
	Bytes    uint64
	Tokens   *uint64
}

func TextTokens(outputs []Output) (uint64, bool) {
	var total uint64
	known := true
	for _, output := range outputs {
		if output.Modality != "text" && output.Modality != "context" {
			continue
		}
		if output.Tokens == nil {
			known = false
			continue
		}
		if math.MaxUint64-total < *output.Tokens {
			return 0, false
		}
		total += *output.Tokens
	}
	return total, known
}
