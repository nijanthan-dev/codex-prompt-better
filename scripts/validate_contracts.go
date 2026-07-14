package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const metaSchema = "https://json-schema.org/draft/2020-12/schema"

type document = map[string]any

type validator struct {
	root     string
	schemas  map[string]document
	fixtures map[string]document
	failures []string
}

type stats struct {
	schemas  int
	fixtures int
	cases    int
	tools    int
}

func main() {
	root, err := findRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAIL")
		fmt.Fprintln(os.Stderr, "-", err)
		os.Exit(1)
	}

	v := &validator{root: root}
	result := v.run()
	if len(v.failures) != 0 {
		fmt.Println("FAIL")
		for _, failure := range v.failures {
			fmt.Println("-", failure)
		}
		os.Exit(1)
	}
	fmt.Printf("PASS schemas=%d fixtures=%d cases=%d tools=%d\n", result.schemas, result.fixtures, result.cases, result.tools)
}

func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if isDir(filepath.Join(dir, "schemas", "v1")) && isDir(filepath.Join(dir, "testdata", "golden")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root not found")
		}
		dir = parent
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (v *validator) run() stats {
	v.schemas = v.loadJSONTree(filepath.Join(v.root, "schemas", "v1"))
	v.fixtures = v.loadJSONTree(filepath.Join(v.root, "testdata", "golden"))
	if len(v.schemas) != 14 {
		v.fail("expected 14 schemas, found %d", len(v.schemas))
	}

	v.validateEvidence()
	v.validateCapabilities()
	v.validateRuntimeObservations()
	v.validateSchemaMetadata()
	tools := v.validateTools()
	cases := v.validateGoldenCoverage()
	v.validateSensitiveContent()
	v.validateSourceLedger()
	v.validateDocumentation()
	v.validateCIRunners()

	return stats{schemas: len(v.schemas), fixtures: len(v.fixtures), cases: cases, tools: tools}
}

func (v *validator) loadJSONTree(root string) map[string]document {
	loaded := make(map[string]document)
	paths := walkFiles(root, ".json", &v.failures, v.root)
	for _, path := range paths {
		value, err := loadJSON(path)
		if err != nil {
			v.fail("%s: invalid JSON: %v", relative(v.root, path), err)
			continue
		}
		object, ok := value.(document)
		if !ok {
			v.fail("%s: JSON root must be object", relative(v.root, path))
			continue
		}
		loaded[clean(path)] = object
	}
	return loaded
}

func loadJSON(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	err = decoder.Decode(&extra)
	if err == nil {
		return nil, errors.New("multiple JSON values")
	}
	if !errors.Is(err, io.EOF) {
		return nil, err
	}
	return normalizeNumbers(value), nil
}

func normalizeNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		number, _ := typed.Float64()
		return number
	case []any:
		for index, item := range typed {
			typed[index] = normalizeNumbers(item)
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeNumbers(item)
		}
	}
	return value
}

func (v *validator) schemaValid(value any, schema document, schemaPath string) bool {
	if reference, ok := stringValue(schema["$ref"]); ok {
		resolved, path, found := v.resolveReference(reference, schemaPath)
		return found && v.schemaValid(value, resolved, path)
	}
	if branches := documents(schema["oneOf"]); branches != nil {
		matches := 0
		for _, branch := range branches {
			if v.schemaValid(value, branch, schemaPath) {
				matches++
			}
		}
		if matches != 1 {
			return false
		}
	}
	if types := stringsValue(schema["type"]); len(types) != 0 && !matchesAnyType(value, types) {
		return false
	}
	if constant, exists := schema["const"]; exists && !equalJSON(value, constant) {
		return false
	}
	if enum, ok := schema["enum"].([]any); ok && !containsJSON(enum, value) {
		return false
	}
	if text, ok := value.(string); ok && !validString(text, schema) {
		return false
	}
	if number, ok := value.(float64); ok && !validNumber(number, schema) {
		return false
	}
	if items, ok := value.([]any); ok && !validArray(items, schema) {
		return false
	}
	for _, rule := range documents(schema["allOf"]) {
		condition, conditional := rule["if"].(document)
		if !conditional {
			if !v.schemaValid(value, rule, schemaPath) {
				return false
			}
			continue
		}
		if v.schemaValid(value, condition, schemaPath) {
			consequence, _ := rule["then"].(document)
			if !v.schemaValid(value, consequence, schemaPath) {
				return false
			}
		}
	}
	if object, ok := value.(document); ok {
		properties, _ := schema["properties"].(document)
		for _, required := range stringsValue(schema["required"]) {
			if _, exists := object[required]; !exists {
				return false
			}
		}
		if additional, exists := schema["additionalProperties"]; exists && additional == false {
			for key := range object {
				if _, exists := properties[key]; !exists {
					return false
				}
			}
		}
		for key, item := range object {
			property, exists := properties[key].(document)
			if exists && !v.schemaValid(item, property, schemaPath) {
				return false
			}
		}
	}
	if items, ok := value.([]any); ok {
		if itemSchema, exists := schema["items"].(document); exists {
			for _, item := range items {
				if !v.schemaValid(item, itemSchema, schemaPath) {
					return false
				}
			}
		}
	}
	return true
}

