# CLI contract

Issue #3 provides a deterministic, local-only Go CLI. It does not call a model,
execute an improved prompt, inspect Codex data, or contact external services.
Database-backed commands may connect to the explicitly configured local
PostgreSQL socket or host.

## Commands

- `improve_prompt`: compile plain intent or a v1 JSON request.
- `create_goal_prompt`: render the `Take this as a new goal:` house format.
- `create_review_fix_prompt`: render bounded review remediation instructions.
- `lint_prompt`: return deterministic prompt diagnostics.
- `audit`: read one explicitly consented bounded governance window from the
  configured local PostgreSQL store.
- `doctor`: report sanitized integration readiness; read-only.
- `init`: preview/apply/uninstall the owned Codex integration.
- `version [--format text|json]`: report release/build provenance.

`prompt-better --version` is the text-form version alias. Source builds report
`devel`; tagged release and Homebrew builds inject the `vX.Y.Z` tag, commit, and
commit timestamp. JSON also reports the Go version, operating system, and
architecture. No wall-clock build time is recorded.

Input is positional text, standard input, or `--input FILE`. `--request-json`
selects the frozen v1 request shape. Output is concise text by default;
`--format json` returns the matching v1 result or stable error object.

Source examples:

```sh
go run ./cmd/prompt-better improve_prompt 'Return a synthetic result.'
go run ./cmd/prompt-better lint_prompt --format json < synthetic-prompt.txt
PROMPT_BETTER_DATABASE_URL='postgres://...' go run ./cmd/prompt-better audit \
  --scope project \
  --reference 00000000-0000-4000-8000-000000000001 \
  --source codex_jsonl \
  --starts-at 2026-01-01T00:00:00Z \
  --ends-at 2026-01-02T00:00:00Z \
  --as-of 2026-01-02T00:00:00Z \
  --consent \
  --format json
```

`audit` never starts collection or applies recommendations. Missing consent,
database access, coverage, or scope capability returns a stable error. It uses a
two-second database timeout and enforces the 50 KB result budget.

## Configuration

Configuration is optional JSON loaded only from an explicit `--config FILE`.
Precedence is defaults, then file, then explicitly supplied CLI flags. Provenance
is retained internally and never includes configuration values. No implicit home
directory, environment, repository, or network configuration is read.
`--show-provenance` prints field source names (`default`, `file`, or `cli`) but
never prints configuration values.

Defaults are `improve_only`, unknown host permission, 16 KiB input, two-second
timeout, and text output. Host permission always dominates policy. Unknown host
permission never authorizes execution.

## Exit codes

| Code | Meaning |
|---:|---|
| 0 | Successful deterministic result |
| 2 | Invalid command, input, schema, or configuration |
| 3 | Semantic-invalid result, including lint errors |
| 4 | Permission denied |
| 5 | Approval required as an error |
| 124 | Cancellation, timeout, or exhausted budget |
| 1 | Sanitized internal error |

Prompt bodies and configuration values are never logged. Errors identify only a
safe field or input source. Files are bounded before parsing; cancellation is
checked between compilation stages.

The v1 lint schema gained optional `location`, `rationale`, and `remediation`
fields additively. This implementation emits all three for every diagnostic.

`audit`, `doctor`, and `init` use their own explicit flags and do not read prompt input.
See [installation](installation.md) and [MCP integration](mcp.md).
