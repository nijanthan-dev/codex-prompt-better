-- +goose Up
SET search_path TO prompt_better, public;

CREATE TABLE projects (
    project_id uuid PRIMARY KEY,
    created_at timestamptz
);

CREATE TABLE project_versions (
    project_version_id uuid PRIMARY KEY,
    project_id uuid,
    version_number bigint,
    classification text,
    lifecycle_state text,
    version_hash bytea,
    valid_from timestamptz,
    valid_to timestamptz
);

CREATE TABLE sources (
    source_id uuid PRIMARY KEY,
    source_kind text,
    created_at timestamptz
);

CREATE TABLE source_versions (
    source_version_id uuid PRIMARY KEY,
    source_id uuid,
    version_number bigint,
    adapter_version text,
    product_surface text,
    coverage_state text,
    enabled boolean,
    version_hash bytea,
    valid_from timestamptz,
    valid_to timestamptz
);

CREATE TABLE key_versions (
    key_version_id uuid PRIMARY KEY,
    key_reference text,
    algorithm text,
    state text,
    created_at timestamptz,
    activated_at timestamptz,
    retired_at timestamptz,
    deleted_at timestamptz
);

CREATE TABLE project_aliases (
    project_alias_id uuid PRIMARY KEY,
    project_id uuid,
    source_id uuid,
    key_version_id uuid,
    alias_kind text,
    alias_digest bytea,
    created_at timestamptz,
    deleted_at timestamptz
);

CREATE TABLE project_attributions (
    project_attribution_id uuid PRIMARY KEY,
    project_id uuid,
    source_id uuid,
    evidence_artifact_id uuid,
    attribution_state text,
    confidence double precision,
    observed_at timestamptz,
    valid_from timestamptz,
    valid_to timestamptz
);

CREATE TABLE source_assertions (
    source_assertion_id uuid PRIMARY KEY,
    source_id uuid,
    project_id uuid,
    assertion_kind text,
    knowledge_state text,
    provenance text,
    asserted_at timestamptz,
    source_version text
);

CREATE TABLE collection_cursors (
    collection_cursor_id uuid PRIMARY KEY,
    source_id uuid,
    cursor_kind text,
    cursor_value text,
    cursor_state text,
    observed_at timestamptz,
    lease_expires_at timestamptz,
    updated_at timestamptz
);

CREATE TABLE policy_snapshots (
    policy_snapshot_id uuid PRIMARY KEY,
    project_id uuid,
    schema_version text,
    policy_hash bytea,
    raw_retention_enabled boolean,
    telemetry_enabled boolean,
    created_at timestamptz
);

CREATE TABLE sessions (
    session_id uuid PRIMARY KEY,
    project_id uuid,
    source_id uuid,
    started_at timestamptz,
    ended_at timestamptz,
    coverage_state text,
    knowledge_state text
);

CREATE TABLE trajectories (
    trajectory_id uuid PRIMARY KEY,
    session_id uuid,
    source_id uuid,
    external_alias_id uuid,
    parent_trajectory_id uuid,
    started_at timestamptz,
    ended_at timestamptz,
    knowledge_state text
);

CREATE TABLE turns (
    turn_id uuid PRIMARY KEY,
    trajectory_id uuid,
    source_id uuid,
    external_alias_id uuid,
    ordinal bigint,
    observed_at timestamptz,
    knowledge_state text
);

CREATE TABLE responses (
    response_id uuid PRIMARY KEY,
    turn_id uuid,
    source_id uuid,
    external_alias_id uuid,
    parent_response_id uuid,
    model_name text,
    model_variant text,
    reasoning_effort text,
    reasoning_mode text,
    reasoning_context text,
    verbosity text,
    service_mode text,
    cache_mode text,
    cache_ttl_seconds bigint,
    safeguard_outcome text,
    started_at timestamptz,
    completed_at timestamptz,
    knowledge_state text
);

CREATE TABLE items (
    item_id uuid PRIMARY KEY,
    response_id uuid,
    source_id uuid,
    external_alias_id uuid,
    item_kind text,
    assistant_phase text,
    ordinal bigint,
    image_detail text,
    content_hash bytea,
    content_length bigint,
    observed_at timestamptz,
    knowledge_state text
);

CREATE TABLE phases (
    phase_id uuid PRIMARY KEY,
    trajectory_id uuid,
    response_id uuid,
    phase_kind text,
    ordinal bigint,
    started_at timestamptz,
    ended_at timestamptz,
    knowledge_state text
);

CREATE TABLE tool_calls (
    tool_call_id uuid PRIMARY KEY,
    response_id uuid,
    item_id uuid,
    source_id uuid,
    external_alias_id uuid,
    caller_alias_id uuid,
    program_output_alias_id uuid,
    call_path text,
    tool_kind text,
    started_at timestamptz,
    completed_at timestamptz,
    outcome text,
    knowledge_state text
);

