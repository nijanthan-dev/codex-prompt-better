// Package normalize provides deterministic cross-source normalization.
package normalize

import (
	"sort"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
)

type Assertion struct {
	Field          string
	Value          string
	SourceKind     string
	SourceSequence uint64
	MonotonicNanos int64
	ObservedAt     time.Time
	Precedence     int
}

type Resolution struct {
	Field             string
	Value             string
	WinningSource     string
	Conflicts         []Assertion
	OrderingUncertain bool
}

func Resolve(assertions []Assertion) Resolution {
	ordered := append([]Assertion{}, assertions...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Precedence != ordered[j].Precedence {
			return ordered[i].Precedence > ordered[j].Precedence
		}
		if ordered[i].SourceSequence != ordered[j].SourceSequence {
			return ordered[i].SourceSequence > ordered[j].SourceSequence
		}
		if ordered[i].MonotonicNanos != ordered[j].MonotonicNanos {
			return ordered[i].MonotonicNanos > ordered[j].MonotonicNanos
		}
		return ordered[i].ObservedAt.After(ordered[j].ObservedAt)
	})
	result := Resolution{Conflicts: []Assertion{}}
	if len(ordered) == 0 {
		return result
	}
	winner := ordered[0]
	result.Field, result.Value, result.WinningSource = winner.Field, winner.Value, winner.SourceKind
	for _, assertion := range ordered[1:] {
		if assertion.Value != winner.Value {
			result.Conflicts = append(result.Conflicts, assertion)
		}
		if assertion.Precedence == winner.Precedence && assertion.SourceSequence == winner.SourceSequence &&
			assertion.MonotonicNanos == winner.MonotonicNanos && assertion.ObservedAt.Equal(winner.ObservedAt) {
			result.OrderingUncertain = true
		}
	}
	return result
}

func Order(records []evidence.Record) []evidence.Record {
	ordered := append([]evidence.Record{}, records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Sequence != ordered[j].Sequence {
			return ordered[i].Sequence < ordered[j].Sequence
		}
		if ordered[i].MonotonicNanos != ordered[j].MonotonicNanos {
			return ordered[i].MonotonicNanos < ordered[j].MonotonicNanos
		}
		if !ordered[i].ObservedAt.Equal(ordered[j].ObservedAt) {
			return ordered[i].ObservedAt.Before(ordered[j].ObservedAt)
		}
		return ordered[i].ID < ordered[j].ID
	})
	return ordered
}
