# ADR 0006: Cross-platform evidence adapters

- Status: accepted
- Date: 2026-07-13

## Context

Codex JSONL, state metadata, Git/GitHub, configuration, rollout summaries, and
process evidence vary by host and version. Direct core coupling would make the
architecture brittle and macOS-shaped.

## Decision

Use capability-scoped adapters that emit a versioned evidence envelope. Discovery
is configuration/capability based. Each adapter owns parsing and cursors; it may
be disabled independently. Targeted process evidence is explicit and optional.

## Consequences

Adapters need version fixtures, schema-drift handling, redaction, read-only source
access, idempotency, and sanitized errors. Unsupported sources yield explicit
coverage gaps, not fabricated compliance. Platform-specific scheduling and paths
remain outside normalized contracts.
