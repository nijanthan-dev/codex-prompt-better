package normalize

import (
	"testing"
	"time"
)

func TestResolve_PreservesConflictAndUsesSequenceBeforeWallClock(t *testing.T) {
	t.Parallel()
	lateWallClock := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	earlyWallClock := lateWallClock.Add(-time.Hour)
	result := Resolve([]Assertion{
		{Field: "state", Value: "old", SourceKind: "github", SourceSequence: 1, MonotonicNanos: 1, ObservedAt: lateWallClock, Precedence: 10},
		{Field: "state", Value: "new", SourceKind: "git", SourceSequence: 2, MonotonicNanos: 2, ObservedAt: earlyWallClock, Precedence: 10},
	})
	if result.Value != "new" || len(result.Conflicts) != 1 {
		t.Fatalf("unexpected resolution: %#v", result)
	}
}

func TestResolve_ReportsOrderingUncertainty(t *testing.T) {
	t.Parallel()
	now := time.Now()
	result := Resolve([]Assertion{
		{Field: "state", Value: "one", SourceKind: "a", SourceSequence: 1, MonotonicNanos: 1, ObservedAt: now, Precedence: 1},
		{Field: "state", Value: "two", SourceKind: "b", SourceSequence: 1, MonotonicNanos: 1, ObservedAt: now, Precedence: 1},
	})
	reversed := Resolve([]Assertion{
		{Field: "state", Value: "two", SourceKind: "b", SourceSequence: 1, MonotonicNanos: 1, ObservedAt: now, Precedence: 1},
		{Field: "state", Value: "one", SourceKind: "a", SourceSequence: 1, MonotonicNanos: 1, ObservedAt: now, Precedence: 1},
	})
	if !result.OrderingUncertain || result.Value != "" || result.WinningSource != "" ||
		reversed.Value != "" || reversed.WinningSource != "" {
		t.Fatalf("equal conflicting evidence resolved authoritatively: %#v %#v", result, reversed)
	}
}
