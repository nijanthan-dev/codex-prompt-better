# Collector

The collector is a separate, scheduled process. It never starts from MCP and
never mutates a source. Every adapter is disabled by default, reads a configured
bounded source, and emits evidence-envelope `1.0.0` metadata without raw content.

## Commit and recovery

Each batch has a deterministic source-scoped identity. Evidence and its next
cursor commit atomically. Replaying a committed batch is a no-op. A lease bounds
single-source ownership; failures use bounded backoff and sanitized quarantine.
Rotation or truncation creates a partial-coverage revision instead of silently
reusing a stale cursor.

Identity generation `identity-v2` maps compatible session, trajectory,
response, and tool-call aliases into canonical keyed domains before storage.
Only opaque identifiers cross the adapter boundary. Normalized lineage rows and
evidence links commit before the cursor in the same transaction. Missing parent
lineage remains unlinked with partial coverage instead of being guessed.

Evidence collected before `identity-v2` remains immutable and may be unlinked.
The next configured collection detects the versioned cursor digest change,
replays the bounded source, and writes new opaque identities. Operators should
retain or delete older evidence through the documented retention workflow; no
automatic destructive backfill is attempted.

Mutable project and source attributes are never written by adapters. They pass
through the PostgreSQL SCD2 repository with stable UUIDs. Source event time is
the effective `valid_from`; collector ingestion time remains separate and never
fills an unknown event time. Identical observations are no-ops. A changed
observation closes the current half-open range and creates one current version.
Late or out-of-order observations follow the deterministic quarantine policy
with `unknown_event_time` or `late_or_out_of_order`; they never rewrite history.

Current reads select the sole open version. Historical replay uses an explicit
event timestamp and the half-open `valid_from <= event < valid_to` predicate;
it never reads a current view. Concurrent writers serialize through the
repository so ranges cannot overlap or produce multiple current rows.

## Sources and precedence

Configuration, Git, GitHub, Codex session JSONL, Codex state SQLite,
rollout/Chronicle, and targeted process evidence are separate versioned
adapters. Source sequence/cursor and monotonic observation time outrank ambiguous
wall-clock order. Conflicts remain visible. GitHub is explicit and optional;
offline local collection continues.

Targeted process collection requires both enablement and a non-empty purpose.
It is bounded to that purpose and is off by default. Collector/audit/report/
governor records are tagged `governance_overhead`, excluded from downstream user
work by default, and never schedule another collection.

## Privacy and status

Classification and redaction happen before storage or logs. Prompt, response,
reasoning, program, payload, database content, token/secret, username, and raw
host path fields are not retained. Status exposes only source kind, enabled and
supported state, cursor/freshness, bounded counts, coverage gaps, conflicts,
ordering uncertainty, late revisions, logical host/leaf counts, and sanitized
error codes.

Native counter types and units remain separate. Derived deltas record algorithm
version, endpoints, confidence, and reset/wrap/unknown state. Missing values are
unknown, never zero. Parallel duration uses interval union; binary/native/media
size never becomes text/context tokens.
