# ADR 0005: Local privacy and retention defaults

- Status: accepted
- Date: 2026-07-13

## Context

Prompts and execution evidence can contain source code, credentials, personal
information, and confidential context.

## Decision

Analytics are local-only and remote telemetry is disabled by default. Evidence
sources are opt-in/configured and read-only. Raw prompt retention is disabled by
default; normalized metadata, hashes, classifications, and derived facts are
preferred. Prompt Better never expands Codex permissions.

## Consequences

Reports redact and aggregate by default. Retention/deletion is policy-driven and
auditable. Public examples are synthetic. Storing raw content or adding a remote
feature requires explicit consent, security-risk review, and a new ADR.
