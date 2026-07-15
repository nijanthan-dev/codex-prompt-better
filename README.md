# Prompt Better for Codex

Prompt Better is a pre-alpha local-first prompt compiler, prompt linter, and
governance toolkit for OpenAI Codex. Its cross-platform Go CLI/core, thin Codex
skill, and local MCP server are designed to turn rough intent into bounded,
reviewable instructions while measuring whether execution stayed in scope.

> **Status:** pre-alpha. The v0.1.0 contract-preview source release and a
> deterministic Go compiler/linter CLI exist. MCP, collection, database,
> reports, installable artifacts, packages, and installers do not.

## Vision

- Improve prompts without adding a second LLM by default.
- Discover project boundaries generically instead of encoding one repository.
- Keep reasoning in Codex; expose a thin `@PromptBetter` skill and on-demand
  local stdio MCP server.
- Join prompt, execution, Git/GitHub, configuration, and governance evidence in
  PostgreSQL 16+.
- Make execution policy explicit: `improve_only`, `ask_before_execute`, or
  `follow_user_intent`. Prompt Better never replaces Codex permissions.

## Architecture

The cross-platform Go CLI/core serves macOS without making the design
macOS-specific. A short-lived MCP process will handle interactive tools. A
separate scheduled collector will incrementally ingest configured local
evidence. Reports render in Codex chat when supported, fall back to compact
Markdown/tables, and may gain an optional local dashboard later.

Implemented CLI commands: `improve_prompt`, `create_goal_prompt`,
`create_review_fix_prompt`, and `lint_prompt`. Planned integration tools:
`get_checkpoint`, `audit_session`, and `render_governance_report`.

See [architecture](docs/architecture.md), [data model](docs/data-model.md), and
the [implementation roadmap](https://github.com/nijanthan-dev/codex-prompt-better/issues/1).

## Privacy

Raw prompt retention is disabled by default. Session ingestion is local and
opt-in/configured. Analytics remain local and no remote telemetry is enabled by
default. Synthetic fixtures are required in the public repository.

## Installation and usage

No installable artifact or package is published yet. Developers can run the
source CLI with a supported Go toolchain; see [the CLI contract](docs/cli.md).
The existing v0.1.0 GitHub release freezes the source contracts only. The
intended public installation order is a signed release artifact,
Homebrew/install script, then WinGet and Linux packages. Planned first-run
commands are `prompt-better doctor` and `prompt-better init`; names and behavior
remain design contracts until implemented. See [installation](docs/installation.md).

## Non-goals

- Replacing Codex reasoning, permissions, or user intent.
- Calling another LLM by default.
- Uploading prompts, session evidence, or analytics.
- Executing improved prompts or shipping a service, dashboard, package, or release.

## Community

Read [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md),
[SUPPORT.md](SUPPORT.md), and [GOVERNANCE.md](GOVERNANCE.md). Repository
positioning and metadata are maintained in [docs/discovery.md](docs/discovery.md).
Release versioning is documented in [docs/releasing.md](docs/releasing.md).

## License

[MIT](LICENSE)
