# Governance reporting

`render_governance_report` preserves its frozen session-summary behavior when
`revision_hash` is absent. A project, portfolio, task, or trajectory report must
name both its `audit_reference` and exact `revision_hash`; the revision must have
been returned by `audit_project` in the same MCP session. Each session retains
the eight most recent distinct revisions. Missing, mismatched, or evicted
revisions fail instead of selecting latest state.

All project formats use one normalized report model. It exposes outcome,
coverage, quality gate, scope counts, source/version state, native-unit metrics,
contributions, workload/mix effects, guardrails, findings, outliers, confounders,
at most five verification-bound actions, governance overhead excluded from the
outcome, revision identity, and provenance. Missing remains `unknown`; it is
never rendered as zero or pass. Raw evidence references are not rendered.
Privacy-suppressed contributions render their value, numerator, and denominator
as `unknown`, even when upstream persistence retains those counts.

Chat output requires the MCP client extension
`io.prompt-better/governance-report` with settings `version: "1"` and a
`formats` entry of `chat`. Absent or malformed support returns the semantically
equivalent Markdown format with a stable `fallback_reason`.

The complete JSON result is limited to 50,000 bytes. Truncation removes
confounders, outliers, findings, metrics, contributions, workload explanation,
sources, and overhead before core provenance or the highest-priority action.
`omitted_count` and `section_omissions` report every removed item. Identity,
scope, coverage, quality gate, revision, as-of time, caveats, provenance, and a
next action remain.

Every display string crosses the same UTF-8, control, ANSI/bidi, HTML, Markdown,
table, link, and directive-safe boundary. Output contains no active content or
remote resources. The optional `governance_report_export` schema is explicitly
local-only, redacted, versioned, and forbids raw evidence by construction; this
repository does not implement a dashboard or uploader.