CREATE TABLE tool_loops (
    tool_loop_id uuid PRIMARY KEY,
    trajectory_id uuid,
    phase_id uuid,
    ordinal bigint,
    started_at timestamptz,
    ended_at timestamptz,
    stop_reason text,
    knowledge_state text
);

CREATE TABLE delegation_events (
    delegation_event_id uuid PRIMARY KEY,
    trajectory_id uuid,
    parent_trajectory_id uuid,
    source_id uuid,
    external_alias_id uuid,
    event_kind text,
    depth bigint,
    context_mode text,
    observed_at timestamptz,
    knowledge_state text
);

CREATE TABLE compaction_events (
    compaction_event_id uuid PRIMARY KEY,
    trajectory_id uuid,
    response_id uuid,
    source_id uuid,
    external_alias_id uuid,
    compaction_mode text,
    threshold_value bigint,
    opaque_item_hash bytea,
    observed_at timestamptz,
    knowledge_state text
);

CREATE TABLE stop_events (
    stop_event_id uuid PRIMARY KEY,
    trajectory_id uuid,
    phase_id uuid,
    event_kind text,
    outcome text,
    observed_at timestamptz,
    knowledge_state text
);

CREATE TABLE prompt_plans (
    prompt_plan_id uuid PRIMARY KEY,
    project_id uuid,
    session_id uuid,
    schema_version text,
    plan_hash bytea,
    phase_scope text,
    approval_boundary_class text,
    created_at timestamptz
);

CREATE TABLE execution_budgets (
    execution_budget_id uuid PRIMARY KEY,
    prompt_plan_id uuid,
    schema_version text,
    enforcement text,
    max_tool_loops bigint,
    max_retries bigint,
    max_retrieval_expansions bigint,
    delegation_policy text,
    max_agent_depth bigint,
    max_concurrency bigint,
    context_mode text,
    exhaustion_outcome text,
    created_at timestamptz
);

CREATE TABLE host_capability_snapshots (
    host_capability_snapshot_id uuid PRIMARY KEY,
    session_id uuid,
    source_id uuid,
    host_kind text,
    host_version text,
    capability_name text,
    capability_state text,
    observed_at timestamptz,
    knowledge_state text
);

CREATE TABLE boundaries (
    boundary_id uuid PRIMARY KEY,
    project_id uuid,
    session_id uuid,
    prompt_plan_id uuid,
    category text,
    outcome text,
    risk bigint,
    confidence double precision,
    pack_id text,
    rule_id text,
    policy_version text,
    observed_at timestamptz
);

CREATE TABLE checkpoints (
    checkpoint_id uuid PRIMARY KEY,
    session_id uuid,
    prompt_plan_id uuid,
    checkpoint_hash bytea,
    completed_count bigint,
    blocker_count bigint,
    next_action_count bigint,
    gate_count bigint,
    created_at timestamptz
);

CREATE TABLE evidence_artifacts (
    evidence_artifact_id uuid PRIMARY KEY,
    source_id uuid,
    project_id uuid,
    session_id uuid,
    external_alias_id uuid,
    schema_version text,
    content_hash bytea,
    content_length bigint,
    classification text,
    redaction_state text,
    coverage_state text,
    provenance text,
    product_surface text,
    observed_at timestamptz,
    retained_until timestamptz,
    deleted_at timestamptz
);

CREATE TABLE evidence_links (
    evidence_link_id uuid PRIMARY KEY,
    evidence_artifact_id uuid,
    target_kind text,
    target_id uuid,
    link_kind text,
    confidence double precision,
    created_at timestamptz
);

CREATE TABLE usage_observations (
    usage_observation_id uuid PRIMARY KEY,
    trajectory_id uuid,
    response_id uuid,
    evidence_artifact_id uuid,
    metric_kind text,
    value_numeric numeric,
    usage_unit text,
    product_surface text,
    accounting_regime text,
    provenance text,
    source_adapter text,
    source_version text,
    observed_at timestamptz,
    knowledge_state text
);

CREATE TABLE cache_observations (
    cache_observation_id uuid PRIMARY KEY,
    trajectory_id uuid,
    response_id uuid,
    evidence_artifact_id uuid,
    cache_kind text,
    value_numeric numeric,
    usage_unit text,
    cache_mode text,
    cache_ttl_seconds bigint,
    provenance text,
    observed_at timestamptz,
    knowledge_state text
);

CREATE TABLE audit_windows (
    audit_window_id uuid PRIMARY KEY,
    project_id uuid,
    policy_snapshot_id uuid,
    window_kind text,
    starts_at timestamptz,
    ends_at timestamptz,
    as_of timestamptz,
    timezone_name text,
    immutable_since timestamptz
);

CREATE TABLE audit_revisions (
    audit_revision_id uuid PRIMARY KEY,
    audit_window_id uuid,
    revision_number bigint,
    prior_revision_id uuid,
    source_watermark_at timestamptz,
    coverage_state text,
    revision_hash bytea,
    created_at timestamptz
);

CREATE TABLE evaluation_runs (
    evaluation_run_id uuid PRIMARY KEY,
    audit_revision_id uuid,
    evaluator_name text,
    evaluator_version text,
    started_at timestamptz,
    completed_at timestamptz,
    outcome text,
    knowledge_state text
);

