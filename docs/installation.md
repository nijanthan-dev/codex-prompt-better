# Installation plan

Prompt Better is pre-alpha. Its v0.1.0 GitHub release freezes source contracts.
The repository contains the CLI, migrations, collectors, stdio MCP server,
source plugin, Codex skill, doctor, and safe init. There is no released binary,
package, or installer. Do not treat source commands as a published artifact.

## Source prerequisites

- Source builds are verified for macOS, Linux, and Windows on amd64, plus macOS
  and Linux on arm64. Runtime subprocess integration is exercised on the host
  platform; platform-specific collection remains adapter-dependent.
- Go 1.25 or newer for source builds.
- PostgreSQL 16 or newer, on the latest minor release for its major version.
- Codex with local skill and stdio MCP support for the interactive integration.
- Git; GitHub CLI only for GitHub evidence/features that the user configures.

PostgreSQL is not bundled or silently provisioned. The installer must not request
or copy Codex session content. Package instructions will use release checksums and
provenance after v0.2.0 artifacts exist.

## Planned channels

1. Homebrew tap/formula and a checksum-verifying install script for macOS.
2. Direct signed/checksummed release archives.
3. Later WinGet and Linux packages after platform validation.

No package or installable artifact is published before the v0.2.0 closeout.

## Source integration

Preview source setup from a checkout:

```sh
go run ./cmd/prompt-better init \
  --execution-policy improve_only \
  --source configuration \
  --source git \
  --source-root "$PWD"
```

Repeat with `--apply` only after reviewing the preview. Source and binary modes
are mutually exclusive. Binary mode uses `--server-binary FILE`. The command
accepts no shell fragments and registers only the named `promptBetter` stdio
server through `codex mcp add`.

Installing the source plugin alone provides compiler/lint tools under the safe
`improve_only` policy and disables store-backed audit access. `init --apply`
intentionally overlays that plugin registration with the versioned local config.
Uninstall removes only the owned overlay, so the plugin registration becomes
visible again. Any unrelated same-name registration remains a `source_conflict`.

Doctor is read-only:

```sh
go run ./cmd/prompt-better doctor --format text
go run ./cmd/prompt-better doctor --format json
```

It reports sanitized readiness states for platform, strict integration config,
owned skill hashes, MCP registration, PostgreSQL, configured collectors, and
unknown host capabilities. Server readiness verifies Go 1.25+, an offline
read-only module build, MCP initialize, and all eight discovered tools. Collector
readiness requires enabled current database source dimensions for every configured
source kind. It never prints paths, connection strings, usernames, prompts,
session data, or secret values.

Init writes only the versioned PromptBetter integration config, installed skill,
ownership/hash manifest, and named MCP registration. Files are atomic mode 0600.
Existing differing state fails with `source_conflict`; identical state is an
idempotent no-op. Config stores enabled source kinds, execution policy, optional
process purpose, disabled raw retention/telemetry, and the database environment
variable name. It never stores a DSN, identity key, or resolved secret.

Preview uninstall with `prompt-better init --uninstall`; add `--apply` to remove
only unchanged owned files and the owned registration. Modified owned files or
registration cause safe refusal. Uninstall does not delete databases, collected
evidence, source checkouts, or binaries.

See [MCP integration](mcp.md) for tool behavior, limits, rollback, and privacy.
Marketplace publication, marketplace interface metadata, packaged binaries, and
installer channels remain issue #10 work.

## Primary references

- [Go installation](https://go.dev/doc/install)
- [MCP Go SDK](https://go.sdk.modelcontextprotocol.io/)
- [PostgreSQL version policy](https://www.postgresql.org/support/versioning/)
- [Homebrew taps](https://docs.brew.sh/Taps)
- [Codex repository documentation](https://github.com/openai/codex)
