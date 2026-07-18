# ADR 0007: Normalized schema and local maintenance

- Status: accepted
- Date: 2026-07-18

## Context

The pre-release model had 47 narrowly separated tables. Prompt Better needs full
execution lineage, statistical provenance, governance replay, and retention on a
single-user laptop without operating a second analytical store.

## Decision

Use one PostgreSQL 16+ normalized relational model with 32 physical tables.
Consolidate structurally equivalent facts in constrained `execution_events` and
`observations`; retain distinct identities, effective-dated dimensions,
evidence, governance, and retention entities where their lifecycles differ. Use
dimensional views for reporting. This is a normalized temporal operational model,
not a star schema.

Polymorphism does not weaken integrity: every event/observation kind has required
typed lineage and values, bounded redacted JSON, and focused integration tests.
Plan, budget, policy, host capability, source assertion, and project attribution
evidence remains immutable and replayable.

PostgreSQL remains the only active store. Parquet may later be an optional export;
Delta Lake is excluded locally. Rely on autovacuum, targeted indexes, bounded
retention, and nonblocking post-retention `VACUUM (ANALYZE, SKIP_LOCKED)`.
Inspection is read-only and precedes maintenance. Never automate `VACUUM FULL`,
`CLUSTER`, `REINDEX`, or speculative index deletion.

Migrations are authoritative. A deterministic Go generator updates the checked-in
DBML, SVG, and bounded GitHub Mermaid diagrams in place. CI compares generated
tables, columns, types, nullability, and foreign keys with PostgreSQL's catalog.
The generator reads all ordered migration up-sections and rewrites artifacts only
when their canonical physical-model fingerprint changes.

## Consequences

The model preserves source-native units, provenance, knowledge and coverage
states, temporal history, replay hashes, privacy boundaries, and archive-gated
deletion with fewer write paths. Explicit evidence ownership keeps execution
events aligned with artifact retention; revision ownership keeps metrics,
observations, findings, recommendations, and events atomic during purge. A
configurable local byte budget defaults to 1.5 GiB. Incompatible unreleased
schemas fail closed with backup/reset guidance;
no public migration compatibility is promised before the first release.
