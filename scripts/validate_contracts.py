#!/usr/bin/env python3
"""Deterministic, dependency-free guardrails for frozen contract artifacts."""
from __future__ import annotations

import json
import math
import re
import sys
from copy import deepcopy
from datetime import date, datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCHEMAS = ROOT / "schemas" / "v1"
GOLDEN = ROOT / "testdata" / "golden"
failures: list[str] = []


def reject_non_json_constant(value):
    raise ValueError(f"non-JSON numeric constant: {value}")


def load(path: Path):
    try:
        return json.loads(
            path.read_text(encoding="utf-8"),
            parse_constant=reject_non_json_constant,
        )
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


def schema_valid(value, schema, schema_path):
    if "$ref" in schema:
        resolved, resolved_path = resolve_reference(schema["$ref"], schema_path)
        return resolved is not None and schema_valid(value, resolved, resolved_path)
    if "oneOf" in schema:
        matches = sum(schema_valid(value, branch, schema_path) for branch in schema["oneOf"])
        if matches != 1:
            return False

    kinds = expected_types(schema)
    if kinds and not any(type_matches(value, kind) for kind in kinds):
        return False
    if "const" in schema and value != schema["const"]:
        return False
    if "enum" in schema and value not in schema["enum"]:
        return False
    if isinstance(value, str):
        if len(value) < schema.get("minLength", 0):
            return False
        if len(value) > schema.get("maxLength", len(value)):
            return False
        if "pattern" in schema and re.search(schema["pattern"], value) is None:
            return False
        try:
            if schema.get("format") == "date":
                if re.fullmatch(r"\d{4}-\d{2}-\d{2}", value) is None:
                    return False
                date.fromisoformat(value)
            if schema.get("format") == "date-time":
                if re.fullmatch(
                    r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})",
                    value,
                ) is None:
                    return False
                datetime.fromisoformat(value.replace("Z", "+00:00"))
        except ValueError:
            return False
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        if not math.isfinite(value):
            return False
        if value < schema.get("minimum", value):
            return False
        if value > schema.get("maximum", value):
            return False
    if isinstance(value, list):
        if len(value) < schema.get("minItems", 0):
            return False
        if len(value) > schema.get("maxItems", len(value)):
            return False
        if schema.get("uniqueItems") and len({json.dumps(item, sort_keys=True) for item in value}) != len(value):
            return False

    for rule in schema.get("allOf", []):
        condition = rule.get("if")
        if condition is None:
            if not schema_valid(value, rule, schema_path):
                return False
            continue
        if schema_valid(value, condition, schema_path):
            consequence = rule.get("then", {})
            if not schema_valid(value, consequence, schema_path):
                return False

    if isinstance(value, dict):
        required = set(schema.get("required", []))
        properties = schema.get("properties", {})
        if not required <= set(value):
            return False
        if schema.get("additionalProperties") is False and not set(value) <= set(properties):
            return False
        return all(
            schema_valid(item, properties[key], schema_path)
            for key, item in value.items()
            if key in properties
        )

    if isinstance(value, list) and "items" in schema:
        return all(schema_valid(item, schema["items"], schema_path) for item in value)
    return True


schemas = {p: load(p) for p in sorted(SCHEMAS.rglob("*.json"))}
fixtures = {p: load(p) for p in sorted(GOLDEN.rglob("*.json"))}
if len(schemas) != 14:
    failures.append(f"expected 14 schemas, found {len(schemas)}")

evidence_schema_path = SCHEMAS / "evidence-envelope.schema.json"
evidence_schema = schemas[evidence_schema_path]
evidence_positive = fixtures.get(GOLDEN / "evidence-positive.json")
evidence_negative = fixtures.get(GOLDEN / "evidence-negative.json")
if not schema_valid(evidence_positive, evidence_schema, evidence_schema_path):
    failures.append("positive evidence fixture fails evidence contract")
if schema_valid(evidence_negative, evidence_schema, evidence_schema_path):
    failures.append("negative evidence fixture accepted")
