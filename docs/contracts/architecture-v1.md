# Architecture contracts v1

Status: frozen for downstream issues #3–#11. This defines interfaces, not runtime
packages or migrations.

## Package and ownership boundaries

| Boundary | Owns | Must not own |
|---|---|---|
| `contracts` | versioned IRs, validation, stable errors | host calls, storage, platform paths |
| `core` | deterministic compilation and policy evaluation | permissions, scheduling, API calls |
| `platform` | capability-scoped adapters | normalized policy decisions |
| `collector` | configured read-only incremental ingestion | MCP lifecycle, implicit scans |
| `mcp` | on-demand stdio request lifecycle | daemon work, collection scheduling |
| `store` | later PostgreSQL persistence | source mutation, raw prompt default |
| `report` | redacted local rendering | remote telemetry |

Core interfaces accept normalized values. macOS, Linux, and Windows adapters own
paths, scheduling, processes, and source versions. Codex is the reasoning host;
Prompt Better sends no OpenAI API request and adds no second LLM by default.

## Lifecycles

`MCP: stopped -> starting -> serving -> draining -> stopped`; failures move to
`failed` and never start collection. `Collector: disabled -> scheduled -> running
-> checkpointed -> scheduled`; a failure becomes `degraded` and preserves its
last committed cursor. `Evidence: observed -> normalized -> classified ->
redacted -> accepted|quarantined -> retained|deleted`. Unsupported and unknown
capabilities never coerce to supported.

## Compatibility

Schemas use `1.0.0`. Additive optional fields are minor-compatible. New enum
values require consumers to preserve `unknown`; removing/renaming fields,
changing meaning/type/requiredness, or loosening a security invariant is
breaking and requires a new major schema plus migration plan. Invalid required
data fails with a stable error. Missing optional data remains absent or `unknown`;
it is never guessed. Persisted changes later require migrations; tables precede
indexes, foreign keys, and views. Unknown capability names use `name: unknown`,
preserve the source-safe name in `observed_name`, and require `state: unknown`.

## Permission and execution truth table

| Intent | Policy | Host permits | Outcome |
|---|---|---:|---|
| improve | any | any | return prompt only |
| execute | `improve_only` | yes/no | return prompt; stop |
| execute | `ask_before_execute` | yes | require approval |
| execute | `follow_user_intent` | yes | recommend in-scope execution |
| execute | any | no/unknown | deny or ask host; never bypass |
| plan/review/diagnose | any | yes | inspect/report only; no implementation |
| scope expansion/destructive/external | any | yes | explicit approval required |

Host permissions dominate every recommendation. Budgets are advisory unless an
observed host capability explicitly enforces them. Exhaustion yields `stop`,
`fallback`, `abstain`, or `approval_required`, never silent continuation.

## Stable errors

`invalid_schema`, `semantic_invalid`, `permission_denied`, `approval_required`,
`unsupported_capability`, `unknown_capability`, `coverage_incomplete`,
`source_conflict`, `budget_exhausted`, `sensitive_payload`, `not_found`, and
`internal_error`. Diagnostics are sanitized and may include field paths and
evidence references, never raw sensitive payloads.
