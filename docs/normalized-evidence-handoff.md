# Normalized evidence handoff

Issue #36 completes the persisted, content-free handoff consumed by governance
metrics. Missing source fields remain `unknown`; zero is never substituted.

| Family | Adapter attributes | Persisted target | Coverage/as-of rule |
|---|---|---|---|
| Identity | session, trajectory, task, turn, response opaque aliases | `sessions`, `trajectories`, `tasks`, `turns`, `responses` | event-time project/source SCD2 version; ambiguous task attribution remains explicit |
| Response | model variant, reasoning effort/mode, verbosity, service mode, safeguard, completion event | `responses` | absent values stay null/unknown |
| Items/phases | phase/event, output modality/size, result state | `phases`, `items`, `tool_calls` | binary/media and text sizes remain distinct native bytes |
| Tool/state | caller, call path/kind/outcome, redacted canonical call, state epoch, mutation/wait state | `tool_calls`, `state_epochs` | canonical signature is hashed after redaction; unchanged is scoped to one epoch |
| Lifecycle | checkpoint, boundary, delegation, compaction, stop event | existing normalized lifecycle tables | idempotent event identity; no raw event payload |
| Usage/cache | kind, native value/unit, product surface, accounting regime, cache mode/TTL | `usage_observations`, `cache_observations` | source-native values only; API and subscription accounting never combine |
| Self overhead | `activity_class=governance_overhead` | `governance_overhead` when an audit revision exists | reported separately; never recursively triggers collection |

Adapters parse versioned source fixtures and permit only the listed bounded
attributes. Configured collector source mappings carry the stable project UUID;
ambiguous/multi-project events omit it and retain their attribution state. The
collector converts numeric fields, hashes canonical call/state identities, and
rejects negative or malformed values before one serializable evidence/cursor
commit. Repository replay uses stable event-derived IDs and `ON CONFLICT`
identity checks.

Raw prompts, responses, reasoning, tool payloads, paths, usernames, secrets, and
database rows have no handoff field. Retention deletion continues from source
evidence into normalized and derived rows; unreferenced task identities and
state epochs are removed when the project has no retained evidence.
