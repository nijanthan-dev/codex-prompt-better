// Package lineage creates exactly-once logical host and leaf invocation counts.
package lineage

import "sort"

type Event struct {
	Source        string
	SourceEventID string
	LogicalCallID string
	ParentCallID  string
	Kind          string
	Role          string
	Attempt       int
	Result        bool
	LateRevision  bool
}

type Invocation struct {
	LogicalCallID  string
	ParentCallID   string
	Kinds          []string
	Sources        []string
	Attempts       int
	HasResult      bool
	Revised        bool
	ParentConflict bool
}

type Summary struct {
	Invocations []Invocation
	HostCount   int
	LeafCount   int
}

func Correlate(events []Event) Summary {
	byCall := map[string]*Invocation{}
	seen := map[string]struct{}{}
	for _, event := range events {
		identity := event.Source + "\x00" + event.SourceEventID
		if event.LogicalCallID == "" || event.SourceEventID == "" {
			continue
		}
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		invocation, ok := byCall[event.LogicalCallID]
		if !ok {
			invocation = &Invocation{LogicalCallID: event.LogicalCallID, ParentCallID: event.ParentCallID, Kinds: []string{}, Sources: []string{}}
			byCall[event.LogicalCallID] = invocation
		} else if invocation.ParentCallID != event.ParentCallID {
			invocation.ParentCallID = ""
			invocation.ParentConflict = true
		}
		invocation.Kinds = appendUnique(invocation.Kinds, event.Kind)
		invocation.Sources = appendUnique(invocation.Sources, event.Source)
		if event.Attempt > invocation.Attempts {
			invocation.Attempts = event.Attempt
		}
		invocation.HasResult = invocation.HasResult || event.Result
		invocation.Revised = invocation.Revised || event.LateRevision
	}
	children := map[string]bool{}
	for _, invocation := range byCall {
		if invocation.ParentCallID != "" {
			children[invocation.ParentCallID] = true
		}
	}
	result := Summary{Invocations: []Invocation{}}
	for _, invocation := range byCall {
		result.Invocations = append(result.Invocations, *invocation)
		if invocation.ParentCallID == "" {
			result.HostCount++
		}
		if !children[invocation.LogicalCallID] {
			result.LeafCount++
		}
	}
	sort.Slice(result.Invocations, func(i, j int) bool { return result.Invocations[i].LogicalCallID < result.Invocations[j].LogicalCallID })
	return result
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
