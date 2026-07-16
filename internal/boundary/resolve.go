package boundary

import "sort"

type Resolution struct {
	Candidates []Candidate
	Conflicted map[string]bool
	Cycle      []string
}

func Resolve(candidates []Candidate) Resolution {
	ordered := cloneCandidates(candidates)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if authority(left.SourceKind) != authority(right.SourceKind) {
			return authority(left.SourceKind) > authority(right.SourceKind)
		}
		if left.Specificity != right.Specificity {
			return left.Specificity > right.Specificity
		}
		if left.Confidence != right.Confidence {
			return left.Confidence > right.Confidence
		}
		return left.ID < right.ID
	})
	graph := make(map[string][]string, len(ordered))
	conflicted := make(map[string]bool)
	for _, candidate := range ordered {
		refs := append([]string{}, candidate.Conflicts...)
		sort.Strings(refs)
		graph[candidate.ID] = refs
		if len(refs) > 0 {
			conflicted[candidate.ID] = true
		}
		for _, ref := range refs {
			conflicted[ref] = true
		}
	}
	return Resolution{Candidates: ordered, Conflicted: conflicted, Cycle: firstCycle(graph)}
}

func cloneCandidates(candidates []Candidate) []Candidate {
	result := make([]Candidate, len(candidates))
	for index, candidate := range candidates {
		result[index] = candidate
		if candidate.Facts != nil {
			result[index].Facts = make(map[string]string, len(candidate.Facts))
			for key, value := range candidate.Facts {
				result[index].Facts[key] = value
			}
		}
		if candidate.Conflicts != nil {
			result[index].Conflicts = append([]string{}, candidate.Conflicts...)
		}
	}
	return result
}

func firstCycle(graph map[string][]string) []string {
	state := make(map[string]uint8, len(graph))
	stack := make([]string, 0, len(graph))
	position := make(map[string]int, len(graph))
	keys := make([]string, 0, len(graph))
	for key := range graph {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var visit func(string) []string
	visit = func(node string) []string {
		if state[node] == 1 {
			start := position[node]
			cycle := append([]string{}, stack[start:]...)
			return append(cycle, node)
		}
		if state[node] == 2 {
			return nil
		}
		state[node] = 1
		position[node] = len(stack)
		stack = append(stack, node)
		for _, next := range graph[node] {
			if _, exists := graph[next]; !exists {
				continue
			}
			if cycle := visit(next); len(cycle) > 0 {
				return cycle
			}
		}
		stack = stack[:len(stack)-1]
		delete(position, node)
		state[node] = 2
		return nil
	}
	for _, key := range keys {
		if cycle := visit(key); len(cycle) > 0 {
			return cycle
		}
	}
	return []string{}
}

func authority(source string) int {
	switch source {
	case "host_permission":
		return 6
	case "configuration":
		return 5
	case "instruction":
		return 4
	case "user_request":
		return 3
	case "repository_metadata":
		return 2
	default:
		return 1
	}
}
