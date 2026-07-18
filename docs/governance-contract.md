# Governance implementation contract

Issue #8 extends the frozen architecture and the persisted concepts from issues
#2, #5, #6, #7, #24, and #36. `audit_session` remains unchanged.
`audit_project` is additive, read-only, explicitly consented, and bounded by
scope, window, `as_of`, output limits, and opaque evidence references.

## Versioning and review

- Contract schema: `1.0.0`.
- Metric registry: `metric-v1`.
- Detector registry: `detector-v1`.
- Recommendation policy: `recommendation-v1`.
- Baseline algorithm: `baseline-v1`.
- Counting algorithm: `logical-invocation-v1`.
- Workload classifier: `workload-v1`.
- Evaluation and replay: `eval-v1` and `replay-v1`.
- Threshold or policy changes require a new version, synthetic replay evidence,
  approval in review, and a documented rollback decision.
- Persisted revisions are immutable. Late evidence creates another revision.
- A metric name/version is immutable: a changed definition hash is rejected
  rather than silently reusing persisted metadata.
- `report-facts-v1` is an optional additive `audit_project` block. It supplies
  normalized, redacted semantic facts to #9 and participates in immutable
  revision hashing; the renderer must not query storage to reconstruct them.

## Metric governance template

Every metric definition records:

1. ID, display label, version, intent, formula, numerator, denominator.
2. Native unit and polarity (`lower_better`, `higher_better`, `contextual`, or
   `guardrail`).
3. Required normalized evidence, provenance, knowledge state, and coverage.
4. Exclusions and exactly-once counting rule.
5. Minimum denominator, baseline sample, and coverage.
6. Practical-effect threshold, MAD/noise rule, hysteresis, and persistence.
7. Applicable guardrails and confounders.
8. Allowed diagnoses, limitations, misuse risk, and privacy treatment.

Ratios are aggregated from raw numerators and denominators. Unlike units are
never combined into an opaque score.

## Normalized evidence handoff

| Family | Normalized repository source | Knowledge/coverage | As-of rule |
|---|---|---|---|
| Scope identity | `projects`, `project_versions`, `tasks`, `sessions`, `trajectories`, `turns` | attribution state/confidence preserved; ambiguous and unattributed stay explicit | resolve SCD2 versions effective at event time and not newer than audit `as_of` |
| Responses/phases | `responses`, `items`, `phases` | absent completion or phase is unknown, never zero | only rows observed before source watermark |
| Tool state | `tool_calls`, `state_epochs`, lineage event tables | canonical digest is redacted/keyed; missing state epoch disables duplicate classification | only state known at the immutable watermark |
| Usage/cache | `observations` kinds `usage` and `cache` | source-native kinds/units remain separate | revisions select observations available by `as_of` |
| Runtime/waits | response/phase intervals and tool wait state | active runtime is unknown unless observable intervals are complete | interval endpoints must be known by watermark |
| Lifecycle/gates | checkpoints, boundaries, stop/delegation/compaction events | structured events only; no message-text inference | latest event at or before `as_of` |
| Confounders | source/project versions and `observations` kind `confounder` | observed/configured/unknown; never inferred from private text | matched strata pin effective version identity |
| Governance overhead | `observations` kind `governance_overhead` | excluded from user workload by default and reported separately | cannot trigger another audit or recommendation |

Task scope first restricts lineage to trajectories containing the normalized
task, then applies the task predicate to turn-linked evidence. Sibling
trajectories in the same session cannot contribute overhead, state,
confounders, evidence counts/references, or revision identity. Narrow-scope
evidence requires an explicit opaque trajectory link; session-only evidence is
not guessed into a task or trajectory. At a bounded lineage edge, a selected call
whose parent is outside the scope/window is the host for that bounded result.
Project recommendation feedback consumes project-window lifecycle rows only;
ties are resolved by audit watermark, revision, and stable identity.

The report handoff includes display identity, previous/rolling provenance,
included counts with explicit unknown exclusions, per-source freshness and
coverage, metric labels/polarity, aggregate quality-gate state, enriched
outlier/action context, and field-level knowledge/provenance. Missing exclusion
counts remain `null`; privacy-suppressed narrow scopes use an opaque identity
and never expose evidence references or inferred source knowledge.

## Status and baseline contract

- Current: requested complete comparable window.
- Previous: immediately preceding complete comparable window.
- Rolling: median of the prior seven sufficiently covered compatible windows.
- Sparse fallback: versioned last-N comparable completed task/trajectory windows.
- Optional matching: weekday, season, project phase, and workload class.
- Preserve baseline sample, missing windows, MAD, coverage, and revision identity.
- Return `insufficient` for incompatible versions, inadequate sample/coverage,
  unknown guardrails, or privacy suppression.
- Return `mixed` for contextual movement, conflicting baselines, or partly
  explained workload/confounder changes.
- Apply practical threshold, MAD/noise, hysteresis, minimum persistence,
  cooldown, and alert-noise controls before escalation.

## Privacy, safety, and permissions

No contract field may contain raw prompts, responses, reasoning, payloads,
database rows, host paths, usernames, secrets, tokens, or full source IDs.
References are opaque and bounded. Small cohorts are suppressed. Metrics and
recommendations never grant permission, execute actions, alter host settings, or
recommend model/reasoning downgrade from resource use alone.

## Step checkpoints

| Step | Required evidence | Status |
|---:|---|---|
| 1 | Dependency closure, additive contracts, templates, compatibility boundary | complete |
| 2 | Full metric/detector/recommendation registry | complete |
| 3 | Field inventory, as-of access, rollups, counting, confounders, overhead | complete |
| 4 | Six metric families with formula/table tests | complete |
| 5 | Immutable evaluation provenance and reproducibility | complete |
| 6 | Full labeled synthetic corpus and opt-in redacted trace path | complete |
| 7 | Deterministic immutable historical replay | complete |
| 8 | Controlled GPT-5.6 migration matrix | complete |
| 9 | False-positive, bias, misuse, resource, and lifecycle review | complete |
| 10 | Contract/docs/evidence reconciliation and final gates | complete |
