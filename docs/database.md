# Database operations

Prompt Better uses PostgreSQL 16 or newer as an explicit local prerequisite.
Migrations exclusively own persisted schema. Runtime, collector, and report code
must never create, alter, or repair schema ad hoc.

## Ownership and compatibility

The `internal/store/postgres` package owns migration execution, connection
readiness, and transactional repositories. `internal/retention` owns retention
planning and application. The `prompt-better-admin` command owns explicit
operator workflows. Evidence adapters and report renderers consume these APIs;
they do not own schema.

Migrations are immutable after merge and use monotonically increasing versions.
Each release supports a fresh install plus upgrade from every schema version
shipped by the previous minor release. Additive nullable columns and new tables
are minor-compatible. Removing, renaming, narrowing, or changing required data is
breaking and requires a new major schema contract, an explicit backfill, and a
documented repair path. Rollback is used only while a migration transaction is
uncommitted. After commit, repair is forward-only so retained evidence is not
silently destroyed.

Every migration sequence is ordered: tables and primary keys; data backfill and
validation; indexes; foreign keys and checks; views. Runtime startup refuses an
unknown, dirty, older, or newer schema. Migration credentials are never accepted
by runtime or reporting processes.

## Entity and join names

Internal joins use generated UUID primary keys. Source-native IDs are represented
only by keyed aliases unique within `(source_id, key_version_id, alias_kind,
alias_digest)`. Raw identifiers, display labels, host paths, usernames, prompt
text, reasoning, tool payloads, and secret material have no storage column.

The base model uses these normalized groups:

- ownership: `projects`, `project_versions`, `project_aliases`,
  `project_attributions`, `sources`, `source_versions`, `source_assertions`,
  `collection_cursors`;
- execution: `sessions`, `trajectories`, `turns`, `responses`, `items`, `phases`,
  `tool_calls`, `tool_loops`, `delegation_events`, `compaction_events`,
  `stop_events`, `prompt_plans`, `execution_budgets`, and
  `host_capability_snapshots`;
- evidence: `evidence_artifacts`, `evidence_links`, `usage_observations`, and
  `cache_observations`;
- governance: `audit_windows`, `audit_revisions`, `evaluation_runs`,
  `metric_results`, `findings`, `recommendations`, `recommendation_events`,
  `confounder_labels`, and `governance_overhead`;
- privacy and operations: `key_versions`, `retention_policies`,
  `retention_actions`, `archive_batches`, `archive_entities`,
  `deletion_audits`, and `schema_migrations`.

Nullable source-scoped joins preserve unmatched evidence. Knowledge and coverage
states remain explicit. Source-native usage values retain their unit, product
surface, source adapter/version, observation time, and provenance; API and Codex
subscription measurements are never converted or combined implicitly.

Migration 6 adds opaque `tasks`, redacted `state_epochs`, and bounded tool
outcome/modality/size fields. These complete the collector-to-governance handoff;
they do not compute metrics or retain raw content. Runtime/collector roles write
them; the reporter role is read-only.

Migration 7 adds versioned `metric_definitions` and reproducibility,
status/uncertainty/exclusion, and recommendation-policy fields to the existing
governance model. It does not duplicate #5 audit, metric, finding,
recommendation, confounder, or overhead concepts.

Migration 8 records the audit engine version on every immutable revision,
backfills existing rows as `audit-v1`, and partitions new audit windows by engine
version. Deploy it during a coordinated audit-writer drain because older binaries
require schema 7 readiness. The down migration refuses to discard provenance
after any non-v1 revision exists.

Migration 9 backfills trajectory evidence provenance only from explicit
usage/cache lineage. Historical session-only evidence stays session-scoped;
trajectory ownership is never inferred from current session shape.

Migration 10 computes project session and retained-evidence counts in separate
aggregates, preventing join multiplication in coverage denominators.

Audit persistence is idempotent by deterministic window/revision/result IDs.
Late evidence changes the revision hash and creates the next immutable revision.
Evaluation rows preserve separate fixture, config, model, compiler, policy,
metric, and run hashes. Portfolio windows and recommendation previews may have
no single project identity; project/task/trajectory results retain their
project lineage. Evidence links reference retained evidence artifacts only.

## SCD2 dimensions

`projects` and `sources` hold stable UUID identity only. Mutable classification,
lifecycle, adapter, product-surface, coverage, and enablement attributes live in
`project_versions` and `source_versions`. Each version has a monotonic number,
effective half-open UTC range, and deterministic hash. GiST exclusion constraints
prevent overlapping ranges and partial unique indexes permit exactly one open
version. Serializable writers lock the current row, skip identical hashes, close
changed rows, and insert the next version atomically.

Current views use the exact `valid_to IS NULL` predicate required by their partial
indexes. As-of reads use `(stable_id, valid_from DESC, valid_to)`. Tests cover
identical updates, history, non-monotonic time, concurrent changes, one-current
enforcement, and selective current/as-of plans.

## Roles

- `prompt_better_migrator`: owns schema objects and applies migrations only.
- `prompt_better_runtime`: reads and writes normalized operational data through
  parameterized repository transactions; cannot migrate or manage roles.
- `prompt_better_collector`: appends evidence and advances cursors; cannot read
  key material, governance reports, or migration metadata beyond readiness.
- `prompt_better_reporter`: reads rare-cohort-safe views only.

