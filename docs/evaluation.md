# Governance evaluation and replay

Evaluation is local, deterministic, content-free, and advisory.

Each `eval-v1` run independently hashes:

- synthetic fixture/case;
- configuration and controlled variable;
- model snapshot/route;
- compiler version;
- recommendation/policy version;
- metric registry;
- resulting comparison.

Promotion requires an improved metric comparison and passing correctness,
completion, safety, permission, evidence, validation, stop, and privacy gates.
Metric or quality regression rolls back. Unknown/mixed evidence holds.

`replay-v1` pins the audit revision, input hash, project/source SCD2 versions,
metric/policy versions, and `as_of`. Repeated replay must produce an identical
manifest and revision hash. A source/version change is a different replay, never
an in-place rewrite.

The GPT-5.6 migration matrix changes exactly one control per case: model
snapshot/route, reasoning effort/mode, verbosity, prompt contract, tool path,
cache mode, delegation policy, or retrieval budget. Reported external efficiency
ranges are context only, never acceptance thresholds.

Opt-in local traces use only aggregate numeric inputs, coverage, workload class,
confounder labels, and opaque evidence hashes. Unknown fields—including raw
prompt, response, reasoning, payload, path, username, or database content—fail
closed. Trace loading is disabled unless explicitly enabled.
