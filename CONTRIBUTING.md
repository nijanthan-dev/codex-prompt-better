# Contributing

Prompt Better is pre-alpha. Start with an issue before substantial work.

## Workflow

1. Fork or create a fresh worktree from current `origin/main`.
2. Use a conventional branch such as `feat/...`, `fix/...`, or `docs/...`.
3. Keep changes inside one issue and preserve documented privacy boundaries.
4. Use synthetic fixtures only. Never commit prompts, sessions, databases,
   credentials, host paths, usernames, tokens, or other personal data.
5. Run focused validation, then the repository's full checks when available.
6. Secret-scan the tree, history, and PR diff before requesting merge.
7. Open a focused PR with validation and privacy/security notes.

Architecture changes require an ADR. Schema changes require versioned migrations;
create tables before indexes, foreign keys, and views. Do not dual-own shared
resources across packaging or infrastructure systems.

By participating, you agree to [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
