# Installation

Prompt Better is pre-alpha. Its v0.1.0 GitHub release freezes source contracts.
The repository contains the CLI, migrations, collectors, stdio MCP server,
source plugin, Codex skill, doctor, and safe init. v0.2 release packaging is
implemented, but no v0.2 binary, installer asset, or Homebrew formula is
published until issue #11 completes release verification.

## Source prerequisites

- Source builds are verified for macOS, Linux, and Windows on amd64, plus macOS
  and Linux on arm64. Runtime subprocess integration is exercised on the host
  platform; platform-specific collection remains adapter-dependent.
- Go 1.25 or newer for source builds.
- PostgreSQL 16 or newer, on the latest minor release for its major version.
- Codex with local skill and stdio MCP support for the interactive integration.
- Git; GitHub CLI for configured GitHub evidence and direct-archive provenance.

PostgreSQL is not bundled or silently provisioned. Package installation never
requests or copies Codex session content.

## Release channels

When the reviewed v0.2 draft is published by issue #11:

- Verified direct archives support Darwin amd64/arm64, Linux amd64/arm64, and
  Windows amd64. Each contains `prompt-better`, `prompt-better-mcp`,
  `prompt-better-collector`, `prompt-better-admin`, the license, README, and
  this installation guide.
- The maintained Homebrew formula builds all four binaries from the immutable
  source tag and supports macOS arm64 first. Darwin amd64 uses the direct archive.
- WinGet and Linux package-manager publication remain deferred.

## Verified direct installation

Download `install.sh` from the matching GitHub release, inspect it, and verify
its keyless provenance before running it:

```sh
gh attestation verify install.sh --repo nijanthan-dev/codex-prompt-better
```

Then run:

```sh
sh install.sh --version vX.Y.Z
```

The installer supports Darwin and Linux archives. Windows users verify the
amd64 archive, checksum, and attestation with GitHub CLI, then extract the four
`.exe` files directly.

Use `--install-dir /absolute/path` to override `$HOME/.local/bin`. Installation
requires `curl`, a SHA-256 tool, and GitHub CLI. Before changing the destination,
the installer verifies the archive checksum, then runs `gh attestation verify`
against `nijanthan-dev/codex-prompt-better`. Missing tools, provenance/network
failures, tampering, wrong versions, unsupported platforms, unexpected archive
entries, or unowned destination files fail closed. Do not pipe a remote script
into a shell.

One verified prior binary set is retained under
`${XDG_STATE_HOME:-$HOME/.local/state}/prompt-better`:

```sh
sh install.sh --rollback
sh install.sh --uninstall
```

Rollback and uninstall refuse modified binaries. Package uninstall removes only
the four owned executables and installer state. It does not remove Prompt Better
configuration, Codex registration, schedules, PostgreSQL data, collected
evidence, or source checkouts. `prompt-better init --uninstall` is a separate
configuration/integration operation.

## Homebrew

Issue #11 publishes `nijanthan-dev/homebrew-tap` only after the v0.2 draft assets
and attestations pass review. The formula uses Go only at build time and does no
post-install initialization, MCP registration, collection, telemetry, or data
removal. PostgreSQL remains external.

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
Marketplace publication and marketplace interface metadata remain separate work.

## Primary references

- [Go installation](https://go.dev/doc/install)
- [MCP Go SDK](https://go.sdk.modelcontextprotocol.io/)
- [PostgreSQL version policy](https://www.postgresql.org/support/versioning/)
- [Homebrew taps](https://docs.brew.sh/Taps)
- [Codex repository documentation](https://github.com/openai/codex)