if evidence_positive:
    missing_observed_at = deepcopy(evidence_positive)
    missing_observed_at.pop("observed_at", None)
    if schema_valid(missing_observed_at, evidence_schema, evidence_schema_path):
        failures.append("evidence accepted without observed_at")
    mutable_source = deepcopy(evidence_positive)
    mutable_source["source"]["read_only"] = False
    if schema_valid(mutable_source, evidence_schema, evidence_schema_path):
        failures.append("evidence accepted mutable source")
    naive_observed_at = deepcopy(evidence_positive)
    naive_observed_at["observed_at"] = "2026-07-14T00:00:00"
    if schema_valid(naive_observed_at, evidence_schema, evidence_schema_path):
        failures.append("evidence accepted non-RFC3339 observed_at")
    for field, unsafe_value in (
        ("source.identity", "/home/synthetic/.codex/session.json"),
        ("source.identity", "sk-proj-" + "a" * 20),
        ("external_id", "C:\\Users\\synthetic\\session.json"),
        ("external_id", "ghp_" + "a" * 20),
    ):
        unsafe_identity = deepcopy(evidence_positive)
        if field == "source.identity":
            unsafe_identity["source"]["identity"] = unsafe_value
        else:
            unsafe_identity[field] = unsafe_value
        if schema_valid(unsafe_identity, evidence_schema, evidence_schema_path):
            failures.append(f"evidence accepted unsafe {field}")
    for unsafe_value in (
        "/home/synthetic/.codex/session.json",
        "sk-proj-" + "a" * 20,
    ):
        unsafe_cursor = deepcopy(evidence_positive)
        unsafe_cursor["cursor"] = {"kind": "unknown", "value": unsafe_value}
        if schema_valid(unsafe_cursor, evidence_schema, evidence_schema_path):
            failures.append("evidence accepted unsafe cursor value")

runtime_schema_path = SCHEMAS / "runtime-observation.schema.json"
runtime_schema = schemas[runtime_schema_path]
runtime_observation = {
    "schema_version": "1.0.0",
    "trajectory_id": "trajectory-alpha",
    "knowledge_state": "observed",
    "product_surface": "local",
    "accounting_regime": "local",
    "usage_unit": "event",
    "source_version": None,
    "confidence": 1,
}
if not schema_valid(runtime_observation, runtime_schema, runtime_schema_path):
    failures.append("synthetic runtime observation fails contract")
unsafe_runtime_observation = deepcopy(runtime_observation)
unsafe_runtime_observation["tool_call"] = {
    "call_id": "/home/synthetic/tool",
    "path": "direct",
    "caller": None,
    "program_output_id": None,
}
if schema_valid(unsafe_runtime_observation, runtime_schema, runtime_schema_path):
    failures.append("runtime observation accepted unsafe tool identifier")
unlinked_programmatic_call = deepcopy(runtime_observation)
unlinked_programmatic_call["tool_call"] = {
    "call_id": "call-programmatic",
    "path": "programmatic",
    "caller": "synthetic-program",
    "program_output_id": None,
}
if schema_valid(unlinked_programmatic_call, runtime_schema, runtime_schema_path):
    failures.append("runtime observation accepted unlinked programmatic tool call")
linked_programmatic_call = deepcopy(unlinked_programmatic_call)
linked_programmatic_call["tool_call"]["program_output_id"] = "program-output-alpha"
if not schema_valid(linked_programmatic_call, runtime_schema, runtime_schema_path):
    failures.append("runtime observation rejected linked programmatic tool call")
misclassified_subscription = deepcopy(runtime_observation)
misclassified_subscription.update(
    product_surface="codex_subscription",
    accounting_regime="api_money",
    usage_unit="currency_minor",
)
if schema_valid(misclassified_subscription, runtime_schema, runtime_schema_path):
    failures.append("subscription observation accepted API accounting")
non_finite_runtime = deepcopy(runtime_observation)
non_finite_runtime["confidence"] = float("nan")
if schema_valid(non_finite_runtime, runtime_schema, runtime_schema_path):
    failures.append("runtime observation accepted non-finite number")

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
    for kind in ("request", "result", "error"):
        example = tools.get(name, {}).get(kind)
        if not schema_valid(example, schema, path):
            failures.append(f"{name}: full tool schema rejects {kind}")
    if schema_valid({"arbitrary": True}, schema, path):
        failures.append(f"{name}: full tool schema accepts arbitrary payload")
    for kind in ("request", "result"):
        definition = schema["$defs"][kind]
        example = tools.get(name, {}).get(kind)
        if not schema_valid(example, definition, path):
            failures.append(f"{name}: positive {kind} fails contract")
            continue
        negative = deepcopy(example)
        negative["unexpected"] = True
        if schema_valid(negative, definition, path):
            failures.append(f"{name}: negative closed-object case accepted")

common_schema_path = SCHEMAS / "common.schema.json"
error_schema = schemas[common_schema_path]["$defs"]["error"]
for name, examples in tools.items():
    error = examples.get("error")
    if not schema_valid(error, error_schema, common_schema_path):
        failures.append(f"{name}: error example fails common error contract")
        continue
    invalid_error = deepcopy(error)
    invalid_error["unexpected"] = True
    if schema_valid(invalid_error, error_schema, common_schema_path):
        failures.append(f"{name}: negative error closed-object case accepted")

