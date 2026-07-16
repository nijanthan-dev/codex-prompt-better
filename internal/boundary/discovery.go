package boundary

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const (
	MaxMetadataEntries  = 10_000
	MaxScopeDepth       = 64
	MaxInstructionBytes = 64 * 1024
)

type Candidate struct {
	ID          string
	Category    string
	SourceKind  string
	SourceRef   string
	Confidence  float64
	Specificity int
	Facts       map[string]string
	Conflicts   []string
}

type Context struct {
	Candidates      []Candidate
	MetadataEntries int
	TargetedReads   int
}

type Reader interface {
	ReadFile(name string) ([]byte, error)
	Stat(name string) (fs.FileInfo, error)
	Walk(root string, fn fs.WalkDirFunc) error
}

type FSReader struct{ FS fs.FS }

func (reader FSReader) ReadFile(name string) ([]byte, error)  { return fs.ReadFile(reader.FS, name) }
func (reader FSReader) Stat(name string) (fs.FileInfo, error) { return fs.Stat(reader.FS, name) }
func (reader FSReader) Walk(root string, fn fs.WalkDirFunc) error {
	return fs.WalkDir(reader.FS, root, fn)
}

func Discover(ctx context.Context, reader Reader, scopes []string) (Context, error) {
	normalizedScopes, err := normalizeScopes(scopes)
	if err != nil {
		return Context{}, err
	}
	result := Context{}
	seen := make(map[string]struct{})
	categoryCounts := make(map[string]int)
	add := func(category, sourceKind, key string, confidence float64, specificity int, facts map[string]string) {
		identity := category + "\x00" + key
		if _, exists := seen[identity]; exists {
			return
		}
		seen[identity] = struct{}{}
		categoryCounts[category]++
		id := fmt.Sprintf("%s.%04d", category, categoryCounts[category])
		normalizedFacts := make(map[string]string, len(facts)+2)
		for name, value := range facts {
			normalizedFacts[name] = value
		}
		normalizedFacts["category"] = category
		normalizedFacts["source_kind"] = sourceKind
		result.Candidates = append(result.Candidates, Candidate{ID: id, Category: category, SourceKind: sourceKind, SourceRef: id, Confidence: confidence, Specificity: specificity, Facts: normalizedFacts, Conflicts: []string{}})
	}

	if _, err := reader.Stat("."); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Context{}, nil
		}
		return Context{}, sanitized("context root unavailable", "context_root")
	}
	for _, scope := range normalizedScopes {
		add("scope", "user_request", scope, 1, pathDepth(scope), map[string]string{"category": "scope"})
	}

	err = reader.Walk(".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return sanitized("repository metadata unavailable", "context_root")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		result.MetadataEntries++
		if result.MetadataEntries > MaxMetadataEntries {
			return sanitized("repository metadata exceeds 10000 entries", "context_root")
		}
		clean := strings.TrimPrefix(path.Clean(strings.ReplaceAll(name, "\\", "/")), "./")
		if clean == "." {
			clean = ""
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			add("security", "repository_metadata", clean+":symlink", 1, pathDepth(clean), map[string]string{"category": "security", "ambiguous": "true"})
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		base := strings.ToLower(path.Base(clean))
		if base == ".git" {
			add("repository", "repository_metadata", clean+":repository", 1, pathDepth(clean), map[string]string{"category": "repository"})
			if !entry.IsDir() {
				add("worktree", "repository_metadata", clean+":worktree", 1, pathDepth(clean), map[string]string{"category": "worktree"})
			}
			if entry.IsDir() {
				return fs.SkipDir
			}
		}
		if entry.IsDir() {
			switch base {
			case "vendor", "node_modules", "third_party":
				add("vendor", "repository_metadata", clean, 1, pathDepth(clean), map[string]string{"category": "vendor"})
				return fs.SkipDir
			case "generated", "dist", "build":
				add("generated", "repository_metadata", clean, 0.9, pathDepth(clean), map[string]string{"category": "generated"})
				return fs.SkipDir
			}
			return nil
		}
		if !targetedFile(clean, base) {
			return nil
		}
		category := fileCategory(clean, base)
		add(category, "repository_metadata", clean, 1, pathDepth(clean), map[string]string{"category": category})
		if instructionFile(base) && appliesToScope(clean, normalizedScopes) {
			content, err := reader.ReadFile(clean)
			result.TargetedReads++
			if err != nil {
				return sanitized("instruction file unavailable", "context_root")
			}
			if len(content) > MaxInstructionBytes {
				return sanitized("instruction file exceeds 65536 bytes", "context_root")
			}
			add("instruction", "instruction", clean, 1, pathDepth(clean), map[string]string{"category": "instruction"})
			lower := strings.ToLower(string(content))
			if strings.Contains(lower, "non-goal") || strings.Contains(lower, "out of scope") {
				add("non_goal", "instruction", clean+":non-goal", 0.9, pathDepth(clean), map[string]string{"category": "non_goal"})
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Context{}, contracts.NewError(contracts.ErrorCodeBudgetExhausted, "boundary discovery cancelled or timed out", "context_root", true)
		}
		return Context{}, err
	}
	linkApplicableNonGoals(result.Candidates)
	sort.Slice(result.Candidates, func(i, j int) bool { return result.Candidates[i].ID < result.Candidates[j].ID })
	return result, nil
}

func linkApplicableNonGoals(candidates []Candidate) {
	scopes := candidateIndexes(candidates, "scope")
	nonGoals := candidateIndexes(candidates, "non_goal")
	for _, scope := range scopes {
		for _, nonGoal := range nonGoals {
			candidates[scope].Conflicts = append(candidates[scope].Conflicts, candidates[nonGoal].ID)
			candidates[nonGoal].Conflicts = append(candidates[nonGoal].Conflicts, candidates[scope].ID)
		}
	}
}

func candidateIndexes(candidates []Candidate, category string) []int {
	indexes := make([]int, 0)
	for index := range candidates {
		if candidates[index].Category == category {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func normalizeScopes(scopes []string) ([]string, error) {
	result := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, value := range scopes {
		value = strings.ReplaceAll(value, "\\", "/")
		clean := path.Clean(value)
		if clean == "." {
			clean = ""
		}
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || pathDepth(clean) > MaxScopeDepth {
			return nil, sanitized("scope must remain within context root", "scope")
		}
		if _, exists := seen[clean]; !exists {
			seen[clean] = struct{}{}
			result = append(result, clean)
		}
	}
	sort.Strings(result)
	return result, nil
}

func targetedFile(name, base string) bool {
	return workflowFile(name, base) || instructionFile(base) || base == "security.md" || privacyMetadataFile(base) || base == "release-please-config.json" || base == ".goreleaser.yml" || base == ".goreleaser.yaml" || base == "makefile"
}
func instructionFile(base string) bool { return base == "agents.md" || base == "claude.md" }
func privacyMetadataFile(base string) bool {
	if base == ".env" || base == "credentials.json" || base == "secrets.json" {
		return true
	}
	if !strings.HasPrefix(base, ".env.") {
		return false
	}
	switch strings.TrimPrefix(base, ".env.") {
	case "example", "sample", "template", "dist":
		return false
	default:
		return true
	}
}
func workflowFile(name, base string) bool {
	inWorkflowDirectory := strings.HasPrefix(name, ".github/workflows/") || strings.Contains(name, "/.github/workflows/")
	return inWorkflowDirectory && (strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml"))
}
func fileCategory(name, base string) string {
	switch {
	case instructionFile(base):
		return "instruction"
	case privacyMetadataFile(base):
		return "privacy"
	case workflowFile(name, base) || base == "makefile" || base == "security.md":
		if strings.Contains(base, "release") || strings.Contains(base, "publish") {
			return "release"
		}
		return "validation"
	default:
		return "release"
	}
}
func appliesToScope(name string, scopes []string) bool {
	dir := path.Dir(name)
	if dir == "." {
		dir = ""
	}
	if len(scopes) == 0 {
		return dir == ""
	}
	for _, scope := range scopes {
		if dir == "" || scope == dir || strings.HasPrefix(scope, dir+"/") {
			return true
		}
	}
	return false
}
func pathDepth(value string) int {
	if value == "" {
		return 0
	}
	return len(strings.Split(value, "/"))
}
func sanitized(message, field string) error {
	return contracts.NewError(contracts.ErrorCodeSemanticInvalid, message, field, false)
}
