# Codex stdio MCP integration

Prompt Better exposes seven frozen-v1 tools through one short-lived stdio MCP
server. Stdout contains protocol frames only. Sanitized operational failures go
to stderr. The server has no network listener, daemon, telemetry, second model,
or automatic task executor.

## Tool selection

Use the smallest applicable tool:

1. `improve_prompt` compiles rough intent under an explicit execution policy.
2. `create_goal_prompt` renders the repository goal format.
3. `create_review_fix_prompt` renders bounded review remediation instructions.
4. `lint_prompt` returns deterministic prompt diagnostics.
5. `get_checkpoint` returns only `latest`, produced by the four compiler tools.
6. `audit_session` reads an explicitly consented, allowlisted collected session.
7. `render_governance_report` renders the exact cached audit reference as chat,
   Markdown, or a compact table.

The MCP transport advertises each tool's request and result definition, not the
request/result/error union in the source schema. Transport registration contains
no compiler, policy, collector, or database business logic.

The configured execution policy is a maximum: a tool request cannot select a
broader policy. Plugin-only startup defaults to `improve_only`. The MCP host
permission remains unknown, so the server never grants or silently recommends
execution. Process-source audit also requires the configured process-purpose
scope; that purpose is never returned or logged.

## State and evidence

Checkpoint and audit render state is bounded, in memory, isolated by MCP client,
and cleared when the stdio process disconnects. It never persists raw prompts.
`audit_session` never starts collection. A bare UUID or `current:UUID` uses
current SCD2 dimensions; `as-of:RFC3339@UUID` uses event-time dimensions.
Missing or incompatible lineage remains incomplete or unknown.

`render_governance_report` accepts only the exact audit reference cached in the
same MCP session. It provides no governance metrics, diagnosis, dashboard, or
rich renderer; those remain later roadmap work.

## Bounds and errors

Requests are limited to 64 KiB. Results are limited to 50,000 encoded bytes.
The server permits four concurrent calls, rejects excess work without blocking
the reader loop, and bounds each handler to two seconds. Schema arrays and text
also retain their frozen-v1 limits; deterministic findings report omitted
counts where supported.

Stable safe errors cover invalid schema, policy/permission denial, approval,
missing references, source conflict, incomplete coverage, cancellation,
timeout, overload, unsupported/unknown capability, and internal failure. Errors
do not include prompt text, paths, DSNs, tokens, usernames, or source records.

## Setup, rollback, and unsupported behavior

`prompt-better init` previews by default; `--apply` is required. It uses
documented `codex mcp get/add/remove` commands and never edits Codex TOML or
changes Codex permissions, model, reasoning effort, verbosity, fast mode,
global instructions, or hidden flags. See [installation](installation.md).
When the source plugin is installed, init safely overlays its same-name
registration with the configured command. Uninstall removes the owned overlay
and restores plugin discovery without changing plugin files.

Rollback uses `prompt-better init --uninstall --apply`. It removes only unchanged
owned integration files and the unchanged owned MCP registration. It does not
remove PostgreSQL data, collected evidence, binaries, or source checkouts.

Marketplace entry/interface publication, package installation, release
artifacts, automatic historical backfill, governance metrics, dashboards, rich
reports, and automatic execution are not supported by this source integration;
marketplace/package work remains issue #10.
