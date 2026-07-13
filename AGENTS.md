# AGENTS.md

- Keep interactions and commits extremely concise.
- Verify cwd, branch, worktree, and live GitHub state before mutation.
- Use fresh worktrees from current `origin/main`; never risk user work in cleanup.
- Keep scope issue-bound. Planning/issues do not authorize implementation.
- Use cross-platform Go architecture; macOS-first integration must stay isolated.
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
- Scan the full bug class before re-requesting one current-head review.
- Squash merge only after clean review, checks, threads, merge state, and scans.
- Version persisted changes. Tables precede indexes, FKs, and views in migrations.
- Keep README and GitHub About/topics aligned with `docs/discovery.md`.
- No release without documented closeout, provenance, changelog, and rollback plan.
