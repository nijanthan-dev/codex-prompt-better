# Privacy model

Prompt Better is local-only by default. Ingestion is explicit and configured;
sources are read-only. Raw prompts and source payloads are not retained by
default, reports aggregate/redact, backups inherit retention/classification, and
remote telemetry is off. Secrets are rejected, not stored. Optional raw retention
or remote behavior needs explicit consent, encryption/deletion design, access
audit, threat-model update, and a new ADR.

Data flows: configured local sources -> platform adapter -> normalization ->
classification/redaction -> local PostgreSQL -> local audit/report. Codex invokes
the stdio MCP separately. The collector never starts from MCP. Packaging contains
schemas and synthetic fixtures only. Deletion and backup/restore remain auditable.

Classification: `public`, `internal`, `confidential`, `restricted`, `unknown`.
Coverage and consent are explicit. Unknown classification is treated as
restricted for disclosure. API usage/cost evidence and Codex subscription units
remain separate domains and are never converted without authoritative evidence.
