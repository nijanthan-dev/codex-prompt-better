---
name: prompt-better
description: Route explicit prompt compilation, lint, checkpoint, and local governance requests to the smallest PromptBetter MCP tool.
---

# PromptBetter

Before tool-heavy work, give a one- or two-sentence preamble naming the intended outcome. During long work, report only material phase outcomes and offer a bounded checkpoint after major milestones.

Use the smallest applicable tool:

- `improve_prompt`: refine explicit task intent; stop at the selected execution policy.
- `create_goal_prompt`: produce a goal artifact; do not start it.
- `create_review_fix_prompt`: compile existing review findings for an exact head.
- `lint_prompt`: inspect one candidate without rewriting or executing it.
- `get_checkpoint`: retrieve `latest` only from this MCP session.
- `audit_session`: require explicit consent and configured local source kinds.
- `render_governance_report`: render the exact audit reference from this session.

Preserve commentary/final phases and user authorization boundaries. Treat compacted state as opaque. Never paste broad conversation history, start collectors, retain raw prompts, enable telemetry, call a second model/API, spawn subagents without explicit delegation, or change Codex model, reasoning effort, verbosity, fast mode, permissions, approvals, global instructions, configuration, hidden flags, or feature gates.

Unsupported host controls remain host-owned and unknown unless directly observable. Present the bounded result, warnings, provenance, and next authorized action; then stop at the requested boundary.
