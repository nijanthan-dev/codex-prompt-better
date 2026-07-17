---
name: verify-release-packaging
description: Audit or change Prompt Better release packaging, installers, Homebrew formulae, release workflows, local Act validation, build metadata, SBOMs, checksums, attestations, or supported Go versions. Use for every packaging/release issue, review, fix, PR, or toolchain update.
---

# Verify Release Packaging

Treat packaging as hostile-input and failure-recovery code. Do not approve from happy-path builds.

## Required workflow

1. Read the issue, complete diff, and every changed file. List trust boundaries and mutation points before editing.
2. Read [invariants.md](references/invariants.md). Map each applicable invariant to code plus a negative regression test.
3. Run focused tests first. Run `go run ./scripts/validate_patterns.go` after any release, installer, workflow, or toolchain edit.
4. Run `codex-simplify` after behavior is complete. Preserve fail-closed behavior.
5. Run `./scripts/run-local-ci.sh` once at the final pre-PR gate. It must use an internal Docker network and the local vulnerability database snapshot.
6. Review the exact final diff through filesystem-adversary, concurrent-operator, hermetic-build, mutation-testing, supply-chain, and supportability lenses.
7. Secret-scan tree, history, diff, and generated artifacts. Recheck live main and PR head before push or merge.

## Stop conditions

Stop and fix before PR when any of these occur:

- archive extraction precedes path/type validation;
- expected targets, SBOM pairs, or checksum entries are inferred instead of exact;
- installer mutation lacks destination-scoped locking or whole-set rollback;
- an offline gate reaches DNS, package proxies, or vulnerability services;
- release and local toolchains differ or the minimum Go patch is vulnerable;
- tests assert only success and do not prove tamper, traversal, concurrency, partial-failure, or wrong-platform rejection;
- interrupted validation leaves Prompt Better containers or networks owned by a dead process.

Never weaken isolation, skip vulnerability results, permit modified destinations, add `curl | sh`, or publish release assets to make a gate pass.
