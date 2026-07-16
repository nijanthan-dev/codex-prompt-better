package attribution

import "testing"

func TestResolve_Fixtures(t *testing.T) {
	t.Parallel()
	candidates := []Candidate{
		{ProjectID: "alpha", RepositoryID: "repo-alpha", WorktreeID: "wt-alpha", Root: "/synthetic/alpha", Remote: "https://example.invalid/team/alpha.git", RenamedFrom: []string{"git@example.invalid:old/alpha.git"}},
		{ProjectID: "beta", RepositoryID: "repo-beta", WorktreeID: "wt-beta", Root: "/synthetic/alpha/nested", Remote: "https://example.invalid/team/beta.git", Sparse: true},
	}
	tests := []struct {
		name        string
		observation Observation
		wantState   string
		wantProject string
	}{
		{name: "worktree rolls up", observation: Observation{WorktreeID: "wt-alpha"}, wantState: "attributed", wantProject: "alpha"},
		{name: "nested repo wins exact identity", observation: Observation{RepositoryID: "repo-beta", Root: "/synthetic/alpha/nested"}, wantState: "attributed", wantProject: "beta"},
		{name: "renamed remote", observation: Observation{Remote: "git@example.invalid:old/alpha.git"}, wantState: "attributed", wantProject: "alpha"},
		{name: "sparse checkout", observation: Observation{RepositoryID: "repo-beta"}, wantState: "attributed", wantProject: "beta"},
		{name: "temporary", observation: Observation{Temporary: true, Root: "/synthetic/tmp"}, wantState: "unattributed"},
		{name: "multi project preserved", observation: Observation{ProjectIDs: []string{"beta", "alpha"}}, wantState: "multi_project"},
		{name: "unattributed", observation: Observation{Root: "/synthetic/unknown"}, wantState: "unattributed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := Resolve(test.observation, candidates)
			if result.State != test.wantState {
				t.Fatalf("got state %q, want %q", result.State, test.wantState)
			}
			if test.wantProject != "" && (len(result.ProjectIDs) != 1 || result.ProjectIDs[0] != test.wantProject) {
				t.Fatalf("unexpected projects: %#v", result.ProjectIDs)
			}
		})
	}
}

func TestResolve_AmbiguousEqualPrecedence(t *testing.T) {
	t.Parallel()
	result := Resolve(Observation{Root: "/synthetic/shared"}, []Candidate{
		{ProjectID: "alpha", Root: "/synthetic/shared"},
		{ProjectID: "beta", Root: "/synthetic/shared"},
	})
	if result.State != "ambiguous" || len(result.ProjectIDs) != 2 || result.Confidence != "low" {
		t.Fatalf("forced ambiguous attribution: %#v", result)
	}
}
