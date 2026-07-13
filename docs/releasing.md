# Releasing

Release Please manages versions, changelog entries, tags, and GitHub Releases.

## Flow

1. Merge conventional commits into `main`.
2. The `Release Please` workflow opens or updates one release PR.
3. Review that PR like any other current-head change.
4. Squash-merge the release PR when its version and changelog are correct.
5. The next workflow run creates the matching `vX.Y.Z` tag and GitHub Release.
6. Verify the release and tag target the reviewed merge commit.

The repository starts at manifest version `0.0.0`. The first `feat:` commit will
propose `v0.1.0`; `fix:` commits propose patch releases. Before `v1.0.0`, a
breaking change bumps the minor version. `docs:`, `chore:`, and `ci:` commits do
not create releases by themselves.

## Boundaries

- Release PRs never bypass branch protection, checks, review, or secret scans.
- The workflow uses the repository `GITHUB_TOKEN`; no long-lived release token is stored.
- The action is pinned to a full commit SHA.
- No package or binary is published yet. Future artifact publishing must use the
  action's `release_created` output in this workflow or explicitly revise the
  token/event design.
- Keep `bootstrap-sha` until the first generated release PR is merged; remove it
  in a later reviewed change after Release Please has a durable release marker.
