// Package attribution resolves project evidence without forced allocation.
package attribution

import (
	"sort"
	"strings"
)

const AlgorithmVersion = "attribution-v1"

type Candidate struct {
	ProjectID    string
	RepositoryID string
	WorktreeID   string
	Root         string
	Remote       string
	RenamedFrom  []string
	Sparse       bool
}

type Observation struct {
	RepositoryID string
	WorktreeID   string
	Root         string
	Remote       string
	ProjectIDs   []string
	Temporary    bool
}

type Result struct {
	ProjectIDs       []string
	Evidence         []string
	Confidence       string
	AlgorithmVersion string
	State            string
}

func Resolve(observation Observation, candidates []Candidate) Result {
	result := Result{ProjectIDs: []string{}, Evidence: []string{}, Confidence: "unknown", AlgorithmVersion: AlgorithmVersion, State: "unattributed"}
	if observation.Temporary {
		result.Evidence = append(result.Evidence, "temporary_directory")
		return result
	}
	if len(observation.ProjectIDs) > 1 {
		result.ProjectIDs = unique(observation.ProjectIDs)
		result.Evidence = append(result.Evidence, "explicit_multi_project")
		result.Confidence = "high"
		result.State = "multi_project"
		return result
	}
	scores := map[string]int{}
	evidenceByProject := map[string][]string{}
	for _, candidate := range candidates {
		if candidate.ProjectID == "" {
			continue
		}
		add := func(score int, reason string) {
			scores[candidate.ProjectID] += score
			evidenceByProject[candidate.ProjectID] = append(evidenceByProject[candidate.ProjectID], reason)
		}
		if observation.WorktreeID != "" && observation.WorktreeID == candidate.WorktreeID {
			add(100, "worktree_identity")
		}
		if observation.RepositoryID != "" && observation.RepositoryID == candidate.RepositoryID {
			add(80, "repository_identity")
		}
		if normalizedRemote(observation.Remote) != "" && normalizedRemote(observation.Remote) == normalizedRemote(candidate.Remote) {
			add(60, "remote_identity")
		}
		for _, renamed := range candidate.RenamedFrom {
			if normalizedRemote(observation.Remote) == normalizedRemote(renamed) {
				add(50, "renamed_remote")
			}
		}
		if candidate.Root != "" && cleanRoot(observation.Root) == cleanRoot(candidate.Root) {
			add(40, "root_identity")
		}
	}
	best := 0
	for _, score := range scores {
		if score > best {
			best = score
		}
	}
	for projectID, score := range scores {
		if score == best && score > 0 {
			result.ProjectIDs = append(result.ProjectIDs, projectID)
		}
	}
	sort.Strings(result.ProjectIDs)
	if len(result.ProjectIDs) == 0 {
		return result
	}
	if len(result.ProjectIDs) > 1 {
		result.State = "ambiguous"
		result.Confidence = "low"
		result.Evidence = append(result.Evidence, "conflicting_equal_precedence")
		return result
	}
	result.State = "attributed"
	result.Evidence = append(result.Evidence, evidenceByProject[result.ProjectIDs[0]]...)
	result.Confidence = "medium"
	if best >= 80 {
		result.Confidence = "high"
	}
	return result
}

func normalizedRemote(remote string) string {
	remote = strings.TrimSuffix(strings.TrimSpace(strings.ToLower(remote)), ".git")
	remote = strings.TrimPrefix(remote, "https://")
	remote = strings.TrimPrefix(remote, "ssh://")
	remote = strings.Replace(remote, "git@", "", 1)
	remote = strings.Replace(remote, ":", "/", 1)
	return remote
}

func cleanRoot(root string) string { return strings.TrimSuffix(strings.TrimSpace(root), "/") }

func unique(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
