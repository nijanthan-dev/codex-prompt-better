# ADR 0001: Cross-platform Go CLI and core

- Status: accepted
- Date: 2026-07-13

## Context

Prompt Better needs a portable CLI, deterministic compiler/core, stdio MCP
process, collector, and packaging path. macOS integration is first, but core
contracts must not inherit macOS assumptions.

## Decision

Use Go for the CLI and core. Keep platform behavior behind small adapters selected
by build/runtime capability. Core packages accept interfaces and normalized data,
not host paths, launch-agent types, or platform process structures.

## Consequences

One toolchain can target macOS, Windows, and Linux. Platform adapters need contract
tests and synthetic fixtures. The minimum supported Go version is deferred until
implementation and must follow supported upstream releases.
