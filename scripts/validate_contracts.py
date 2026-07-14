#!/usr/bin/env python3
"""Deterministic, dependency-free guardrails for frozen contract artifacts."""
from __future__ import annotations

import json
import re
import sys
from copy import deepcopy
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCHEMAS = ROOT / "schemas" / "v1"
GOLDEN = ROOT / "testdata" / "golden"
failures: list[str] = []


def load(path: Path):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:
        failures.append(f"{path.relative_to(ROOT)}: invalid JSON: {exc}")
        return None


def expected_types(schema):
    wanted = schema.get("type")
    if wanted is None:
        return []
    if isinstance(wanted, list):
        return wanted
    return [wanted]


def type_matches(value, kind):
    if kind == "object":
        return isinstance(value, dict)
    if kind == "array":
        return isinstance(value, list)
    if kind == "string":
        return isinstance(value, str)
    if kind == "integer":
        return isinstance(value, int) and not isinstance(value, bool)
    if kind == "number":
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    if kind == "boolean":
        return isinstance(value, bool)
    if kind == "null":
        return value is None
    return False


def resolve_reference(reference, schema_path):
    file_name, _, fragment = reference.partition("#")
    target_path = (schema_path.parent / file_name).resolve() if file_name else schema_path
    target = schemas.get(target_path)
    if target is None:
        return None, target_path
    for part in fragment.removeprefix("/").split("/") if fragment else []:
        target = target[part.replace("~1", "/").replace("~0", "~")]
    return target, target_path


def shallow_valid(value, schema, schema_path):
    if "$ref" in schema:
        resolved, resolved_path = resolve_reference(schema["$ref"], schema_path)
        return resolved is not None and shallow_valid(value, resolved, resolved_path)

    kinds = expected_types(schema)
    if kinds and not any(type_matches(value, kind) for kind in kinds):
        return False
    if "const" in schema and value != schema["const"]:
        return False
    if "enum" in schema and value not in schema["enum"]:
        return False

    for rule in schema.get("allOf", []):
        condition = rule.get("if")
        if condition is None:
            if not shallow_valid(value, rule, schema_path):
                return False
            continue
        if shallow_valid(value, condition, schema_path):
            consequence = rule.get("then", {})
            if not shallow_valid(value, consequence, schema_path):
                return False

    if isinstance(value, dict):
        required = set(schema.get("required", []))
        properties = schema.get("properties", {})
        if not required <= set(value):
            return False
        if schema.get("additionalProperties") is False and not set(value) <= set(properties):
            return False
        return all(
            shallow_valid(item, properties[key], schema_path)
            for key, item in value.items()
            if key in properties
        )

    if isinstance(value, list) and "items" in schema:
        return all(shallow_valid(item, schema["items"], schema_path) for item in value)
    return True


schemas = {p: load(p) for p in sorted(SCHEMAS.rglob("*.json"))}
fixtures = {p: load(p) for p in sorted(GOLDEN.rglob("*.json"))}
if len(schemas) != 14:
    failures.append(f"expected 14 schemas, found {len(schemas)}")

for path, schema in schemas.items():
    if not isinstance(schema, dict):
        continue
    if schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
        failures.append(f"{path.name}: wrong meta-schema")
    if schema.get("$id") is None:
        failures.append(f"{path.name}: missing $id")
    if path.parent.name == "tools":
        defs = schema.get("$defs", {})
        if not {"request", "result"} <= set(defs):
            failures.append(f"{path.name}: missing request/result")
        for kind in ("request", "result"):
            if defs.get(kind, {}).get("additionalProperties") is not False:
                failures.append(f"{path.name}: {kind} must be closed")

tools = fixtures.get(GOLDEN / "tool-examples.json", {}).get("tools", {})
expected_tools = {p.stem.removesuffix(".schema") for p in (SCHEMAS / "tools").glob("*.json")}
if set(tools) != expected_tools:
    failures.append("tool examples do not exactly cover seven tool schemas")
for name, examples in tools.items():
    if set(examples) != {"request", "result", "error"}:
        failures.append(f"{name}: request/result/error examples required")

for path, schema in schemas.items():
    if path.parent.name != "tools" or not isinstance(schema, dict):
        continue
    name = path.stem.removesuffix(".schema")
    for kind in ("request", "result"):
        definition = schema["$defs"][kind]
        example = tools.get(name, {}).get(kind)
        if not shallow_valid(example, definition, path):
            failures.append(f"{name}: positive {kind} fails contract")
            continue
        negative = deepcopy(example)
        negative["unexpected"] = True
        if shallow_valid(negative, definition, path):
            failures.append(f"{name}: negative closed-object case accepted")

common_schema_path = SCHEMAS / "common.schema.json"
error_schema = schemas[common_schema_path]["$defs"]["error"]
for name, examples in tools.items():
    error = examples.get("error")
    if not shallow_valid(error, error_schema, common_schema_path):
        failures.append(f"{name}: error example fails common error contract")
        continue
    invalid_error = deepcopy(error)
    invalid_error["unexpected"] = True
    if shallow_valid(invalid_error, error_schema, common_schema_path):
        failures.append(f"{name}: negative error closed-object case accepted")

