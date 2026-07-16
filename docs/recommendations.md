# Governance recommendations

`recommendation-v1` is deterministic and preview-only. Prompt Better never
changes instructions, models, permissions, settings, files, or external state.

Candidates require:

- an evidence-backed `avoidable` or supported `mixed` finding;
- complete applicable quality, permission, privacy, and validation guardrails;
- no existing rule that already covers the pattern;
- no active dismissal cooldown unless materially new evidence/version exists;
- a minimal target surface, expected movement, risks, and matched replay plan.

Supported policies cover unchanged-state polling, repeated calls before state
change, missing post-mutation validation, and privacy/redaction repair.
Resource use alone never causes model or reasoning-effort downgrade guidance.
Required validation/release/security work, weak evidence, workload-mix-only
movement, existing coverage, or unknown guardrails produce `no_action`.

Lifecycle states are `proposed`, `accepted`, `dismissed`, `applied_external`,
`verified`, `ineffective`, and `reverted`. Dismissal is suppressed for the
versioned cooldown. Materially new evidence may reactivate it. An externally
applied action is evaluated against a frozen matched pre-action baseline; quality
regression or no meaningful target improvement marks it ineffective or supports
revert guidance.