prompt_plan = tools.get("create_goal_prompt", {}).get("request", {}).get("prompt_plan")
if prompt_plan:
    invalid_prompt_plan = deepcopy(prompt_plan)
    invalid_prompt_plan.pop("goal", None)
    prompt_plan_schema = schemas[SCHEMAS / "prompt-plan.schema.json"]
    if schema_valid(invalid_prompt_plan, prompt_plan_schema, SCHEMAS / "prompt-plan.schema.json"):
        failures.append("nested prompt-plan reference accepted missing goal")

improve_request = tools.get("improve_prompt", {}).get("request")
if improve_request:
    invalid_budget_request = deepcopy(improve_request)
    invalid_budget_request["budget"]["max_agent_depth"] = None
    improve_schema_path = SCHEMAS / "tools/improve_prompt.schema.json"
    improve_request_schema = schemas[improve_schema_path]["$defs"]["request"]
    if schema_valid(invalid_budget_request, improve_request_schema, improve_schema_path):
        failures.append("nested execution-budget reference accepted null bounded limit")
    excessive_budget_request = deepcopy(improve_request)
    excessive_budget_request["budget"]["max_concurrency"] = 65
    if schema_valid(excessive_budget_request, improve_request_schema, improve_schema_path):
        failures.append("nested execution-budget reference accepted excessive concurrency")

review_request = tools.get("create_review_fix_prompt", {}).get("request")
if review_request:
    invalid_review_request = deepcopy(review_request)
    invalid_review_request["review_head"] = "not-hex"
    review_schema_path = SCHEMAS / "tools/create_review_fix_prompt.schema.json"
    review_request_schema = schemas[review_schema_path]["$defs"]["request"]
    if schema_valid(invalid_review_request, review_request_schema, review_schema_path):
        failures.append("review head pattern constraint not enforced")

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
    r"(?:/Users/|/home/[^/\s]+/|/mnt/[A-Za-z]/Users/|"
    r"[A-Za-z]:\\Users\\[^\\\s]+|ghp_[A-Za-z0-9]{20,}|"
    r"github_pat_[A-Za-z0-9_]{20,}|sk-(?:proj-)?[A-Za-z0-9_-]{20,}|"
    r"BEGIN [A-Z ]*PRIVATE KEY|@(?:gmail|outlook)\.)"
)
sensitive_samples = (
    "/Users/" + "synthetic",
    "/home/" + "synthetic/work",
    "/mnt/c/" + "Users/synthetic",
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
expected_source_ids = {
    "OAI-56", "OAI-PE", "OAI-RB", "OAI-PC", "OAI-CO", "OAI-FC",
    "OAI-CS", "OAI-MG", "STAFF-1", "PRACT-1",
}
ledger_rows = {}
for line in ledger.splitlines():
    cells = [cell.strip() for cell in line.strip().strip("|").split("|")]
    if cells and re.fullmatch(r"(?:OAI-[A-Z0-9]+|STAFF-1|PRACT-1)", cells[0]):
        ledger_rows[cells[0]] = cells
for source_id in sorted(expected_source_ids):
    if ledger.count(f"| {source_id} |") != 1:
        failures.append(f"ledger mapping count invalid: {source_id}")
        continue
    cells = ledger_rows.get(source_id, [])
    if len(cells) != 6 or any(not cells[index] for index in (1, 2, 3, 4, 5)):
        failures.append(f"ledger row incomplete: {source_id}")
        continue
    for artifact in re.findall(r"`([^`]+/[^`]+)`", cells[4]):
        if not (ROOT / artifact).is_file():
            failures.append(f"ledger artifact missing: {source_id} -> {artifact}")

hash_lines = [line for line in (ROOT / "docs/contracts/source-ledger.sha256").read_text().splitlines() if line and not line.startswith("#")]
hash_rows = {}
for line in hash_lines:
    match = re.fullmatch(r"([a-f0-9]{64}|unavailable-no-source-url)  ([A-Z0-9-]+)", line)
    if not match:
        failures.append(f"invalid source hash row: {line}")
        continue
    digest, source_id = match.groups()
    if source_id in hash_rows:
        failures.append(f"duplicate source hash ID: {source_id}")
    hash_rows[source_id] = digest
if set(hash_rows) != expected_source_ids:
    failures.append("source hash IDs do not match ledger IDs")
if hash_rows.get("PRACT-1") != "unavailable-no-source-url":
    failures.append("PRACT-1 must retain explicit unavailable marker")
for source_id in expected_source_ids - {"PRACT-1"}:
    if not re.fullmatch(r"[a-f0-9]{64}", hash_rows.get(source_id, "")):
        failures.append(f"source hash missing: {source_id}")

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
