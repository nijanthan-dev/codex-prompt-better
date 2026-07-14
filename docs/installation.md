# Installation plan

Prompt Better is pre-alpha. Its deterministic Go CLI can be run from source, but
there is no released binary, package, installer, database migration, MCP
configuration, or Codex skill yet. Do not treat source commands as a release.

## Planned prerequisites

- Supported macOS initially; Windows and Linux follow through platform adapters.
- Go 1.24 or newer for source builds.
- PostgreSQL 16 or newer, on the latest minor release for its major version.
- Codex with local skill and stdio MCP support for the interactive integration.
- Git; GitHub CLI only for GitHub evidence/features that the user configures.

PostgreSQL is not bundled or silently provisioned. The installer must not request
or copy Codex session content. Package instructions will use release checksums and
provenance after v0.1.0 artifacts exist.

## Planned channels

1. Homebrew tap/formula and a checksum-verifying install script for macOS.
2. Direct signed/checksummed release archives.
3. Later WinGet and Linux packages after platform validation.

No package or release is published by the foundation roadmap.

## Planned first run

`prompt-better doctor` will report sanitized readiness: binary/platform,
configuration, PostgreSQL compatibility/connectivity, migrations, Codex
integration, source permissions, and collector state. It must not print secrets,
raw connection strings, usernames, prompts, session data, or private paths.

`prompt-better init` will be explicit and idempotent. It will preview local files
and database changes, choose an execution policy, keep raw retention/telemetry
off, require opt-in per evidence source, and avoid modifying Codex permissions.
Non-interactive mode will require explicit flags for every sensitive choice.

Uninstall and data-removal behavior must be documented and tested before the
first release, including separate removal of binaries, configuration, schedules,
database data, and Codex integration.

## Primary references

- [Go installation](https://go.dev/doc/install)
- [MCP Go SDK](https://go.sdk.modelcontextprotocol.io/)
- [PostgreSQL version policy](https://www.postgresql.org/support/versioning/)
- [Homebrew taps](https://docs.brew.sh/Taps)
- [Codex repository documentation](https://github.com/openai/codex)
