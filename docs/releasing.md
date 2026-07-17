# Releasing

Release Please manages versions, changelog entries, tags, and GitHub Releases.

## Flow

1. Run `./scripts/run-local-ci.sh`, then merge conventional commits into `main`.
2. The hosted `Release Please` workflow opens or updates one release PR. It has
   no hosted CI dependency; project tests run only through local `act`.
3. Review that PR like any other current-head change.
4. Squash-merge the release PR when its version and changelog are correct.
5. The next workflow run creates the matching `vX.Y.Z` tag and draft GitHub
   Release. A separate conditional job checks out the exact release SHA/tag once,
   builds five archives containing four binaries each, generates per-archive SPDX
   SBOMs and checksums, creates keyless GitHub attestations, and uploads without
   replacing existing assets.
6. Verify the draft release, assets, attestations, tag target, and rollback notes.
   Issue #11 alone publishes the completed draft and Homebrew tap.

The manifest is at `0.1.0`, the contract-preview source release. Feature work
updates the open Release Please PR toward `v0.2.0`; that PR stays unmerged until
issues #4–#10 complete their implementation gates; issue #11 owns publication.
Before `v1.0.0`, a breaking change
bumps the minor version. `docs:`, `chore:`, and `ci:` commits do not create
releases by themselves.

## Boundaries

- Release PRs never bypass branch protection, review, local `act`, or secret scans.
- The workflow uses the repository `GITHUB_TOKEN`; no long-lived release or
  cross-repository Homebrew token is stored.
- Every action is pinned to a full commit SHA. GoReleaser 2.17.0 and Syft 1.48.0
  are version-pinned.
- Artifact permissions (`id-token` and `attestations`) exist only on the
  conditional package job when `release_created` is true.
- Draft assets are never rebuilt or uploaded with `--clobber`. A partial draft
  is inspected and repaired deliberately before publication.
- Direct installation requires SHA-256 and `gh attestation verify`; no signing
  key, Apple ID, Gatekeeper bypass, or `curl | sh` path is used.
- The first generated release and durable `v0.1.0` marker exist, so bootstrap
  configuration is no longer used.
