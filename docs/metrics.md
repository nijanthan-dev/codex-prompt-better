# Governance metrics

Prompt Better computes local, versioned metrics from normalized PostgreSQL
evidence. It does not parse host files during an audit, rank people, execute
recommendations, or infer missing evidence.

## Contract

`audit_project` accepts one explicitly consented `portfolio`, `project`, `task`,
or `trajectory` window with an immutable `as_of` watermark. Portfolio reference
is `all`; project, task, and trajectory references are opaque local UUIDs. Task
scope uses the opaque normalized task identity introduced by migration 6.
`audit_session` remains the frozen-v1 bounded evidence read.

Every result includes native value/unit, raw numerator/denominator, sample count,
coverage, previous value, rolling median/MAD, absolute and percentage changes,
workload-adjusted residual when available, status/reason/confidence,
uncertainty, exclusions, and evidence references. Partial, missing,
version-incompatible, privacy-suppressed, or undersized evidence returns
`insufficient`; it never becomes automatic success or failure.

## Metric registry

Version `metric-v1` defines:

| Metric | Formula | Polarity | Minimum |
|---|---|---|---:|
| `scope_attribution_coverage` | attributed completed turns / completed turns | higher better | 1 turn |
| `boundary_violation_rate` | denied or violated boundaries / observed boundaries | lower better; host denial remains authoritative | 1 decision |
| `checkpoint_completeness` | checkpoints with completion, next action, and gates / checkpoints | higher better | 1 checkpoint |
| `tokens_per_turn` | total native tokens / completed turns | contextual | 1 turn |
| `non_cached_input_per_turn` | native non-cached input / completed turns | lower better, quality-gated | 1 turn |
| `passive_waits_per_100_tool_calls` | classified passive waits / tool calls × 100 | lower better with required-wait exception | 5 calls |
| `oversized_outputs_per_100_tool_results` | text results above 20 KiB / tool results × 100 | lower better with necessary-output exception | 5 results |
| `repeated_calls_per_100_tool_calls` | unchanged repeated calls / tool calls × 100 | lower better after state-epoch checks | 5 calls |
| `commentary_per_turn` | user-visible commentary / completed turns | contextual | 1 turn |
| `wall_clock_runtime_per_turn` | elapsed completed response seconds / completed turns | contextual | 1 turn |
| `active_runtime_per_turn` | elapsed seconds excluding observable waits / completed turns | contextual | 1 turn |
| `tool_error_rate` | failed logical calls / valid logical calls | guardrail | 5 calls |
| `validation_presence` | validated mutations / observable mutations | guardrail | 1 mutation |
| `privacy_redaction_coverage` | explicitly redacted/not-needed accepted evidence / accepted evidence | guardrail | 1 artifact |

Ratios are recomputed from raw components. Unlike units are not combined into an
opaque score. API accounting, Codex subscription usage, binary/media size, and
text/context exposure remain separate.

## Findings and recommendations

Governance/self-measurement evidence is calculated and returned separately. It
does not enter user-workload metrics or recursively trigger recommendations.

Detectors are versioned as `detector-v1`. They cover scope attribution,
boundary decisions, checkpoint completeness, passive polling, repeated unchanged
calls, tool-error bursts, missing post-mutation validation, and privacy/redaction
gaps. Every finding includes cause, exception check,
classification, confidence, evidence, and counterevidence. Positive counts below
the versioned practical threshold do not produce findings.

## Baselines and status

`baseline-v1` compares the current bounded window with the immediately previous
compatible window and the median/MAD of seven earlier compatible windows. When
that fixed rolling baseline is sparse, it uses the versioned same-weekday
last-N candidates from the prior four weeks.
Project and source SCD2 identities are resolved at each evidence event time and
must be available by the immutable `as_of` watermark. Version drift makes the
comparison mixed or insufficient.

Status requires complete current evidence, at least three compatible rolling
windows, passing quality/privacy/validation guardrails, matched confounders,
practical effect beyond robust noise, and minimum persistence. Contextual metrics
and conflicting baselines remain `mixed`. Hysteresis prevents one-window alert
flapping. Task/trajectory cohorts below three completed turns are suppressed.

Portfolio contributions aggregate raw numerators and denominators by project.
They never average project ratios. Contributions below the privacy cohort floor
are withheld. Workload decomposition separately reports volume, class-mix, and
within-class effects; contradictory aggregate and within-class direction is
`simpson_mixed`. Logical host/leaf calls and results are deduplicated from keyed
lineage identities. A selected logical call with an out-of-window parent is a
host at the bounded scope edge, so host denominators remain complete.
Native-unit outliers are ordered by robust within-metric deviation while
retaining their original units; no cross-unit score exists.

## Replay and evaluation

Audit revision hashes include the normalized snapshot, metric version, findings,
and recommendation output. Identical inputs produce identical hashes. Replay
compares immutable revisions without mutating evidence. Contextual movement
returns `mixed`; lower-better metrics change only with complete comparable data.
Missing, incompatible, or insufficient metric evidence makes the aggregate
comparison `mixed`, so partial evidence cannot promote a candidate. Duplicate
metric names are rejected and non-finite values remain insufficient.

Evaluation hashes fixture, configuration, and component inputs independently.
Model, compiler, policy, and metric hashes remain separate. It promotes only an
improved comparison with passing quality gates, rolls back quality or metric
regressions, and otherwise holds. The controlled migration matrix changes one
model/reasoning/verbosity/prompt/tool/cache/runtime/delegation/retrieval control
per run.

## Privacy, retention, and rollback

Results expose opaque references only. Raw prompts, responses, reasoning, tool
payloads, paths, usernames, secrets, and database rows are excluded.
Repeatable collections are deterministically capped; `omitted_count` reports
additional rows and CLI/MCP JSON remains within the 50 KB result budget.
`metric_definitions` and governance extensions are introduced by migration 7.
Derived audit data links every accepted evidence artifact and remains subject to
project retention/deletion. Rollback refuses to discard portfolio governance
rows; export/repair those rows first or use forward repair. Database access can
be removed to disable audit without deleting source evidence.

The synthetic corpus covers good, bad, ambiguous, missing, drifted, portfolio
mix, sparse/late, nested/parallel/subagent, binary/media, governance overhead,
privacy suppression, and required release-validation cases. Local trace replay
is disabled by default and accepts only the strict content-free redacted shape.
