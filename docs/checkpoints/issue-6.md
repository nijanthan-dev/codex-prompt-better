# Issue 6 checkpoints

Base: `af1d51800a1e3ba418a984a5d711583c8e5eaa78`.

## Step 1: contracts

- Consumes evidence envelope `1.0.0`, PostgreSQL schema version `5`, keyed-HMAC
  aliases, SCD2 project/source repositories, current/as-of rules, and existing
  source/cursor/evidence/usage/lineage tables.
- Adapters emit normalized metadata only. Raw prompts, reasoning, payloads,
  private paths, usernames, source identifiers, and secret-like values are
  rejected or redacted before persistence.
- Cursor and evidence commit is one storage transaction. Source sequence and
  monotonic observation time precede wall-clock ordering.
- Event, ingestion, and validity time remain distinct. Unknown/late event time
  quarantines; current views never answer historical replay.
- Coverage states: `complete`, `partial`, `missing`, `unknown`, with disabled
  and unsupported expressed as reasons rather than invented observations.
- Collector, audit, report, and governor evidence is `governance_overhead` and
  cannot trigger collection.

Validation: upstream contracts and migration ownership inspected. No later step
started. Next: synthetic fixtures and adapter contract tests.

## Step 2: fixtures and adapter contracts

- Added synthetic fixtures for all seven ordered sources plus malformed and
  sensitive cases.
- Added contract, disabled/unsupported/missing, malformed, truncation, replay,
  redaction, process opt-in/non-recursion, and fuzz-seed tests.

Validation: `go test ./internal/evidence ./internal/adapters` passed. No collector
runtime existed while fixtures/tests were defined. Next: collector core.

## Step 3: collector core

- Added deterministic batches, atomic evidence/cursor commits, replay
  idempotency, source leases, bounded transient retry, permanent-error
  quarantine, and sanitized failure codes.
- Added repository-only project/source SCD2 writes using source event time,
  stable identity, identical no-op/change versioning, current/as-of reads, and
  auditable unknown/late/out-of-order quarantine.
- Parser/privacy/truncation errors quarantine immediately; retrying consumed
  readers cannot turn deterministic failure into false success.

Validation: `go test -race ./internal/collector` passed. Next: ordered adapters,
precedence, ordering, deduplication, and attribution.

## Step 4: adapters and attribution

- Implemented configuration, Git, GitHub, Codex JSONL, Codex state, rollout,
  then targeted process constructors in required order.
- Added field precedence/conflict preservation, sequence/monotonic ordering,
  and worktree/nested/rename/sparse/multi-project/ambiguous attribution.

Validation: focused race suites passed. Next: lineage and normalization.

## Step 5: lineage and normalization

- Added exactly-once call/result, nested/programmatic/delegation lineage with
  host/leaf counts and late revisions.
- Added native counter delta/reset/wrap/duplicate/unknown handling, interval
  unions, modality separation, observed confounders, and overhead exclusion.

Validation: focused race suites passed. Next: separate scheduled runner.

## Step 6: scheduled runner

- Added independent one-shot runner and collector command. No MCP import or
  lifecycle ownership exists; implicit collection fails closed.
- Source failures isolate, disabled/unsupported sources skip, and governance
  overhead cannot contribute or recurse.

Validation: runner and command race suites passed. Next: sanitized status.

## Step 7: status

- Added UTC `as_of`, source enablement/support/cursor/freshness/count/coverage,
  conflict/order/revision/overhead, logical host/leaf, and bounded error status.
- Unknown diagnostics collapse to stable `adapter_failure`; private source data
  cannot enter status output.

Validation: status race suite passed. Next: PostgreSQL, performance, platform,
privacy, and full implementation validation.

## Step 8: implementation validation

- Added PostgreSQL atomic evidence/cursor commit and replay/conflict rollback
  integration proof against the existing synthetic database harness.
- Added 10,000-record bounded incremental run, cross-platform synthetic roots,
  fixture privacy scan, source isolation, race, and no-local-data proofs.

Validation: `go test -race ./...` passed. PostgreSQL version-matrix execution is
owned by the final required local `act` gate. Next: three independent reviews.

## Reviews and integrity corrections

- Correctness review: fixed unreachable command path, stateful-reader skips,
  missing PostgreSQL backend/fenced leases, rotation recovery, and equal-cursor
  conflict verification.
- Security review: replaced attribute blacklist with per-source allowlists,
  keyed opaque aliases and canonical digests; added database-time lease fencing
  and synchronized quarantine.
- Test/CI review: added collector cross-build, configured PostgreSQL command
  integration, source SCD2 parity, and representative current/as-of plans.
- Extra integrity reviews: replaced whole-file generation with stable source
  identity plus keyed prefix checkpoints; append now emits only new evidence,
  while modified/truncated prefixes atomically reset as revisions. Duplicate
  routing identities reject before mutation and disabled sources do not open.

Validation: affected race suites and full `go test -race ./...` passed. Next:
clean verification of the corrected exact diff, then simplification.

## Final gates

- Three primary reviews and three extra integrity reviews: clean after fixes.
- `codex-simplify`: ran once on changed code; affected race suites passed.
- `./scripts/run-local-ci.sh`: passed all jobs, including PostgreSQL 16/17,
  configured replacement collection, race/vet/contracts/smoke, and
  Darwin/Linux/Windows builds.
- Gitleaks: current tree, local branch history, and staged PR diff clean.

No later roadmap issue implementation started.

## Acceptance checklist

- [x] Adapter contract, drift, malformed, rotation/truncation, redaction tests.
- [x] Atomic cursor/idempotency, leases, bounded retry, sanitized quarantine.
- [x] SCD2 identical/change/late/concurrent/out-of-order invariants and as-of reads.
- [x] Explicit disabled/unsupported/missing coverage.
- [x] Native token units and versioned reset/wrap/unknown deltas.
- [x] Process adapter opt-in and purpose bounded.
- [x] Bounded large synthetic incremental run.
- [x] Attribution ambiguity/worktree/nested/rename/sparse fixtures.
- [x] Exactly-once logical lineage across source overlap.
- [x] Interval union and output-modality separation.
- [x] Observable/configured/unknown confounders only.
- [x] Governance overhead exclusion and non-recursion.
- [x] Deterministic watermarks, revisions, clock skew, ordering uncertainty.
