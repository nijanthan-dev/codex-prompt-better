# Security Policy

## Supported versions

Prompt Better is pre-alpha and has no supported release.

## Reporting

Use GitHub private vulnerability reporting when enabled. If unavailable, use the
repository owner's private GitHub contact method. Do not open a public issue or
include secrets, real session data, raw prompts, database contents, or personal
information in a report.

Include impact, affected revision, reproduction with synthetic data, and a
suggested mitigation when possible. Maintainers will acknowledge valid reports,
coordinate remediation, and disclose after a fix is available.

## Security boundaries

- Prompt/session ingestion must be local, explicitly configured, least-privilege,
  and read-only at source.
- Raw prompts are not retained by default.
- No remote telemetry is enabled by default.
- Prompt Better does not bypass or replace Codex permissions.
- Public tests and examples must be synthetic.
