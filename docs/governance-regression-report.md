# Governance regression report

Baseline: synthetic `metric-v1` / `detector-v1` / `recommendation-v1` /
`baseline-v1` corpus. No raw prompts or local traces are retained.

| Area | Regression invariant |
|---|---|
| Contracts | frozen `audit_session`; additive bounded `audit_project`; strict schema |
| Aggregation | raw numerator/denominator rollup; no ratio averaging |
| Workload | exact volume/mix/within-class identity; Simpson contradiction is mixed |
| Lineage/runtime | keyed exactly-once host/leaf counts; parallel intervals unioned |
| Baselines | prior/rolling/seasonal, MAD, persistence, immutable late revisions |
| Confounders | effective SCD2 and observed strata match or comparison is mixed/unknown |
| Privacy | rare cohorts suppressed; content-free references; unknown never fabricated pass |
| Recommendations | preview-only; guardrail-gated; cooldown and new-evidence semantics |
| Replay/eval | deterministic hashes; source immutability; one controlled change |
| Persistence | migration 7 up/down, FK/retention propagation, idempotent revisions |

Acceptance is zero failing deterministic tests, contract fixtures, database
integrations, local workflow jobs, or secret scans. External efficiency ranges
remain hypotheses and are never thresholds.
