# Issue 8 checkpoints

## Steps 1-2

- Dependencies #5, #6, #7, and #36 closed and present on the base.
- Frozen `audit_session` remains unchanged; additive `audit_project` is bounded.
- Versioned metric, detector, recommendation, baseline, workload, evaluation,
  and replay governance contracts documented.
- Focused proof: contract validator and registry/table tests.

## Steps 3-4

- Normalized PostgreSQL access implements portfolio/project/task/trajectory
  filters, SCD2 `as_of`, raw ratio rollups, workload decomposition, logical
  host/leaf counts, interval unions, confounder matching, overhead isolation,
  privacy suppression, native-unit outliers, and late immutable revisions.
- Metric families cover scope/boundary, checkpoint, tool/validation, token,
  evidence coverage, and privacy/security.
- Focused proof: PostgreSQL integration, formula, fuzz, Simpson, lineage,
  runtime, modality, and detector tests.

## Steps 5-8

- Evaluation independently hashes fixture/config/model/compiler/policy/metric/run
  inputs and enforces promote/hold/rollback quality gates.
- Synthetic corpus covers required positive, negative, ambiguous, missing,
  drifted, portfolio-mix, sparse/late, nested/parallel, media, overhead, and
  privacy cases.
- Replay pins immutable revision and SCD2 provenance and verifies source
  immutability and idempotency.
- Controlled migration matrix changes exactly one supported variable.
- Focused proof: evaluation, corpus, redacted-trace, replay, and matrix tests.

## Step 9

- Thresholds require version change and replay evidence.
- Findings preserve exceptions and counterevidence.
- Recommendation lifecycle supports no-action, verified-rule suppression,
  dismissal/cooldown, new-evidence reactivation, and post-change verification.
- Resource movement cannot recommend model/reasoning downgrade or weaken
  permission, privacy, validation, safety, or completion guardrails.

## Step 10

- Contracts, CLI/MCP, architecture, database, metrics, evaluation, privacy,
  retention, and rollback documentation reconciled.
- Remaining gates: full validation, Simplify, dead-code audit, six review
  lenses, Act, secret scans, PR, squash merge, tracker closeout, cleanup.