func (v *validator) resolveReference(reference, schemaPath string) (document, string, bool) {
	file, fragment, _ := strings.Cut(reference, "#")
	targetPath := schemaPath
	if file != "" {
		targetPath = clean(filepath.Join(filepath.Dir(schemaPath), filepath.FromSlash(file)))
	}
	target, found := v.schemas[targetPath]
	if !found {
		return nil, targetPath, false
	}
	var current any = target
	for _, part := range strings.Split(strings.TrimPrefix(fragment, "/"), "/") {
		if part == "" {
			continue
		}
		object, ok := current.(document)
		if !ok {
			return nil, targetPath, false
		}
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		current, found = object[part]
		if !found {
			return nil, targetPath, false
		}
	}
	resolved, ok := current.(document)
	return resolved, targetPath, ok
}

func validString(value string, schema document) bool {
	if minimum, ok := numberValue(schema["minLength"]); ok && len(value) < int(minimum) {
		return false
	}
	if maximum, ok := numberValue(schema["maxLength"]); ok && len(value) > int(maximum) {
		return false
	}
	if pattern, ok := stringValue(schema["pattern"]); ok {
		compiled, err := regexp.Compile(pattern)
		if err != nil || !compiled.MatchString(value) {
			return false
		}
	}
	format, _ := stringValue(schema["format"])
	switch format {
	case "date":
		if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(value) {
			return false
		}
		_, err := time.Parse("2006-01-02", value)
		return err == nil
	case "date-time":
		if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$`).MatchString(value) {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, value)
		return err == nil
	default:
		return true
	}
}

func validNumber(value float64, schema document) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	if minimum, ok := numberValue(schema["minimum"]); ok && value < minimum {
		return false
	}
	if maximum, ok := numberValue(schema["maximum"]); ok && value > maximum {
		return false
	}
	return true
}

func validArray(items []any, schema document) bool {
	if minimum, ok := numberValue(schema["minItems"]); ok && len(items) < int(minimum) {
		return false
	}
	if maximum, ok := numberValue(schema["maxItems"]); ok && len(items) > int(maximum) {
		return false
	}
	unique, _ := schema["uniqueItems"].(bool)
	if !unique {
		return true
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		encoded, _ := json.Marshal(item)
		key := string(encoded)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func matchesAnyType(value any, types []string) bool {
	for _, kind := range types {
		switch kind {
		case "object":
			_, ok := value.(document)
			if ok {
				return true
			}
		case "array":
			_, ok := value.([]any)
			if ok {
				return true
			}
		case "string":
			_, ok := value.(string)
			if ok {
				return true
			}
		case "integer":
			number, ok := value.(float64)
			if ok && math.Trunc(number) == number {
				return true
			}
		case "number":
			_, ok := value.(float64)
			if ok {
				return true
			}
		case "boolean":
			_, ok := value.(bool)
			if ok {
				return true
			}
		case "null":
			if value == nil {
				return true
			}
		}
	}
	return false
}

func (v *validator) validateEvidence() {
	schemaPath := v.schemaPath("evidence-envelope.schema.json")
	schema := v.schemas[schemaPath]
	positive := v.fixture("evidence-positive.json")
	negative := v.fixture("evidence-negative.json")
	v.expectValid(positive, schema, schemaPath, "positive evidence fixture fails evidence contract")
	v.expectInvalid(negative, schema, schemaPath, "negative evidence fixture accepted")

	missingObserved := clone(positive)
	delete(missingObserved, "observed_at")
	v.expectInvalid(missingObserved, schema, schemaPath, "evidence accepted without observed_at")
	mutableSource := clone(positive)
	mutableSource["source"].(document)["read_only"] = false
	v.expectInvalid(mutableSource, schema, schemaPath, "evidence accepted mutable source")
	naiveTime := clone(positive)
	naiveTime["observed_at"] = "2026-07-14T00:00:00"
	v.expectInvalid(naiveTime, schema, schemaPath, "evidence accepted non-RFC3339 observed_at")

	unsafeIDs := []struct{ field, value string }{
		{"source.identity", "/home/synthetic/.codex/session.json"},
		{"source.identity", "sk-proj-" + strings.Repeat("a", 20)},
		{"external_id", `C:\Users\synthetic\session.json`},
		{"external_id", "ghp_" + strings.Repeat("a", 20)},
	}
	for _, item := range unsafeIDs {
		candidate := clone(positive)
		if item.field == "source.identity" {
			candidate["source"].(document)["identity"] = item.value
		} else {
			candidate[item.field] = item.value
		}
		v.expectInvalid(candidate, schema, schemaPath, "evidence accepted unsafe "+item.field)
	}
	for _, value := range []string{"/home/synthetic/.codex/session.json", "sk-proj-" + strings.Repeat("a", 20)} {
		candidate := clone(positive)
		candidate["cursor"] = document{"kind": "unknown", "value": value}
		v.expectInvalid(candidate, schema, schemaPath, "evidence accepted unsafe cursor value")
	}
	for _, value := range []string{
		"/Users/synthetic/.codex/session.json",
		"sk-proj-" + strings.Repeat("a", 20),
		"ghp_" + strings.Repeat("a", 20),
	} {
		candidate := clone(positive)
		candidate["source"].(document)["kind"] = value
		v.expectInvalid(candidate, schema, schemaPath, "evidence accepted unsafe source kind")
	}
}

func (v *validator) validateCapabilities() {
	path := v.schemaPath("capability.schema.json")
	schema := v.schemas[path]
	known := document{
		"schema_version": "1.0.0", "name": "prompt_caching", "observed_name": nil,
		"state": "supported", "source": "runtime_observed", "checked_at": "2026-07-14",
		"product_surface": "openai_api", "mutable_by_prompt_better": false,
	}
	v.expectValid(known, schema, path, "known capability fails contract")
	unknown := clone(known)
	unknown["name"], unknown["observed_name"], unknown["state"] = "unknown", "future_host_capability", "unknown"
	v.expectValid(unknown, schema, path, "unknown capability name cannot be preserved")
	misclassified := clone(unknown)
	misclassified["state"] = "supported"
	v.expectInvalid(misclassified, schema, path, "unknown capability coerced to supported")
	unsafe := clone(unknown)
	unsafe["observed_name"] = "/home/synthetic/capability"
	v.expectInvalid(unsafe, schema, path, "unsafe unknown capability name accepted")
}

func (v *validator) validateRuntimeObservations() {
	path := v.schemaPath("runtime-observation.schema.json")
	schema := v.schemas[path]
	base := document{
		"schema_version": "1.0.0", "trajectory_id": "trajectory-alpha", "knowledge_state": "observed",
		"product_surface": "local", "accounting_regime": "local", "usage_unit": "event",
		"source_version": nil, "confidence": float64(1),
	}
	v.expectValid(base, schema, path, "synthetic runtime observation fails contract")
	unsafe := clone(base)
	unsafe["tool_call"] = document{"call_id": "/home/synthetic/tool", "path": "direct", "caller": nil, "program_output_id": nil}
	v.expectInvalid(unsafe, schema, path, "runtime observation accepted unsafe tool identifier")
	unlinked := clone(base)
	unlinked["tool_call"] = document{"call_id": "call-programmatic", "path": "programmatic", "caller": "synthetic-program", "program_output_id": nil}
	v.expectInvalid(unlinked, schema, path, "runtime observation accepted unlinked programmatic tool call")
	linked := clone(unlinked)
	linked["tool_call"].(document)["program_output_id"] = "program-output-alpha"
	v.expectValid(linked, schema, path, "runtime observation rejected linked programmatic tool call")

	subscription := withAccounting(base, "codex_subscription", "api_money", "currency_minor")
	v.expectInvalid(subscription, schema, path, "subscription observation accepted API accounting")
	for _, pair := range [][2]string{{"api_tokens", "currency_minor"}, {"api_money", "token"}, {"api_money", "unknown"}} {
		candidate := withAccounting(base, "openai_api", pair[0], pair[1])
		v.expectInvalid(candidate, schema, path, fmt.Sprintf("API accounting accepted mismatched %s/%s", pair[0], pair[1]))
	}
	v.expectValid(withAccounting(base, "openai_api", "unknown", "unknown"), schema, path, "API accounting rejected paired unknown state")
	for _, pair := range [][2]string{{"api_tokens", "token"}, {"api_money", "currency_minor"}} {
		candidate := withAccounting(base, "openai_api", pair[0], pair[1])
		v.expectValid(candidate, schema, path, fmt.Sprintf("API accounting rejected valid %s/%s", pair[0], pair[1]))
	}
	nonFinite := clone(base)
	nonFinite["confidence"] = math.NaN()
	v.expectInvalid(nonFinite, schema, path, "runtime observation accepted non-finite number")
}

func withAccounting(base document, surface, regime, unit string) document {
	result := clone(base)
	result["product_surface"], result["accounting_regime"], result["usage_unit"] = surface, regime, unit
	return result
}

func (v *validator) validateSchemaMetadata() {
	for path, schema := range v.schemas {
		if schema["$schema"] != metaSchema {
			v.fail("%s: wrong meta-schema", filepath.Base(path))
		}
		if schema["$id"] == nil {
			v.fail("%s: missing $id", filepath.Base(path))
		}
		if filepath.Base(filepath.Dir(path)) != "tools" {
			continue
		}
		defs, _ := schema["$defs"].(document)
		for _, kind := range []string{"request", "result"} {
			definition, ok := defs[kind].(document)
			if !ok {
				v.fail("%s: missing request/result", filepath.Base(path))
				break
			}
			if definition["additionalProperties"] != false {
				v.fail("%s: %s must be closed", filepath.Base(path), kind)
			}
		}
	}
}

func (v *validator) validateTools() int {
	fixture := v.fixture("tool-examples.json")
	tools, _ := fixture["tools"].(document)
	expected := make(map[string]struct{})
	for path := range v.schemas {
		if filepath.Base(filepath.Dir(path)) == "tools" {
			expected[strings.TrimSuffix(filepath.Base(path), ".schema.json")] = struct{}{}
		}
	}
	if !sameKeys(tools, expected) {
		v.fail("tool examples do not exactly cover seven tool schemas")
	}

	commonPath := v.schemaPath("common.schema.json")
	commonDefs, _ := v.schemas[commonPath]["$defs"].(document)
	errorSchema, _ := commonDefs["error"].(document)
	for name, rawExamples := range tools {
		examples, _ := rawExamples.(document)
		if !sameStringSet(keys(examples), []string{"request", "result", "error"}) {
			v.fail("%s: request/result/error examples required", name)
		}
		path := clean(filepath.Join(v.root, "schemas", "v1", "tools", name+".schema.json"))
		schema := v.schemas[path]
		defs, _ := schema["$defs"].(document)
		for _, kind := range []string{"request", "result", "error"} {
			v.expectValid(examples[kind], schema, path, fmt.Sprintf("%s: full tool schema rejects %s", name, kind))
		}
		v.expectInvalid(document{"arbitrary": true}, schema, path, name+": full tool schema accepts arbitrary payload")
		for _, kind := range []string{"request", "result"} {
			definition, _ := defs[kind].(document)
			example, _ := examples[kind].(document)
			if !v.schemaValid(example, definition, path) {
				v.fail("%s: positive %s fails contract", name, kind)
				continue
			}
			negative := clone(example)
			negative["unexpected"] = true
			v.expectInvalid(negative, definition, path, fmt.Sprintf("%s: negative closed-object case accepted", name))
		}
		errorExample, _ := examples["error"].(document)
		if !v.schemaValid(errorExample, errorSchema, commonPath) {
			v.fail("%s: error example fails common error contract", name)
		} else {
			invalid := clone(errorExample)
			invalid["unexpected"] = true
			v.expectInvalid(invalid, errorSchema, commonPath, name+": negative error closed-object case accepted")
		}
	}
	v.validateNestedToolContracts(tools)
	return len(tools)
}

func (v *validator) validateNestedToolContracts(tools document) {
	goal := nestedDocument(tools, "create_goal_prompt", "request", "prompt_plan")
	if goal != nil {
		invalid := clone(goal)
		delete(invalid, "goal")
		path := v.schemaPath("prompt-plan.schema.json")
		v.expectInvalid(invalid, v.schemas[path], path, "nested prompt-plan reference accepted missing goal")
	}
	improve := nestedDocument(tools, "improve_prompt", "request")
	if improve != nil {
		path := clean(filepath.Join(v.root, "schemas", "v1", "tools", "improve_prompt.schema.json"))
		defs, _ := v.schemas[path]["$defs"].(document)
		requestSchema, _ := defs["request"].(document)
		invalid := clone(improve)
		invalid["budget"].(document)["max_agent_depth"] = nil
		v.expectInvalid(invalid, requestSchema, path, "nested execution-budget reference accepted null bounded limit")
		excessive := clone(improve)
		excessive["budget"].(document)["max_concurrency"] = float64(65)
		v.expectInvalid(excessive, requestSchema, path, "nested execution-budget reference accepted excessive concurrency")
	}
	review := nestedDocument(tools, "create_review_fix_prompt", "request")
	if review != nil {
		path := clean(filepath.Join(v.root, "schemas", "v1", "tools", "create_review_fix_prompt.schema.json"))
		defs, _ := v.schemas[path]["$defs"].(document)
		requestSchema, _ := defs["request"].(document)
		invalid := clone(review)
		invalid["review_head"] = "not-hex"
		v.expectInvalid(invalid, requestSchema, path, "review head pattern constraint not enforced")
	}

	budgetPath := v.schemaPath("execution-budget.schema.json")
	allOf := documents(v.schemas[budgetPath]["allOf"])
	then, _ := allOf[0]["then"].(document)
	properties, _ := then["properties"].(document)
	for _, field := range []string{"max_agent_depth", "max_concurrency"} {
		definition, _ := properties[field].(document)
		if containsString(stringsValue(definition["type"]), "null") {
			v.fail("bounded delegation permits null %s", field)
		}
	}
}

func (v *validator) validateGoldenCoverage() int {
	required := stringSet("happy", "ambiguous", "malformed", "missing", "duplicate", "drifted", "sensitive", "privacy", "permissions", "stop_rules", "delegation_budget", "source_conflict", "unknown_capability", "subscription_units", "api_money", "macos", "linux", "windows", "stable_prefix", "compaction", "direct_tool", "programmatic_tool")
	fixture := v.fixture("behavior-cases.json")
	cases, _ := fixture["cases"].([]any)
	covered := make(map[string]struct{})
	ids := make(map[string]struct{})
	for _, rawCase := range cases {
		item, _ := rawCase.(document)
		for _, tag := range stringsValue(item["covers"]) {
			covered[tag] = struct{}{}
		}
		id, _ := stringValue(item["id"])
		if _, exists := ids[id]; exists {
			v.fail("duplicate golden case id")
		}
		ids[id] = struct{}{}
	}
	missing := difference(required, covered)
	if len(missing) != 0 {
		v.fail("missing golden coverage: %s", strings.Join(missing, ", "))
	}
	return len(cases)
}

func (v *validator) validateSensitiveContent() {
	pattern := regexp.MustCompile(`(?:/Users/|/home/[^/\s]+/|/mnt/[A-Za-z]/Users/|[A-Za-z]:\\Users\\[^\\\s]+|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-(?:proj-)?[A-Za-z0-9_-]{20,}|BEGIN [A-Z ]*PRIVATE KEY|@(?:gmail|outlook)\.)`)
	samples := []string{
		"/Users/" + "synthetic", "/home/" + "synthetic/work", "/mnt/c/" + "Users/synthetic",
		`C:\Users\synthetic`, "sk-" + "proj-" + strings.Repeat("a", 20), "github_" + "pat_" + strings.Repeat("a", 20),
	}
	for _, prefix := range []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"} {
		samples = append(samples, prefix+strings.Repeat("a", 20))
	}
	for _, sample := range samples {
		if !pattern.MatchString(sample) {
			v.fail("sensitive-pattern regression: %s", first(sample, 8))
		}
	}
	for _, root := range []string{filepath.Join(v.root, "docs"), filepath.Join(v.root, "schemas", "v1"), filepath.Join(v.root, "testdata")} {
		for _, path := range walkFiles(root, "", &v.failures, v.root) {
			data, err := os.ReadFile(path)
			if err == nil && pattern.Match(data) {
				v.fail("%s: sensitive-pattern match", relative(v.root, path))
			}
		}
	}
}

func (v *validator) validateCIRunners() {
	workflowRoot := filepath.Join(v.root, ".github", "workflows")
	runnerPattern := regexp.MustCompile(`(?m)^\s*runs-on:\s*([^\s#]+)`)
	for _, path := range walkFiles(workflowRoot, "", &v.failures, v.root) {
		if extension := filepath.Ext(path); extension != ".yml" && extension != ".yaml" {
			continue
		}
		content := v.read(path)
		lower := strings.ToLower(content)
		if strings.Contains(lower, "windows-") || strings.Contains(lower, "macos-") {
			v.fail("%s: paid non-Ubuntu runner configured", relative(v.root, path))
		}
		for _, match := range runnerPattern.FindAllStringSubmatch(content, -1) {
			if !strings.HasPrefix(match[1], "ubuntu-") {
				v.fail("%s: runner must be Ubuntu: %s", relative(v.root, path), match[1])
			}
		}
	}
}

func (v *validator) validateSourceLedger() {
	ledgerPath := filepath.Join(v.root, "docs", "contracts", "source-ledger.md")
	ledger := v.read(ledgerPath)
	expected := stringSet("OAI-56", "OAI-PE", "OAI-RB", "OAI-PC", "OAI-CO", "OAI-FC", "OAI-CS", "OAI-MG", "STAFF-1", "PRACT-1")
	rows := make(map[string][]string)
	validID := regexp.MustCompile(`^(?:OAI-[A-Z0-9]+|STAFF-1|PRACT-1)$`)
	for _, line := range strings.Split(ledger, "\n") {
		cells := splitTableRow(line)
		if len(cells) != 0 && validID.MatchString(cells[0]) {
			rows[cells[0]] = cells
		}
	}
	for _, sourceID := range sortedKeys(expected) {
		if strings.Count(ledger, "| "+sourceID+" |") != 1 {
			v.fail("ledger mapping count invalid: %s", sourceID)
			continue
		}
		cells := rows[sourceID]
		if len(cells) != 6 || hasEmpty(cells[1:]) {
			v.fail("ledger row incomplete: %s", sourceID)
			continue
		}
		artifacts := regexp.MustCompile("`([^`]+/[^`]+)`").FindAllStringSubmatch(cells[4], -1)
		for _, match := range artifacts {
			if info, err := os.Stat(filepath.Join(v.root, filepath.FromSlash(match[1]))); err != nil || info.IsDir() {
				v.fail("ledger artifact missing: %s -> %s", sourceID, match[1])
			}
		}
	}

	hashes := v.read(filepath.Join(v.root, "docs", "contracts", "source-ledger.sha256"))
	hashRows := make(map[string]string)
	rowPattern := regexp.MustCompile(`^([a-f0-9]{64}|unavailable-no-source-url)  ([A-Z0-9-]+)$`)
	for _, line := range strings.Split(hashes, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		match := rowPattern.FindStringSubmatch(line)
		if match == nil {
			v.fail("invalid source hash row: %s", line)
			continue
		}
		if _, exists := hashRows[match[2]]; exists {
			v.fail("duplicate source hash ID: %s", match[2])
		}
		hashRows[match[2]] = match[1]
	}
	if !sameKeys(hashRows, expected) {
		v.fail("source hash IDs do not match ledger IDs")
	}
	if hashRows["PRACT-1"] != "unavailable-no-source-url" {
		v.fail("PRACT-1 must retain explicit unavailable marker")
	}
	hashPattern := regexp.MustCompile(`^[a-f0-9]{64}$`)
	for sourceID := range expected {
		if sourceID != "PRACT-1" && !hashPattern.MatchString(hashRows[sourceID]) {
			v.fail("source hash missing: %s", sourceID)
		}
	}
}

func (v *validator) validateDocumentation() {
	paths := walkFiles(filepath.Join(v.root, "docs"), ".md", &v.failures, v.root)
	linkPattern := regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	var combined strings.Builder
	for _, path := range paths {
		text := v.read(path)
		combined.WriteString(strings.ToLower(text))
		combined.WriteByte('\n')
		for _, match := range linkPattern.FindAllStringSubmatch(text, -1) {
			target := match[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "#") {
				continue
			}
			target, _, _ = strings.Cut(target, "#")
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), filepath.FromSlash(target))); err != nil {
				v.fail("%s: broken link %s", relative(v.root, path), match[1])
			}
		}
	}
	for _, contradiction := range []string{
		"remote telemetry is enabled by default", "raw prompt retention is enabled by default",
		"mcp startup triggers collection", "prompt better grants permission",
	} {
		if strings.Contains(combined.String(), contradiction) {
			v.fail("contradictory lifecycle/privacy claim: %s", contradiction)
		}
	}
}

