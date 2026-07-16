package boundary

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

func TestDiscoverNormalizedCandidates(t *testing.T) {
	fixture := fstest.MapFS{
		".git":                                 {Data: []byte("gitdir: synthetic")},
		"AGENTS.md":                            {Data: []byte("Out of scope: generated artifacts.")},
		"SECURITY.md":                          {Data: []byte("Synthetic policy")},
		"src/file.go":                          {Data: []byte("package fixture")},
		"vendor/module/file.go":                {Data: []byte("package module")},
		"generated/output.go":                  {Data: []byte("package generated")},
		"release-please-config.json":           {Data: []byte("{}")},
		".github/workflows/ci.yml":             {Data: []byte("name: synthetic")},
		".github/workflows/release.yaml":       {Data: []byte("name: synthetic release")},
		"apps/api/.github/workflows/test.yaml": {Data: []byte("name: nested synthetic")},
	}
	result, err := Discover(context.Background(), FSReader{FS: fixture}, []string{"src"})
	if err != nil {
		t.Fatal(err)
	}
	wanted := []string{"repository", "worktree", "instruction", "scope", "non_goal", "vendor", "generated", "privacy", "validation", "release"}
	for _, category := range wanted {
		if !hasCategory(result.Candidates, category) {
			t.Fatalf("missing %s: %+v", category, result.Candidates)
		}
	}
	for _, candidate := range result.Candidates {
		if candidate.Facts["category"] != candidate.Category || candidate.Facts["source_kind"] != candidate.SourceKind {
			t.Fatalf("missing normalized facts: %+v", candidate)
		}
		if strings.Contains(candidate.SourceRef, "/") || strings.Contains(candidate.SourceRef, "\\") {
			t.Fatalf("unsafe source ref: %s", candidate.SourceRef)
		}
	}
}

func TestDiscoverMetadataAndReadBudgets(t *testing.T) {
	fixture := make(fstest.MapFS, MaxMetadataEntries-2)
	for index := 0; index < MaxMetadataEntries-2; index++ {
		fixture[fmt.Sprintf("files/%05d.txt", index)] = &fstest.MapFile{Data: nil}
	}
	result, err := Discover(context.Background(), FSReader{FS: fixture}, nil)
	if err != nil || result.MetadataEntries > MaxMetadataEntries || result.TargetedReads != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	fixture["files/overflow.txt"] = &fstest.MapFile{Data: nil}
	if _, err := Discover(context.Background(), FSReader{FS: fixture}, nil); err == nil {
		t.Fatal("metadata overflow accepted")
	}
}

func BenchmarkDiscoverTenThousandEntries(b *testing.B) {
	fixture := make(fstest.MapFS, MaxMetadataEntries-2)
	for index := 0; index < MaxMetadataEntries-2; index++ {
		fixture[fmt.Sprintf("files/%05d.txt", index)] = &fstest.MapFile{Data: nil}
	}
	reader := FSReader{FS: fixture}
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := Discover(context.Background(), reader, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDiscoverNoRepositoryAndUnsafeScope(t *testing.T) {
	result, err := Discover(context.Background(), FSReader{FS: fstest.MapFS{}}, nil)
	if err != nil || len(result.Candidates) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := Discover(context.Background(), FSReader{FS: fstest.MapFS{"file": {Data: nil}}}, []string{"../outside"}); err == nil {
		t.Fatal("unsafe scope accepted")
	}
}

func TestDiscoverDeterministicAcrossPlatformSeparators(t *testing.T) {
	fixture := fstest.MapFS{"src/file.go": {Data: nil}}
	left, err := Discover(context.Background(), FSReader{FS: fixture}, []string{"src/file.go"})
	if err != nil {
		t.Fatal(err)
	}
	right, err := Discover(context.Background(), FSReader{FS: fixture}, []string{`src\file.go`})
	if err != nil {
		t.Fatal(err)
	}
	if len(left.Candidates) != len(right.Candidates) {
		t.Fatalf("left=%+v right=%+v", left, right)
	}
	for index := range left.Candidates {
		if left.Candidates[index].ID != right.Candidates[index].ID {
			t.Fatal("platform-specific ordering")
		}
	}
}

func TestDiscoverSyntheticMonorepoAndNestedWorktree(t *testing.T) {
	fixture := fstest.MapFS{
		".git/config":                  {Data: nil},
		"AGENTS.md":                    {Data: []byte("Root rules")},
		"apps/api/.git":                {Data: []byte("gitdir: synthetic-target")},
		"apps/api/AGENTS.md":           {Data: []byte("Out of scope: deployment")},
		"apps/api/src/main.go":         {Data: nil},
		"apps/web/generated/bundle.js": {Data: nil},
	}
	result, err := Discover(context.Background(), FSReader{FS: fixture}, []string{"apps/api/src"})
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range []string{"repository", "worktree", "instruction", "non_goal", "generated", "scope"} {
		if !hasCategory(result.Candidates, category) {
			t.Fatalf("missing %s: %+v", category, result.Candidates)
		}
	}
}

func hasCategory(candidates []Candidate, category string) bool {
	for _, candidate := range candidates {
		if candidate.Category == category {
			return true
		}
	}
	return false
}

func FuzzNormalizeScopes(f *testing.F) {
	f.Add("src/file.go")
	f.Fuzz(func(t *testing.T, value string) { _, _ = normalizeScopes([]string{value}) })
}
