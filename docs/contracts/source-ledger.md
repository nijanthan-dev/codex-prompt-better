# Source coverage ledger

Checked 2026-07-14. SHA-256 values identify the retrieved Markdown content and
are refreshed when issue #11 re-audits current guidance. `official_current`
describes OpenAI API guidance; it does not assert Codex-host support.

| ID | Source | Provenance | Guidance mapped | Contract / fixture | Disposition |
|---|---|---|---|---|---|
| OAI-56 | [GPT-5.6 prompting](https://developers.openai.com/api/docs/guides/prompt-guidance-gpt-5p6) | `official_current` | Lean outcome-first prompts; preserve values; separate personality/collaboration; approval, retrieval, tool, output, validation, fallback and stop rules; PTC handoffs; phase preservation; milestone compaction; stable prefixes; eval changes | `prompt-plan.schema.json`; `execution-budget.schema.json`; golden cases | implemented/tested; model-specific numeric eval examples excluded |
| OAI-PE | [Prompt engineering](https://developers.openai.com/api/docs/guides/prompt-engineering) | `official_current` | API output is multi-item; structured output, state and compaction are API features | `capability.schema.json`; `runtime-observation.schema.json` | advisory; API-only unless observed host support |
| OAI-RB | [Reasoning best practices](https://developers.openai.com/api/docs/guides/reasoning-best-practices) | `official_current` | Simple direct prompts; avoid chain-of-thought requests; validate against representative tasks | prompt-plan invariants; `contradictory-instructions.json` | implemented/tested |
| OAI-PC | [Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching) | `official_current` | Stable prefixes; cache reads/writes remain distinct; measure rather than assume | runtime observations; `accounting-separation.json` | implemented/tested; prices not encoded |
| OAI-CO | [Compaction](https://developers.openai.com/api/docs/guides/compaction) | `official_current` | Compaction is stateful API behavior; compacted items are opaque; schedules differ from trajectory size | runtime observations; `compaction-schedules.json` | implemented/tested; thresholds not encoded |
| OAI-FC | [Function calling](https://developers.openai.com/api/docs/guides/function-calling) | `official_current` | Strict closed schemas where supported; nullable optionals; semantic validation; parallel capability explicit | base/tool schemas; validator | implemented/tested |
| OAI-CS | [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state) | `official_current` | `previous_response_id` differs from manual replay | runtime observations | implemented; API-only capability |
| OAI-MG | [GPT-5.6 model guide](https://developers.openai.com/api/docs/guides/latest-model?model=gpt-5.6) | `official_current` | Alias/variant, reasoning, PTC, multi-agent beta, image detail, safeguards and safety identifier are capability-qualified API features | capability and runtime schemas | advisory/deferred to owning implementation issues |
| STAFF-1 | [Tibo clarification](https://x.com/thsottiaux/status/2076543065045795309) | `staff_clarification` | Codex subscription accounting is not an API pricing contract | `accounting-separation.json` | contextual/tested; chart values excluded |
| PRACT-1 | Theo practitioner material referenced by issue #2 | `practitioner_hypothesis` | Trajectory efficiency may matter more than isolated prompt length | execution budget; runtime observations | hypothesis only; requires #8 eval evidence |

## Retrieval integrity

The validator requires every row to have a disposition and mapping. Retrieved
content hashes are stored in `docs/contracts/source-ledger.sha256`; PRACT-1 has
no source URL in #2 and remains explicitly unavailable rather than fabricated.
Every paragraph/list/example from OAI-56 maps to OAI-56 above as one normative
guidance family; downstream implementation must link individual evals back to
that ID. API controls remain recommendations or unknown when Codex does not
expose them.
