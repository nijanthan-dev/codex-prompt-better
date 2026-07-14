# Threat model

## Scope and trust boundaries

Assets: user intent, permissions, normalized evidence, cursors, hashes, policy,
reports, backups, and provenance. Boundaries: source/adapter, adapter/core,
core/store, Codex/MCP stdio, collector/store, report/user, package/install, and
backup/restore. Actors: user, local process, repository contributor, malicious
evidence producer, compromised tool/source, and operator error.

| Abuse case | Control | Residual risk | Owner / verification |
|---|---|---|---|
| Prompt injection in evidence | evidence is data; never instructions; classify/redact; semantic validation | persuasive sanitized text | #6 / injection golden |
| Malicious tool description/result | strict schemas, allowed tool/path, host permission dominance | compromised host tool | #7 / tool fixtures |
| Permission bypass | no approval tokens; deny unknown; host authoritative | host defect | #7/#11 / truth table |
| Budget bypass/recursive delegation | optional max loops/retries/depth/concurrency; stop outcome; default no delegation | host may not enforce advice | #3/#4 / budget goldens |
| Raw free-form sensitive payload | bounded fields; raw retention off; reject sensitive patterns | novel identifiers | #5/#6 / sensitive audit |
| Stale compaction/checkpoint state | observed timestamps, opaque compacted items, coverage gaps | unavailable host state | #6 / stale fixture |
| API/subscription metric confusion | separate product surfaces, regimes and units; unknown stays unknown | misleading upstream labels | #6/#9 / accounting fixture |
| Cursor replay/duplicate/drift | source-scoped identity, hash, monotonic cursor, quarantine | source rollback | #6 / duplicate/drift fixtures |
| Database/backup disclosure | least privilege, local storage, encryption design, retention/deletion audit | local compromise | #5/#11 / restore test |
| Report re-identification | aggregate/redact, minimum evidence, classification labels | small cohorts | #9 / privacy review |
| Supply-chain schema tampering | immutable Action SHAs, review, hashes, secret scans | compromised dependency | #11 / CI and provenance |

Verification owners must provide deterministic evidence before their issue closes.
Residual risks never justify a permission bypass or hidden host mutation.
