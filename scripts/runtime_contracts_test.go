package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/internal/compiler"
	"github.com/nijanthan-dev/codex-prompt-better/internal/lint"
	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestRuntimeResultsMatchV1Schemas(t *testing.T) {
	root, err := findRoot()
	if err != nil {
		t.Fatal(err)
	}
	v := &validator{root: root}
	v.schemas = v.loadJSONTree(filepath.Join(root, "schemas", "v1"))
	fixtureValue, err := loadJSON(filepath.Join(root, "testdata", "golden", "tool-examples.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := requireDocument(t, fixtureValue, "fixture")
	tools := requireDocument(t, fixture["tools"], "tools")
	for _, name := range []string{"improve_prompt", "create_goal_prompt", "create_review_fix_prompt", "lint_prompt"} {
		t.Run(name, func(t *testing.T) {
			example := requireDocument(t, tools[name], name)
			requestJSON, err := json.Marshal(example["request"])
			if err != nil {
				t.Fatal(err)
			}
			result := runCanonical(t, name, requestJSON)
			valueJSON, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(valueJSON, &value); err != nil {
				t.Fatal(err)
			}
			path := clean(filepath.Join(root, "schemas", "v1", "tools", name+".schema.json"))
			if !v.schemaValid(normalizeNumbers(value), v.schemas[path], path) {
				t.Fatalf("runtime result violates %s", path)
			}
		})
	}
}

func requireDocument(t *testing.T, value any, name string) document {
	t.Helper()
	doc, ok := value.(document)
	if !ok {
		t.Fatalf("%s must be an object", name)
	}
	return doc
}

func runCanonical(t *testing.T, name string, data []byte) any {
	t.Helper()
	switch name {
	case "improve_prompt":
		var request contracts.ImprovePromptRequest
		mustDecode(t, data, &request)
		result, err := compiler.Improve(context.Background(), request, policy.HostUnknown)
		if err != nil {
			t.Fatal(err)
		}
		return result
	case "create_goal_prompt":
		var request contracts.CreateGoalPromptRequest
		mustDecode(t, data, &request)
		result, err := compiler.CreateGoal(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		return result
	case "create_review_fix_prompt":
		var request contracts.CreateReviewFixPromptRequest
		mustDecode(t, data, &request)
		result, err := compiler.CreateReviewFix(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		return result
	case "lint_prompt":
		var request contracts.LintPromptRequest
		mustDecode(t, data, &request)
		result, err := lint.CheckPrompt(request)
		if err != nil {
			t.Fatal(err)
		}
		return result
	default:
		t.Fatalf("unknown canonical tool %q", name)
		return nil
	}
}

func mustDecode(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