prompt_plan = tools.get("create_goal_prompt", {}).get("request", {}).get("prompt_plan")
if prompt_plan:
    invalid_prompt_plan = deepcopy(prompt_plan)
    invalid_prompt_plan.pop("goal", None)
    prompt_plan_schema = schemas[SCHEMAS / "prompt-plan.schema.json"]
    if shallow_valid(invalid_prompt_plan, prompt_plan_schema, SCHEMAS / "prompt-plan.schema.json"):
        failures.append("nested prompt-plan reference accepted missing goal")

improve_request = tools.get("improve_prompt", {}).get("request")
if improve_request:
    invalid_budget_request = deepcopy(improve_request)
    invalid_budget_request["budget"]["max_agent_depth"] = None
    improve_schema_path = SCHEMAS / "tools/improve_prompt.schema.json"
    improve_request_schema = schemas[improve_schema_path]["$defs"]["request"]
    if shallow_valid(invalid_budget_request, improve_request_schema, improve_schema_path):
        failures.append("nested execution-budget reference accepted null bounded limit")

budget_schema = schemas[SCHEMAS / "execution-budget.schema.json"]
bounded_limits = budget_schema["allOf"][0]["then"]["properties"]
for field in ("max_agent_depth", "max_concurrency"):
    if "null" in expected_types(bounded_limits[field]):
        failures.append(f"bounded delegation permits null {field}")

required_cases = {"happy","ambiguous","malformed","missing","duplicate","drifted","sensitive","privacy","permissions","stop_rules","delegation_budget","source_conflict","unknown_capability","subscription_units","api_money","macos","linux","windows","stable_prefix","compaction","direct_tool","programmatic_tool"}
cases = fixtures.get(GOLDEN / "behavior-cases.json", {}).get("cases", [])
covered = {tag for case in cases for tag in case.get("covers", [])}
missing = sorted(required_cases - covered)
if missing:
    failures.append("missing golden coverage: " + ", ".join(missing))
if len({case.get("id") for case in cases}) != len(cases):
    failures.append("duplicate golden case id")

sensitive = re.compile(
    r"(?:/Users/|[A-Za-z]:\\Users\\[^\\\s]+|ghp_[A-Za-z0-9]{20,}|"
    r"github_pat_[A-Za-z0-9_]{20,}|sk-(?:proj-)?[A-Za-z0-9_-]{20,}|"
    r"BEGIN [A-Z ]*PRIVATE KEY|@(?:gmail|outlook)\.)"
)
sensitive_samples = (
    "/Users/" + "synthetic",
    "C:" + "\\Users\\synthetic",
    "sk-" + "proj-" + "a" * 20,
    "github_" + "pat_" + "a" * 20,
)
for sample in sensitive_samples:
    if sensitive.search(sample) is None:
        failures.append("sensitive-pattern regression: " + sample[:8])
for base in (ROOT / "docs", SCHEMAS, ROOT / "testdata"):
    for path in base.rglob("*"):
        if path.is_file() and sensitive.search(path.read_text(encoding="utf-8", errors="replace")):
            failures.append(f"{path.relative_to(ROOT)}: sensitive-pattern match")

ledger = (ROOT / "docs/contracts/source-ledger.md").read_text(encoding="utf-8")
for source_id in ("OAI-56","OAI-PE","OAI-RB","OAI-PC","OAI-CO","OAI-FC","OAI-CS","OAI-MG","STAFF-1","PRACT-1"):
    if ledger.count(f"| {source_id} |") != 1:
        failures.append(f"ledger mapping count invalid: {source_id}")

hash_lines = [line for line in (ROOT / "docs/contracts/source-ledger.sha256").read_text().splitlines() if line and not line.startswith("#")]
if len(hash_lines) != 10 or sum(bool(re.match(r"^[a-f0-9]{64}  [A-Z0-9-]+$", line)) for line in hash_lines) != 9:
    failures.append("source hashes must contain nine retrieved hashes and one explicit unavailable source")

for path in (ROOT / "docs").rglob("*.md"):
    text = path.read_text(encoding="utf-8")
    for target in re.findall(r"\[[^]]+\]\(([^)]+)\)", text):
        if target.startswith(("http://", "https://", "#")):
            continue
        resolved = (path.parent / target.split("#", 1)[0]).resolve()
        if not resolved.exists():
            failures.append(f"{path.relative_to(ROOT)}: broken link {target}")

combined_docs = "\n".join(path.read_text(encoding="utf-8").lower() for path in (ROOT / "docs").rglob("*.md"))
for contradiction in ("remote telemetry is enabled by default", "raw prompt retention is enabled by default", "mcp startup triggers collection", "prompt better grants permission"):
    if contradiction in combined_docs:
        failures.append(f"contradictory lifecycle/privacy claim: {contradiction}")

if failures:
    print("FAIL")
    print("\n".join(f"- {item}" for item in failures))
    sys.exit(1)
print(f"PASS schemas={len(schemas)} fixtures={len(fixtures)} cases={len(cases)} tools={len(tools)}")
