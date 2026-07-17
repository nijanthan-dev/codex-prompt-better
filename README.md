# Prompt Better for Codex

Prompt Better is a pre-alpha local-first prompt compiler, prompt linter, and
governance toolkit for OpenAI Codex. Its cross-platform Go CLI/core, thin Codex
skill, and local MCP server are designed to turn rough intent into bounded,
reviewable instructions while measuring whether execution stayed in scope.

> **Status:** pre-alpha. The v0.1.0 contract-preview source release exists.
> Source now includes the deterministic CLI, PostgreSQL migrations and
> collectors, eight-tool stdio MCP server, thin Codex skill, doctor, and safe
> init. Verified v0.2 packaging machinery is ready, but no v0.2 artifact or
> Homebrew formula is published until the release closeout.

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
macOS-specific. A short-lived MCP process handles interactive tools. A separate
collector incrementally ingests explicitly configured local evidence. Reports
render in Codex chat, compact Markdown, or a compact table. A dashboard remains
future work.

Implemented MCP tools: `improve_prompt`, `create_goal_prompt`,
`create_review_fix_prompt`, `lint_prompt`, `get_checkpoint`, `audit_session`,
`audit_project`, and `render_governance_report`. Project audits are bounded,
explicitly consented, and computed from normalized local evidence. Checkpoints
and rendered audits are ephemeral
and isolated to one MCP client session.

See [architecture](docs/architecture.md), [data model](docs/data-model.md), and
the [governance metrics](docs/metrics.md), [evaluation/replay](docs/evaluation.md),
the [governance reporting contract](docs/reporting.md),
and [implementation roadmap](https://github.com/nijanthan-dev/codex-prompt-better/issues/1).

## Privacy

Raw prompt retention is disabled by default. Session ingestion is local and
opt-in/configured. Analytics remain local and no remote telemetry is enabled by
default. Synthetic fixtures are required in the public repository.

## Installation and usage

No v0.2 artifact or package is published yet. The prepared release contains
four binaries for five OS/architecture targets, checksums, per-archive SPDX
SBOMs, GitHub attestations, and a fail-closed installer. The ARM Homebrew
formula builds from source; Darwin amd64 remains a verified direct archive.
Developers can still run from a checkout with Go 1.25+. `prompt-better init`
previews integration changes and requires `--apply`. See
[installation](docs/installation.md), [CLI contract](docs/cli.md), and
[MCP integration](docs/mcp.md).

## Local validation

Hosted GitHub CI is disabled. Run the complete project gate locally with Docker
Desktop and [`act`](https://github.com/nektos/act):

```sh
./scripts/run-local-ci.sh
```

For a fast pre-commit gate covering formatting, recurring review patterns,
tests, vet, and contracts:

```bash
./scripts/pre-commit.sh
```

The script builds the project-specific `prompt-better-act:local` image, then
runs formatting, ShellCheck, tests, vet, race detection, integration tests,
contract validation, a native smoke test, secret scanning, cross-platform
builds, deterministic packaging, SBOM inspection, and installer lifecycle tests.
After the image is built, jobs use only the isolated Act network for local
PostgreSQL services and do not fetch packages or contact external services.

## Non-goals

- Replacing Codex reasoning, permissions, or user intent.
- Calling another LLM by default.
- Uploading prompts, session evidence, or analytics.
- Executing improved prompts or shipping a service or dashboard.

## Community

Read [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md),
[SUPPORT.md](SUPPORT.md), and [GOVERNANCE.md](GOVERNANCE.md). Repository
positioning and metadata are maintained in [docs/discovery.md](docs/discovery.md).
Release versioning is documented in [docs/releasing.md](docs/releasing.md).

## License

[MIT](LICENSE)