func (v *validator) expectValid(value any, schema document, path, message string) {
	if !v.schemaValid(value, schema, path) {
		v.fail("%s", message)
	}
}

func (v *validator) expectInvalid(value any, schema document, path, message string) {
	if v.schemaValid(value, schema, path) {
		v.fail("%s", message)
	}
}

func (v *validator) fail(format string, args ...any) {
	v.failures = append(v.failures, fmt.Sprintf(format, args...))
}

func (v *validator) read(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		v.fail("%s: %v", relative(v.root, path), err)
		return ""
	}
	return string(data)
}

func (v *validator) schemaPath(name string) string {
	return clean(filepath.Join(v.root, "schemas", "v1", name))
}

func (v *validator) fixture(name string) document {
	return v.fixtures[clean(filepath.Join(v.root, "testdata", "golden", name))]
}

func clone(value document) document {
	encoded, _ := json.Marshal(value)
	var result document
	_ = json.Unmarshal(encoded, &result)
	return result
}

func nestedDocument(root document, path ...string) document {
	current := root
	for _, key := range path {
		next, ok := current[key].(document)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func documents(value any) []document {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]document, 0, len(items))
	for _, item := range items {
		object, ok := item.(document)
		if ok {
			result = append(result, object)
		}
	}
	return result
}

