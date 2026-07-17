package lineage

import "testing"

func TestCorrelate_ExactlyOnceAcrossAdaptersAndNesting(t *testing.T) {
	t.Parallel()
	events := []Event{
		{Source: "codex_jsonl", SourceEventID: "one", LogicalCallID: "host", Kind: "programmatic", Attempt: 1},
		{Source: "codex_state_sqlite", SourceEventID: "two", LogicalCallID: "host", Kind: "programmatic", Attempt: 1},
		{Source: "codex_jsonl", SourceEventID: "three", LogicalCallID: "leaf-a", ParentCallID: "host", Kind: "tool", Attempt: 2},
		{Source: "codex_jsonl", SourceEventID: "four", LogicalCallID: "leaf-a", ParentCallID: "host", Kind: "result", Attempt: 2, Result: true},
		{Source: "rollout_summary", SourceEventID: "five", LogicalCallID: "leaf-b", ParentCallID: "host", Kind: "delegation", LateRevision: true},
		{Source: "rollout_summary", SourceEventID: "five", LogicalCallID: "leaf-b", ParentCallID: "host", Kind: "delegation"},
	}
	result := Correlate(events)
	if len(result.Invocations) != 3 || result.HostCount != 1 || result.LeafCount != 2 {
		t.Fatalf("double-counted lineage: %#v", result)
	}
	if result.Invocations[1].Attempts != 2 || !result.Invocations[1].HasResult {
		t.Fatalf("call/result correlation failed: %#v", result.Invocations)
	}
}

func TestCorrelate_ConflictingParentsAreConservativeAndDeterministic(t *testing.T) {
	t.Parallel()
	events := []Event{
		{Source: "one", SourceEventID: "one", LogicalCallID: "child", ParentCallID: "parent-a"},
		{Source: "two", SourceEventID: "two", LogicalCallID: "child", ParentCallID: "parent-b"},
	}
	first := Correlate(events)
	second := Correlate([]Event{events[1], events[0]})
	if first.HostCount != 1 || second.HostCount != 1 ||
		!first.Invocations[0].ParentConflict || !second.Invocations[0].ParentConflict ||
		first.Invocations[0].ParentCallID != "" || second.Invocations[0].ParentCallID != "" {
		t.Fatalf("conflicting lineage was order dependent: %#v %#v", first, second)
	}
}
