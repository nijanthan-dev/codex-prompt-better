package boundary

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestResolvePrecedenceAndDoesNotMutateInput(t *testing.T) {
	input := []Candidate{
		{ID: "scope.repo", Category: "scope", SourceKind: "repository_metadata", Confidence: 1},
		{ID: "scope.user", Category: "scope", SourceKind: "user_request", Confidence: 1},
		{ID: "scope.instruction", Category: "scope", SourceKind: "instruction", Confidence: 1, Specificity: 2},
		{ID: "scope.config", Category: "scope", SourceKind: "configuration", Confidence: 1},
		{ID: "scope.host", Category: "scope", SourceKind: "host_permission", Confidence: 1},
	}
	original := cloneCandidates(input)
	resolution := Resolve(input)
	winner, ok := winner(resolution, "scope")
	if !ok || winner.ID != "scope.host" {
		t.Fatalf("winner=%+v", winner)
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatal("resolution mutated input")
	}
}

func winner(resolution Resolution, category string) (Candidate, bool) {
	for _, candidate := range resolution.Candidates {
		if candidate.Category == category {
			return candidate, true
		}
	}
	return Candidate{}, false
}

func TestResolveDeterministicForShuffledInputs(t *testing.T) {
	base := []Candidate{
		{ID: "instruction.a", SourceKind: "instruction", Specificity: 1, Confidence: .9},
		{ID: "instruction.b", SourceKind: "instruction", Specificity: 2, Confidence: .9},
		{ID: "scope.a", SourceKind: "user_request", Confidence: 1},
	}
	want := Resolve(base).Candidates
	random := rand.New(rand.NewSource(1))
	for iteration := 0; iteration < 100; iteration++ {
		shuffled := cloneCandidates(base)
		random.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if got := Resolve(shuffled).Candidates; !reflect.DeepEqual(got, want) {
			t.Fatalf("non-deterministic order: %+v", got)
		}
	}
}

func TestResolveDetectsConflictCycle(t *testing.T) {
	resolution := Resolve([]Candidate{
		{ID: "scope.a", Conflicts: []string{"scope.b"}},
		{ID: "scope.b", Conflicts: []string{"scope.c"}},
		{ID: "scope.c", Conflicts: []string{"scope.a"}},
	})
	want := []string{"scope.a", "scope.b", "scope.c", "scope.a"}
	if !reflect.DeepEqual(resolution.Cycle, want) {
		t.Fatalf("cycle=%v", resolution.Cycle)
	}
}
