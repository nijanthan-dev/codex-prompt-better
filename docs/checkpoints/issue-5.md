# Issue 5 checkpoints

## Step 1: contracts and ownership

- Base: `9e865569020e2e484c32ba694079520d3b61af55` (`origin/main`).
- Dependency state: issues 2, 3, and 4 closed; issue 4 squash present; no
  overlapping database implementation found.
- Artifacts: `docs/database.md` records migration ownership, compatibility,
  normalized entity/join names, role boundaries, and recovery rules.
- Validation: `go test ./...`, contract validation, and diff checks passed.
- Decision: no upstream blocker. Step 2 may start; steps 3-10 were not started.

## Step 2: migration runner and roles

- Artifacts: embedded Goose migrations, PostgreSQL migration runner, explicit
  administrator-only role bootstrap, and least-privilege group roles.
- Validation: unit tests and PostgreSQL 16 integration proved role bootstrap,
  schema/version creation, repeated migration idempotency, and context handling.
- Decision: `pgx` and Goose are pinned to Go 1.24-compatible versions. Step 3 may
  start; steps 4-10 were not started.

## Step 3: base tables and primary keys

- Artifact: migration 2 creates 40 normalized tables in dependency order with
  UUID primary keys. It provides no raw-content column.
- Validation: PostgreSQL 16 fresh and version-1 upgrade paths reached version 2;
  repeated migration passed; exactly 40 tables, zero secondary indexes, zero
  foreign keys, and zero views were verified.
- Decision: unmatched joins remain nullable and unknown states remain columns.
  Step 4 may start; steps 5-10 were not started.

## Step 4: indexes

- Artifact: migration 3 adds 33 named indexes, each mapped to an identity,
  cursor, trajectory, retention, audit, metric, finding, or recommendation query.
- Validation: PostgreSQL 16 upgrade and fresh paths reached version 3 with 33
  secondary indexes, zero foreign keys, and zero views.
- Decision: plan gates assert stable index/row properties, not wall-clock time.
  Step 5 may start; steps 6-10 were not started.

## Step 5: constraints and deletion behavior

- Artifact: migration 4 adds 78 foreign keys, bounded value/count/hash checks,
  source-scoped uniqueness, and explicit cascade/set-null/restrict behavior.
- Validation: PostgreSQL 16 upgrade and fresh paths reached version 4; invalid
  classifications and orphan source joins were rejected.
- Decision: retained audit/deletion rows use restrictive ownership; normalized
  evidence and derived project data cascade. Step 6 may start; steps 7-10 were
  not started.

## Step 6: views

- Artifact: migration 5 adds seven security-barrier collection, coverage, usage,
  governance, suppression, recommendation, and retention views plus role grants.
- Validation: PostgreSQL 16 upgrade and fresh paths reached version 5 with seven
  views; base-table, index, and FK counts remained stable.
- Decision: missing denominators return NULL/missing; rare cohorts below five are
  suppressed; product/accounting/native units remain separate. Step 7 may start;
  steps 8-10 were not started.

## Step 7: repository, transactions, queries, and doctor

- Artifacts: bounded pgx pool, sanitized doctor, parameterized project/source
  SCD2 transactions, monotonic cursors, idempotent evidence, bounded COPY usage
  batches, missing-aware queries, and serializable rollback helper.
- Validation: real PostgreSQL tests cover cancellation/readiness, SCD2 as-of and
  concurrency, cursor regression, source conflict, callback rollback, 10,000-row
  batched insert, owned index plan, and bounded analytics.
- Decision: project/source mutable attributes use non-overlapping SCD2 versions;
  stable IDs remain separate. Step 8 may start; steps 9-10 were not started.

## Step 8: identity and retention

- Artifacts: encrypted local key store, keyed-HMAC aliases, re-key/lost/delete
  lifecycle, 30-day policy, bounded deterministic dry-run, encrypted-archive
  receipt gate, idempotent apply, deletion audit, alias erasure, and maintenance.
- Validation: plain hashes differ from aliases; wrong/tampered keys fail; 29/31
  day boundaries, archive-before-delete, count parity, repeated apply, and final
  keyed-alias deletion pass on PostgreSQL 16.
- Decision: each plan owns one policy/classification; legal holds never enter the
  plan. Step 9 may start; step 10 was not started.

## Step 9: upgrade, repair, backup, and restore

- Artifacts: pre-release down/forward-repair runner plus chunked AES-256-GCM
  streaming around custom-format `pg_dump`/`pg_restore`.
- Validation: migration down-to-4/up-to-5 passes; tampered/wrong-key archives
  fail; real encrypted dump restored into a separate database with matching
  schema version and SCD2 row integrity on PostgreSQL 16 and 17.
- Decision: restore never targets the source database; tool stderr, credentials,
  DSNs, and private data are not returned. Step 10 may start.

## Step 10: three local reviews

1. Schema/performance: removed redundant SCD2 current indexes; verified migration
   order, exclusion constraints, query-owned B-tree/BRIN indexes, bounded
   policy-scoped purge, legal hold, and planner proof.
2. Privacy/concurrency: verified parameterization, sanitized errors, serializable
   SCD2/rekey/retention, keyed-HMAC lifecycle, encrypted artifacts, cancellation,
   and no raw identity or prompt storage path.
3. Operations/acceptance: corrected recovery docs, added operator commands, and
   verified fresh/repair migrations, PG16/17 recovery, derived deletion,
   rare-cohort views, cross-builds, race, and offline act gates.
