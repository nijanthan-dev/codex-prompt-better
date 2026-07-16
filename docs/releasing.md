# Releasing

Release Please manages versions, changelog entries, tags, and GitHub Releases.

## Flow

1. Run `./scripts/run-local-ci.sh`, then merge conventional commits into `main`.
2. The hosted `Release Please` workflow opens or updates one release PR. It has
   no hosted CI dependency; project tests run only through local `act`.
3. Review that PR like any other current-head change.
4. Squash-merge the release PR when its version and changelog are correct.
5. The next workflow run creates the matching `vX.Y.Z` tag and GitHub Release.
6. Verify the release and tag target the reviewed merge commit.

The manifest is at `0.1.0`, the contract-preview source release. Feature work
updates the open Release Please PR toward `v0.2.0`; that PR stays unmerged until
issues #4–#11 complete their release gates. Before `v1.0.0`, a breaking change
bumps the minor version. `docs:`, `chore:`, and `ci:` commits do not create
releases by themselves.

## Boundaries

- Release PRs never bypass branch protection, review, local `act`, or secret scans.
- The workflow uses the repository `GITHUB_TOKEN`; no long-lived release token is stored.
- The action is pinned to a full commit SHA.
- No package or binary is published yet. Future artifact publishing must use the
  action's `release_created` output in this workflow or explicitly revise the
  token/event design.
- The first generated release and durable `v0.1.0` marker exist, so bootstrap
  configuration is no longer used.
