# ADR 0004: On-demand stdio MCP

- Status: accepted
- Date: 2026-07-13

## Context

Codex needs local interactive tools, while evidence collection needs scheduled,
incremental work. Combining them would make tool latency and lifecycle unreliable.

## Decision

Expose tools through an on-demand local stdio MCP server using the official MCP Go
SDK. The server lives only for the host session. Run collection as a separate
scheduled process with independent cursors, retries, and failure isolation.

## Consequences

No background network listener is required. MCP startup never triggers a bulk
scan. Collector availability does not control prompt compilation. Both processes
share versioned core/storage contracts, not lifecycle ownership.
