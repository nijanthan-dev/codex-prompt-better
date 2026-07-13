# ADR 0003: PostgreSQL 16+ operational store

- Status: accepted
- Date: 2026-07-13

## Context

Evidence joins, retention, replay, governance metrics, and concurrent collection
need transactional schema evolution and strong analytical queries.

## Decision

Use PostgreSQL 16+ on a supported current minor release. Manage schema only with
versioned migrations. Create tables before indexes, foreign keys, and views.

## Consequences

PostgreSQL is an explicit prerequisite and is never silently provisioned. The
project must supply least-privilege roles, retention, backup/restore, upgrade,
repair, and isolated restore validation before release.
