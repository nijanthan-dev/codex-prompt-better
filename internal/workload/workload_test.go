package workload

import (
	"math"
	"testing"
)

func TestDecomposeExactIdentityAndSimpsonDetection(t *testing.T) {
	previous := []ClassValue{
		{Class: "simple", Count: 90, Numerator: 90, Denominator: 90},
		{Class: "complex", Count: 10, Numerator: 90, Denominator: 10},
	}
	current := []ClassValue{
		{Class: "simple", Count: 10, Numerator: 5, Denominator: 10},
		{Class: "complex", Count: 90, Numerator: 720, Denominator: 90},
	}
	result := Decompose("tokens", previous, current)
	if result.Status != "simpson_mixed" {
		t.Fatalf("status=%s", result.Status)
	}
	change := totalNumerator(current) - totalNumerator(previous)
	reconstructed := *result.VolumeEffect + *result.MixEffect +
		*result.WithinClassEffect + *result.Residual
	if math.Abs(change-reconstructed) > 1e-9 {
		t.Fatalf("change=%v reconstructed=%v", change, reconstructed)
	}
	if *result.WithinClassEffect >= 0 || *result.MixEffect <= 0 {
		t.Fatalf("effects=%+v", result)
	}
}

func TestDecomposeInsufficient(t *testing.T) {
	result := Decompose("tokens", nil, []ClassValue{{Class: "simple", Count: 1}})
	if result.Status != "insufficient" || result.VolumeEffect != nil {
		t.Fatalf("result=%+v", result)
	}
}

func TestDecomposePermutationInvariantAndNoFalseSimpson(t *testing.T) {
	t.Parallel()
	previous := []ClassValue{
		{Class: "a", Count: 4, Numerator: 40, Denominator: 4},
		{Class: "b", Count: 6, Numerator: 120, Denominator: 6},
	}
	current := []ClassValue{
		{Class: "a", Count: 6, Numerator: 54, Denominator: 6},
		{Class: "b", Count: 4, Numerator: 72, Denominator: 4},
	}
	first := Decompose("tokens", previous, current)
	second := Decompose("tokens",
		[]ClassValue{previous[1], previous[0]},
		[]ClassValue{current[1], current[0]})
	if first.Status == "simpson_mixed" || second.Status != first.Status ||
		math.Abs(*first.Residual) > 1e-9 || math.Abs(*second.Residual) > 1e-9 ||
		*first.MixEffect != *second.MixEffect ||
		*first.WithinClassEffect != *second.WithinClassEffect {
		t.Fatalf("decomposition unstable: first=%+v second=%+v", first, second)
	}
}

func FuzzDecomposeIdentity(f *testing.F) {
	f.Add(uint16(10), uint16(20), uint16(30), uint16(40))
	f.Fuzz(func(t *testing.T, p1, p2, c1, c2 uint16) {
		previous := []ClassValue{
			{Class: "a", Count: float64(p1) + 1, Denominator: float64(p1) + 1, Numerator: float64(p1)},
			{Class: "b", Count: float64(p2) + 1, Denominator: float64(p2) + 1, Numerator: float64(p2) * 2},
		}
		current := []ClassValue{
			{Class: "a", Count: float64(c1) + 1, Denominator: float64(c1) + 1, Numerator: float64(c1)},
			{Class: "b", Count: float64(c2) + 1, Denominator: float64(c2) + 1, Numerator: float64(c2) * 2},
		}
		result := Decompose("synthetic", previous, current)
		change := totalNumerator(current) - totalNumerator(previous)
		reconstructed := *result.VolumeEffect + *result.MixEffect +
			*result.WithinClassEffect + *result.Residual
		if math.Abs(change-reconstructed) > 1e-7 {
			t.Fatalf("change=%v reconstructed=%v", change, reconstructed)
		}
	})
}