CREATE TABLE metric_results (
    metric_result_id uuid PRIMARY KEY,
    audit_revision_id uuid,
    evaluation_run_id uuid,
    metric_name text,
    metric_version text,
    native_value numeric,
    native_unit text,
    numerator numeric,
    denominator numeric,
    sample_count bigint,
    coverage_state text,
    provenance text,
    computed_at timestamptz
);

CREATE TABLE findings (
    finding_id uuid PRIMARY KEY,
    audit_revision_id uuid,
    evaluation_run_id uuid,
    finding_kind text,
    detector_name text,
    detector_version text,
    confidence double precision,
    status text,
    created_at timestamptz,
    deleted_at timestamptz
);

CREATE TABLE recommendations (
    recommendation_id uuid PRIMARY KEY,
    finding_id uuid,
    project_id uuid,
    recommendation_kind text,
    lifecycle_state text,
    approval_required boolean,
    verification_kind text,
    created_at timestamptz,
    updated_at timestamptz,
    deleted_at timestamptz
);

CREATE TABLE recommendation_events (
    recommendation_event_id uuid PRIMARY KEY,
    recommendation_id uuid,
    event_kind text,
    prior_state text,
    next_state text,
    observed_at timestamptz,
    evidence_artifact_id uuid
);

CREATE TABLE confounder_labels (
    confounder_label_id uuid PRIMARY KEY,
    audit_revision_id uuid,
    trajectory_id uuid,
    label_kind text,
    label_state text,
    provenance text,
    confidence double precision,
    observed_at timestamptz
);

CREATE TABLE governance_overhead (
    governance_overhead_id uuid PRIMARY KEY,
    audit_revision_id uuid,
    trajectory_id uuid,
    overhead_kind text,
    native_value numeric,
    native_unit text,
    required_state text,
    observed_at timestamptz
);

CREATE TABLE retention_policies (
    retention_policy_id uuid PRIMARY KEY,
    project_id uuid,
    policy_version text,
    classification text,
    retain_for_seconds bigint,
    legal_hold boolean,
    created_at timestamptz,
    retired_at timestamptz
);

CREATE TABLE retention_actions (
    retention_action_id uuid PRIMARY KEY,
    retention_policy_id uuid,
    project_id uuid,
    action_key bytea,
    mode text,
    cutoff_at timestamptz,
    planned_count bigint,
    applied_count bigint,
    status text,
    started_at timestamptz,
    completed_at timestamptz
);

CREATE TABLE archive_batches (
    archive_batch_id uuid PRIMARY KEY,
    project_id uuid,
    retention_action_id uuid,
    archive_reference text,
    encryption_key_reference text,
    format_version text,
    range_start timestamptz,
    range_end timestamptz,
    entity_count bigint,
    plaintext_digest bytea,
    encrypted_digest bytea,
    status text,
    created_at timestamptz,
    verified_at timestamptz
);

CREATE TABLE archive_entities (
    archive_entity_id uuid PRIMARY KEY,
    archive_batch_id uuid,
    entity_kind text,
    entity_count bigint,
    integrity_digest bytea
);

CREATE TABLE deletion_audits (
    deletion_audit_id uuid PRIMARY KEY,
    retention_action_id uuid,
    entity_kind text,
    deleted_count bigint,
    result_hash bytea,
    recorded_at timestamptz
);

-- +goose Down
SET search_path TO prompt_better, public;
DROP TABLE deletion_audits;
DROP TABLE archive_entities;
DROP TABLE archive_batches;
DROP TABLE retention_actions;
DROP TABLE retention_policies;
DROP TABLE governance_overhead;
DROP TABLE confounder_labels;
DROP TABLE recommendation_events;
DROP TABLE recommendations;
DROP TABLE findings;
DROP TABLE metric_results;
DROP TABLE evaluation_runs;
DROP TABLE audit_revisions;
DROP TABLE audit_windows;
DROP TABLE cache_observations;
DROP TABLE usage_observations;
DROP TABLE evidence_links;
DROP TABLE evidence_artifacts;
DROP TABLE checkpoints;
DROP TABLE boundaries;
DROP TABLE host_capability_snapshots;
DROP TABLE execution_budgets;
DROP TABLE prompt_plans;
DROP TABLE stop_events;
DROP TABLE compaction_events;
DROP TABLE delegation_events;
DROP TABLE tool_loops;
DROP TABLE tool_calls;
DROP TABLE phases;
DROP TABLE items;
DROP TABLE responses;
DROP TABLE turns;
DROP TABLE trajectories;
DROP TABLE sessions;
DROP TABLE policy_snapshots;
DROP TABLE collection_cursors;
DROP TABLE source_assertions;
DROP TABLE project_attributions;
DROP TABLE project_aliases;
DROP TABLE key_versions;
DROP TABLE source_versions;
DROP TABLE sources;
DROP TABLE project_versions;
DROP TABLE projects;
