# CLI contract

Issue #3 provides a deterministic, local-only Go CLI. It does not call a model,
execute an improved prompt, inspect Codex data, or access the network.

## Commands

- `improve_prompt`: compile plain intent or a v1 JSON request.
- `create_goal_prompt`: render the `Take this as a new goal:` house format.
- `create_review_fix_prompt`: render bounded review remediation instructions.
- `lint_prompt`: return deterministic prompt diagnostics.
- `doctor`: report sanitized integration readiness; read-only.
- `init`: preview/apply/uninstall the owned Codex integration.

Input is positional text, standard input, or `--input FILE`. `--request-json`
selects the frozen v1 request shape. Output is concise text by default;
`--format json` returns the matching v1 result or stable error object.

Source examples:

```sh
go run ./cmd/prompt-better improve_prompt 'Return a synthetic result.'
go run ./cmd/prompt-better lint_prompt --format json < synthetic-prompt.txt
```

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

`doctor` and `init` use their own explicit flags and do not read prompt input.
See [installation](installation.md) and [MCP integration](mcp.md).
