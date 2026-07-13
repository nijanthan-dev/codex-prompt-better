# ADR 0002: Codex-host reasoning

- Status: accepted
- Date: 2026-07-13

## Context

Prompt improvement needs reasoning, but a second model adds cost, privacy risk,
latency, and inconsistent policy behavior.

## Decision

Codex remains the reasoning host. Prompt Better provides deterministic discovery,
compilation structures, lint rules, evidence, and report contracts. It must not
call a second LLM by default. Any future model integration is optional, explicit,
off by default, and requires a new privacy/security ADR.

## Consequences

The `@PromptBetter` skill stays thin and prompts Codex to use local tools. Quality
must be testable through golden fixtures and evaluation without hidden model calls.
