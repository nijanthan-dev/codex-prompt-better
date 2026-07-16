# AGENTS.md

- Keep interactions and commits extremely concise.
- Verify cwd, branch, worktree, and live GitHub state before mutation.
- Use fresh worktrees from current `origin/main`; never risk user work in cleanup.
- Keep scope issue-bound. Planning/issues do not authorize implementation.
- Use cross-platform Go architecture; macOS-first integration must stay isolated.
- Before creating, modifying, reviewing, or validating Go source, scripts, tools,
  or tests, load and follow the applicable `golang-*` skills. Select the narrowest
  useful set; always consider code style, naming, safety, error handling, and
  testing, then add domain-specific Go skills only when the work requires them.
- Preserve local-only, opt-in ingestion; raw prompt retention and telemetry off by default.
- Never weaken Codex permissions or add a second LLM by default.
- Use synthetic fixtures only. Never expose secrets, prompts, sessions, DB data,
  host paths, usernames, tokens, or personal information.
- Prefer `rg`, bounded output, batched reads, and deterministic validation.
- Fix encountered failures. Run focused checks before full final validation.
- Secret-scan tree, history, and PR diff before push and merge.
- Pin third-party Actions to immutable SHAs. Add dependencies only when needed.
- Recheck PR head, latest reviews, threads, and checks before acting or merging.
- Eyes/ack reactions are not approval. Resolve review threads after fixes.
- Keep Codex auto-review disabled. Never request Codex review or tag `@Codex` in PRs.
- If Codex review lands unsolicited, do not request another. Check current review state once when needed.
- Scan the full bug class before resolving a review finding.
- For each new review finding, state the invariant, scan directly analogous paths, add the smallest focused regression proof, make one commit/push, then reply and resolve. Run full validation only after changes or at the final gate.
- Squash merge only after clean review, checks, threads, merge state, and scans.
- Version persisted changes. Tables precede indexes, FKs, and views in migrations.
- Keep README and GitHub About/topics aligned with `docs/discovery.md`.
- Use Conventional Commit squash titles; verify the Release Please PR, tag, and release.
- No release without documented closeout, provenance, changelog, and rollback plan.
