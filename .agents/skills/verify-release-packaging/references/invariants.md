# Release packaging invariants

| Boundary | Required proof |
|---|---|
| Toolchain | `go.mod`, local Act image, release setup, docs, and runtime readiness agree on a non-vulnerable minimum patch. |
| Network | Act jobs run on an internal network. Dependencies and the current Go vulnerability database are loaded while building the image. |
| Archives | Validate normalized names, traversal, duplicates, link/type policy, and exact entries before extraction. |
| Release set | Exactly five OS/architecture archives exist. Each has one matching SPDX SBOM and one checksum entry. |
| Installer download | Require HTTPS, TLS 1.2+, checksum success, exact GitHub attestation repository, correct version, and correct platform before mutation. |
| Mutation | Lock by destination, reject symlinks/modified files, stage all four executables, and roll back the complete set on ordinary partial failure. |
| Lifecycle | Install, rollback, and uninstall share ownership checks. Forced termination fails closed and has documented recovery. |
| Homebrew | Use an ephemeral port, immutable source, four builds, no post-install configuration/collection, and full install/test/upgrade/uninstall proof. |
| Cleanup | Delete only Prompt Better Act resources whose PID owner is no longer alive. Do not disrupt concurrent validation. |
| Review | For every finding: state invariant, scan analogous paths, add a negative regression, fix once, rerun impacted tests, then final Act. |
