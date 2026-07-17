package runtime

import (
	"math"
	"testing"
	"time"
)

func TestUnionDuration_ParallelAndBlockedExcluded(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	intervals := []Interval{
		{Start: start, End: start.Add(10 * time.Second), Class: "active"},
		{Start: start.Add(5 * time.Second), End: start.Add(15 * time.Second), Class: "active"},
		{Start: start, End: start.Add(time.Minute), Class: "blocked"},
	}
	if got := UnionDuration(intervals, map[string]bool{"active": true}); got != 15*time.Second {
		t.Fatalf("got %v, want 15s", got)
	}
}

func TestTextTokens_OverflowIsUnknown(t *testing.T) {
	t.Parallel()
	maximum, one := uint64(math.MaxUint64), uint64(1)
	if got, known := TextTokens([]Output{
		{Modality: "text", Tokens: &maximum}, {Modality: "context", Tokens: &one},
	}); got != 0 || known {
		t.Fatalf("overflow returned %d/%t", got, known)
	}
}

func TestTextTokens_BinaryMediaCannotInflate(t *testing.T) {
	t.Parallel()
	tokens := uint64(12)
	got, known := TextTokens([]Output{{Modality: "text", Bytes: 100, Tokens: &tokens}, {Modality: "image", Bytes: 10_000_000}, {Modality: "native", Bytes: 20_000_000}})
	if got != 12 || !known {
		t.Fatalf("got %d/%t, want 12/true", got, known)
	}
}

func TestTextTokens_MissingTextCountIsUnknown(t *testing.T) {
	t.Parallel()
	if got, known := TextTokens([]Output{{Modality: "context", Bytes: 100}}); got != 0 || known {
		t.Fatalf("got %d/%t, want unknown", got, known)
	}
}