Role creation is an explicit administrator bootstrap. Application configuration
contains role-specific connection references, never credentials. Operators must
use verified-host TLS for remote production connections; the repository accepts
the supplied pgx DSN so local Unix sockets and isolated test networks can opt out.

## Recovery contract

Backups are encrypted outside PostgreSQL before durable storage. The command
returns plaintext/encrypted digests and byte count; the restored database carries
its migration version. Operators record creation time and the non-secret external
key reference beside the backup. The restore workflow targets a new isolated
database and runs integrity checks before access. Lost identity keys make
existing aliases intentionally unrecoverable. `RestoreFile` uses one database
transaction, but the caller must provide that isolated target, must never pass
the source DSN, and must run the documented checks. A restored database without
its separately protected key store remains non-identifying.

Each migration has a tested fresh, upgrade, failed-transaction, and forward-repair
path. Destructive repair requires explicit operator approval and a verified
encrypted backup.

## Thirty-day archive and purge

Normalized evidence defaults to 30 days per project/classification. Explicit
retention timestamps may extend or shorten that window; legal holds always win.
Dry-run plans are ordered and capped at 1,000 rows. Each plan belongs to exactly
one policy so counts and audits cannot cross classification rules.

Apply requires a successfully reread AES-256-GCM archive whose plaintext digest
matches the dry-run plan. Only then does one serializable transaction record the
archive metadata, delete linked normalized/derived data, remove now-unreferenced
keyed aliases, record exact deletion counts, and complete the action. Repeating
the same action key returns the original result. Archive references and key
references are non-secret digests/labels; archives contain normalized metadata,
not raw prompts, reasoning, tool payloads, host paths, or key material.

Purge uses small batches to bound locks and WAL. Time-correlated high-volume
tables use low-overhead BRIN indexes alongside selective B-tree indexes. After
purge, `VACUUM (ANALYZE, SKIP_LOCKED)` refreshes visibility/statistics without a
blocking `VACUUM FULL`. Monthly partitioning is intentionally deferred: current
30-day, per-project, per-classification, and legal-hold rules prevent safe whole-
partition drops. Revisit partitioning only when observed volume proves pruning
benefits and the retention key can be represented in the partition design.

## Integration validation

Database behavior is tested against real PostgreSQL 16 and 17 servers using
synthetic databases. Unit mocks do not count as persistence acceptance evidence.
The harness waits for both server readiness and a successful connection, creates
an isolated database per scenario, and always removes containers and volumes.

The matrix covers fresh and sequential upgrades, repeated/concurrent migration
execution, transactional failure rollback, forward repair, schema equivalence,
constraints and orphan rejection, cancellation and rollback, concurrent cursor
and retention operations, dry-run/apply parity and idempotency, role denial and
allowed operations, unknown-state views, rare-cohort suppression, and documented
query-plan budgets. Plans use `EXPLAIN (FORMAT JSON)` with synthetic high-volume
fixtures; timing is diagnostic, while stable node/index/row-budget assertions are
the gate.

Migration 7's down path fails safely when nullable portfolio governance rows
exist; it never silently deletes them. Export/repair first or use forward repair.

Project-audit revision numbering is serialized with a session advisory lock.
The lock and retryable serializable transaction share one pooled connection, so
waiting writers acquire their snapshot after the prior writer commits and a
one-connection pool remains supported. Cancellation, statement-level
serialization failure, or commit failure cannot leave a session lock or partial
revision. An unconfirmed unlock discards the physical connection so a session
lock is never returned to the pool. Database failures retain their
machine-classifiable cause while exposing only stable operation messages.
An existing metric name/version must retain the same definition hash; drift is
rejected and requires a new metric version.

Collection links each normalized artifact to its opaque trajectory. Task and
trajectory audits require that link for evidence counts, references, and
revision provenance; ambiguous session-only artifacts stay outside narrow scopes.

Recovery tests create an encrypted archive, prove plaintext is absent, restore
into a newly created isolated database, then compare schema version, normalized
row counts, and deterministic integrity digests. The source database is never a
restore target. These tests follow PostgreSQL's `pg_dump`/`pg_restore`,
transaction-isolation, privilege, and `EXPLAIN` guidance and Goose's provider and
session-locking guidance.

## Query and index budgets

Indexes are named for their owned query. Identity alias lookup, cursor advance,
trajectory reconstruction, evidence retention selection, audit revision lookup,
metric/finding report assembly, and recommendation lifecycle are the required
paths. Synthetic plan tests use at least 10,000 observations and require an index
or bitmap-index path for selective lookups, estimated rows no greater than twice
the requested bound, and no unbounded sort on result sets over 1,000 rows.

Small-table sequential scans are acceptable. Tests do not assert wall-clock
timing because host load is unstable. Every added index must map to one named
query above; unused speculative indexes are removed.

## Operator commands

Set `PROMPT_BETTER_DATABASE_URL` to an operator-owned connection reference. Run
`prompt-better-admin bootstrap-roles` once, then `prompt-better-admin migrate`.
Use `prompt-better-admin doctor` for sanitized readiness output.

Run `retention-plan --policy UUID --as-of RFC3339` before each purge. Apply with
`retention-apply`, `--archive-directory`, and `--archive-key-reference`; provide
the 32-byte hex key only through `PROMPT_BETTER_ARCHIVE_KEY_HEX`. Backup and
isolated restore use `backup --database DSN --output FILE` and
`restore --database NEW_DSN --input FILE`; provide the key only through
`PROMPT_BETTER_BACKUP_KEY_HEX`. Never place keys in arguments, logs, or files.
