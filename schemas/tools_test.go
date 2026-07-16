package schemas

import "testing"

func TestToolDefinition_ExtractsStandaloneRequestAndResult(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"improve_prompt", "create_goal_prompt", "create_review_fix_prompt",
		"lint_prompt", "get_checkpoint", "audit_session", "audit_project",
		"render_governance_report",
	} {
		for _, definition := range []string{"request", "result"} {
			schema, err := ToolDefinition(name, definition)
			if err != nil {
				t.Fatalf("%s %s: %v", name, definition, err)
			}
			if schema["type"] != "object" || schema["oneOf"] != nil || containsReference(schema) {
				t.Fatalf("%s %s is not standalone: %#v", name, definition, schema)
			}
		}
	}
}

func TestToolDefinition_ResolvesSharedReferences(t *testing.T) {
	t.Parallel()
	schema, err := ToolDefinition("get_checkpoint", "result")
	if err != nil {
		t.Fatal(err)
	}
	properties, _ := schema["properties"].(map[string]any)
	accepted, _ := properties["accepted_decisions"].(map[string]any)
	if accepted["type"] != "array" || accepted["maxItems"] == nil {
		t.Fatal("shared list definition not resolved")
	}
}

func containsReference(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if typed["$ref"] != nil {
			return true
		}
		for _, item := range typed {
			if containsReference(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsReference(item) {
				return true
			}
		}
	}
	return false
}
