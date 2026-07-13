# Prompt Better for Codex

Prompt Better is a planned local-first prompt compiler, prompt linter, and
governance toolkit for OpenAI Codex. Its cross-platform Go CLI/core, thin Codex
skill, and local MCP server will turn rough intent into bounded, reviewable
instructions while measuring whether execution stayed inside the requested scope.

> **Status:** pre-alpha. The engine, CLI, MCP server, collector, database,
> packages, and installers do not exist yet.

## Vision

- Improve prompts without adding a second LLM by default.
- Discover project boundaries generically instead of encoding one repository.
- Keep reasoning in Codex; expose a thin `@PromptBetter` skill and on-demand
  local stdio MCP server.
- Join prompt, execution, Git/GitHub, configuration, and governance evidence in
  PostgreSQL 16+.
- Make execution policy explicit: `improve_only`, `ask_before_execute`, or
  `follow_user_intent`. Prompt Better never replaces Codex permissions.

## Planned architecture

The cross-platform Go CLI/core will serve macOS first without making the design
macOS-specific. A short-lived MCP process will handle interactive tools. A
separate scheduled collector will incrementally ingest configured local
evidence. Reports render in Codex chat when supported, fall back to compact
Markdown/tables, and may gain an optional local dashboard later.

Planned tools: `improve_prompt`, `create_goal_prompt`,
`create_review_fix_prompt`, `lint_prompt`, `get_checkpoint`, `audit_session`,
and `render_governance_report`.

See [architecture](docs/architecture.md), [data model](docs/data-model.md), and
the [implementation roadmap](https://github.com/nijanthan-dev/codex-prompt-better/issues/1).

## Privacy

Raw prompt retention is disabled by default. Session ingestion is local and
opt-in/configured. Analytics remain local and no remote telemetry is enabled by
default. Synthetic fixtures are required in the public repository.

## Planned installation and usage

Nothing is installable yet. The intended order is a signed release artifact,
Homebrew/install script, then WinGet and Linux packages. Planned first-run
commands are `prompt-better doctor` and `prompt-better init`; names and behavior
remain design contracts until implemented. See [installation](docs/installation.md).

## Non-goals

- Replacing Codex reasoning, permissions, or user intent.
- Calling another LLM by default.
- Uploading prompts, session evidence, or analytics.
- Shipping an engine, service, dashboard, package, or release in this foundation.

## Community

Read [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md),
[SUPPORT.md](SUPPORT.md), and [GOVERNANCE.md](GOVERNANCE.md). Repository
positioning and metadata are maintained in [docs/discovery.md](docs/discovery.md).

## License

[MIT](LICENSE)