func stringsValue(value any) []string {
	if text, ok := value.(string); ok {
		return []string{text}
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func stringValue(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

func numberValue(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok
}

func equalJSON(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func containsJSON(values []any, wanted any) bool {
	for _, value := range values {
		if equalJSON(value, wanted) {
			return true
		}
	}
	return false
}

func sameKeys[V any](actual map[string]V, expected map[string]struct{}) bool {
	if len(actual) != len(expected) {
		return false
	}
	for key := range expected {
		if _, exists := actual[key]; !exists {
			return false
		}
	}
	return true
}

func keys(values document) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	set := stringSet(left...)
	for _, value := range right {
		if _, exists := set[value]; !exists {
			return false
		}
	}
	return true
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func difference(left, right map[string]struct{}) []string {
	var result []string
	for value := range left {
		if _, exists := right[value]; !exists {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func sortedKeys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func walkFiles(root, extension string, failures *[]string, repoRoot string) []string {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (extension == "" || filepath.Ext(path) == extension) {
			paths = append(paths, clean(path))
		}
		return nil
	})
	if err != nil {
		*failures = append(*failures, fmt.Sprintf("%s: %v", relative(repoRoot, root), err))
	}
	sort.Strings(paths)
	return paths
}

func splitTableRow(line string) []string {
	line = strings.Trim(strings.TrimSpace(line), "|")
	if line == "" {
		return nil
	}
	parts := strings.Split(line, "|")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

func hasEmpty(values []string) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}

func clean(path string) string { return filepath.Clean(path) }

func relative(root, path string) string {
	value, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(value)
}

func first(value string, length int) string {
	if len(value) <= length {
		return value
	}
	return value[:length]
}
