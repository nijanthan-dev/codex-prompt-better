package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContracts(t *testing.T) {
	root, err := findRoot()
	if err != nil {
		t.Fatal(err)
	}
	v := &validator{root: root}
	result := v.run()
	if len(v.failures) != 0 {
		t.Fatalf("contract validation failed:\n%s", strings.Join(v.failures, "\n"))
	}
	if result != (stats{schemas: 17, fixtures: 4, cases: 20, tools: 8}) {
		t.Fatalf("unexpected validation stats: %+v", result)
	}
}

func TestLoadJSONRejectsTrailingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte(`{} trailing`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadJSON(path); err == nil {
		t.Fatal("trailing content accepted")
	}
}

func TestLoadJSONTreeRejectsNonObject(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "schema.json")
	if err := os.WriteFile(path, []byte(`null`), 0o600); err != nil {
		t.Fatal(err)
	}
	v := &validator{root: root}
	if loaded := v.loadJSONTree(root); len(loaded) != 0 {
		t.Fatalf("non-object schema loaded: %v", loaded)
	}
	if len(v.failures) != 1 || !strings.Contains(v.failures[0], "JSON root must be object") {
		t.Fatalf("missing non-object failure: %v", v.failures)
	}
}

func TestSchemaValidRejectsNonFiniteNumber(t *testing.T) {
	v := &validator{}
	schema := document{"type": "number", "minimum": float64(0), "maximum": float64(1)}
	if v.schemaValid(math.NaN(), schema, "") {
		t.Fatal("non-finite number accepted")
	}
}

func TestPolicyPackSchemaRejectsUnknownAction(t *testing.T) {
	root, err := findRoot()
	if err != nil {
		t.Fatal(err)
	}
	value, err := loadJSON(filepath.Join(root, "schemas", "v1", "policy-pack.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema := value.(document)["$defs"].(document)["condition"].(document)
	v := &validator{}
	valid := document{"field": "action_class", "operator": "equals", "value": "read_only"}
	if !v.schemaValid(valid, schema, "") {
		t.Fatal("known action rejected")
	}
	invalid := document{"field": "action_class", "operator": "equals", "value": "unknown_action"}
	if v.schemaValid(invalid, schema, "") {
		t.Fatal("unknown action accepted")
	}
}
