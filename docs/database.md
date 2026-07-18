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

The pre-release schema-v2 baseline is a clean install. No released schema or old
binary compatibility exists yet, so migrations `00001` through `00005` define
the complete baseline without checked-in upgrade/backfill scripts. A developer
with local pre-release data may use an ignored one-off export/import script or
wipe and recollect. After the first public release, migrations become immutable,
monotonic, and forward-compatible under the normal release contract.

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

- ownership: `projects`, `project_versions`, `project_aliases`, `sources`,
  `source_versions`, `key_versions`, and `collection_cursors`;
- execution: `sessions`, `trajectories`, `turns`, `responses`, `items`, `phases`,
  `tasks`, `state_epochs`, `tool_calls`, and typed `execution_events`;
- evidence: `evidence_artifacts`, `evidence_links`, and typed `observations`;
- governance: `audit_windows`, `audit_revisions`, `evaluation_runs`,
  `metric_definitions`, `metric_results`, `findings`, `recommendations`, and
  `recommendation_events`;
- operations: `retention_policies`, `retention_actions`, `archive_batches`, and
  `retention_action_entities`. Goose owns `public.schema_migrations`.

Nullable source-scoped joins preserve unmatched evidence. Knowledge and coverage
states remain explicit. Source-native usage values retain their unit, product
surface, source adapter/version, observation time, and provenance; API and Codex
subscription measurements are never converted or combined implicitly.

Each `execution_events` and `observations` kind has a database contract for its
required identity, typed values, hashes, and lineage. JSON attributes are
redacted before persistence, limited to 4 KiB, and restricted to bounded
kind-specific extensions. Plan, budget, privacy-policy, and host-capability
snapshots are immutable events. Project attribution and source assertions retain
evidence, provenance, confidence, validity, source version, and knowledge state.

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
Every application connection verifies `current_role` and rejects login roles
with superuser, database/role creation, replication, bypass-RLS, or migrator
membership. The application login must have membership only in the required
`NOLOGIN` application role and be permitted to `SET ROLE` into it. For a
URL DSN, append an encoded option such as
`options=-c%20role%3Dprompt_better_runtime` or
`options=-c%20role%3Dprompt_better_collector`. Runtime and collector roles may
read Goose migration metadata only for readiness; neither can mutate it.

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

Apply requires a successfully reread AES-256-GCM v2 archive whose plaintext
digest matches the dry-run plan. The serializable transaction locks every
planned evidence row and requires all archived metadata and current eligibility
to match exactly before deletion. Repeating the same action key returns the
original result. Archive references and key references are non-secret
digests/labels; archives contain normalized metadata, not raw prompts, reasoning,
tool payloads, host paths, or key material.
An action-key advisory lock serializes identical applies before the idempotency
lookup, so concurrent retries return the first committed deletion count.

Evidence owns execution events directly. A session and its execution graph are
deleted only after its final evidence artifact expires. Audit revisions are one
retention unit: their metrics, findings, recommendations, and events remain while
any evidence link, observation, or recommendation event in the revision still
references retained evidence. Once the unit has no retained evidence, revision
deletion cascades through the complete derived graph. The same transaction
removes empty audit windows and now-unreferenced keyed aliases and records exact
deletion counts.

Purge uses small batches to bound locks and WAL. Time-correlated high-volume
tables use low-overhead BRIN indexes alongside selective B-tree indexes. After
purge, `VACUUM (ANALYZE, SKIP_LOCKED)` refreshes visibility/statistics on
`evidence_artifacts`, cascaded `evidence_links`, and cascaded `observations`
without a blocking `VACUUM FULL`. Monthly partitioning is intentionally
deferred: current
30-day, per-project, per-classification, and legal-hold rules prevent safe whole-
partition drops. Revisit partitioning only when observed volume proves pruning
benefits and the retention key can be represented in the partition design.

The default pool is four maximum and zero minimum connections. Collection stops
before the database reaches 1.5 GiB; set `PROMPT_BETTER_MAX_DATABASE_BYTES` to a
positive byte count to choose a different cap. A transaction-scoped advisory
lock, a 16 MiB batch reserve, a conservative per-artifact allowance, and a
pre-commit physical-size recheck prevent concurrent collectors from admitting
growth against stale size readings. This is an application admission boundary;
an operating-system disk quota remains the only absolute filesystem quota.
`storage-inspect` reports evidence count and current schema bytes per retained
evidence alongside table/index bytes. `doctor` reports database bytes,
rejects a reached budget, and verifies server autovacuum is enabled. PostgreSQL
autovacuum
and explicit post-retention `VACUUM (ANALYZE, SKIP_LOCKED)` are the local
equivalents of routine optimize/vacuum maintenance. `VACUUM FULL` is excluded
from automatic paths because it blocks and temporarily needs extra disk.

PostgreSQL is the only active store. Parquet is suitable only for an optional,
portable analytics export once a measured need exists. Delta Lake adds JVM,
transaction-log, compaction, and dual-store complexity without improving this
single-user transactional workload, so it is not part of the local design.

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
After migration, change the connection reference to the runtime role option for
admin/runtime commands and to the collector role option for collection.
Use `prompt-better-admin doctor` for sanitized readiness output.
Use `prompt-better-admin storage-inspect` for a read-only JSON report of database,
relation, and index bytes; live/dead tuple estimates; last vacuum/analyze times;
and zero-scan indexes. A zero-scan index is a review signal, not proof that it
should be removed.

If `migrate` or `doctor` reports an incompatible pre-release schema, create an
encrypted backup if the local data matters, reset that local database, then run
`migrate`. Upgrade scripts for unreleased schemas remain local-only.

Run `retention-plan --policy UUID --as-of RFC3339` before each purge. Apply with
`retention-apply`, `--archive-directory`, and `--archive-key-reference`; provide
the 32-byte hex key only through `PROMPT_BETTER_ARCHIVE_KEY_HEX`. Backup and
isolated restore use `backup --database DSN --output FILE` and
`restore --database NEW_DSN --input FILE`; provide the key only through
`PROMPT_BETTER_BACKUP_KEY_HEX`. Never place keys in arguments, logs, or files.
