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

CREATE TABLE sessions (
    session_id uuid PRIMARY KEY,
    project_id uuid,
    source_id uuid,
    started_at timestamptz,
    ended_at timestamptz,
    coverage_state text,
    knowledge_state text
);

CREATE TABLE tasks (
    task_id uuid PRIMARY KEY,
    project_id uuid,
    source_id uuid,
    external_alias_id uuid,
    task_kind text,
    attribution_state text,
    confidence double precision,
    algorithm_version text,
    observed_at timestamptz,
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
    task_id uuid,
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

CREATE TABLE state_epochs (
    state_epoch_id uuid PRIMARY KEY,
    trajectory_id uuid,
    source_id uuid,
    state_hash bytea,
    mutation_state text,
    started_at timestamptz,
    ended_at timestamptz,
    knowledge_state text
);

CREATE TABLE tool_calls (
    tool_call_id uuid PRIMARY KEY,
    response_id uuid,
    phase_id uuid,
    item_id uuid,
    state_epoch_id uuid,
    source_id uuid,
    external_alias_id uuid,
    caller_alias_id uuid,
    program_output_alias_id uuid,
    canonical_call_hash bytea,
    call_path text,
    tool_kind text,
    result_state text,
    output_modality text,
    output_size_bytes bigint,
    wait_state text,
    started_at timestamptz,
    completed_at timestamptz,
    outcome text,
    knowledge_state text
);

CREATE TABLE execution_events (
    execution_event_id uuid PRIMARY KEY,
    event_kind text,
    event_name text,
    schema_version text,
    project_id uuid,
    session_id uuid,
    trajectory_id uuid,
    parent_trajectory_id uuid,
    response_id uuid,
    phase_id uuid,
    source_id uuid,
    external_alias_id uuid,
    related_event_id uuid,
    outcome text,
    state_value text,
    depth bigint,
    numeric_value numeric,
    confidence double precision,
    content_hash bytea,
    attributes jsonb DEFAULT '{}'::jsonb,
    observed_at timestamptz,
    knowledge_state text
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

CREATE TABLE observations (
    observation_id uuid PRIMARY KEY,
    observation_kind text,
    schema_version text,
    project_id uuid,
    source_id uuid,
    trajectory_id uuid,
    response_id uuid,
    evidence_artifact_id uuid,
    audit_revision_id uuid,
    metric_kind text,
    state_value text,
    value_numeric numeric,
    native_unit text,
    product_surface text,
    accounting_regime text,
    provenance text,
    source_adapter text,
    source_version text,
    confidence double precision,
    valid_from timestamptz,
    valid_to timestamptz,
    attributes jsonb DEFAULT '{}'::jsonb,
    observed_at timestamptz,
    knowledge_state text
);

CREATE TABLE audit_windows (
    audit_window_id uuid PRIMARY KEY,
    project_id uuid,
    window_kind text,
    starts_at timestamptz,
    ends_at timestamptz,
    as_of timestamptz,
    timezone_name text,
    immutable_since timestamptz,
    policy_schema_version text,
    policy_hash bytea,
    raw_retention_enabled boolean,
    telemetry_enabled boolean
);

CREATE TABLE audit_revisions (
    audit_revision_id uuid PRIMARY KEY,
    audit_window_id uuid,
    revision_number bigint,
    prior_revision_id uuid,
    source_watermark_at timestamptz,
    coverage_state text,
    revision_hash bytea,
    engine_version text DEFAULT 'audit-v1',
    created_at timestamptz
);

CREATE TABLE evaluation_runs (
    evaluation_run_id uuid PRIMARY KEY,
    audit_revision_id uuid,
    evaluator_name text,
    evaluator_version text,
    fixture_hash bytea,
    config_hash bytea,
    model_hash bytea,
    compiler_hash bytea,
    policy_hash bytea,
    metric_hash bytea,
    run_hash bytea,
    decision text,
    started_at timestamptz,
    completed_at timestamptz,
    outcome text,
    knowledge_state text
);

CREATE TABLE metric_definitions (
    metric_definition_id uuid PRIMARY KEY,
    metric_name text,
    metric_version text,
    native_unit text,
    polarity text,
    minimum_sample bigint,
    practical_change double precision,
    definition_hash bytea,
    definition_json jsonb,
    created_at timestamptz,
    retired_at timestamptz
);

CREATE TABLE metric_results (
    metric_result_id uuid PRIMARY KEY,
    audit_revision_id uuid,
    evaluation_run_id uuid,
    metric_definition_id uuid,
    metric_name text,
    metric_version text,
    native_value numeric,
    native_unit text,
    numerator numeric,
    denominator numeric,
    sample_count bigint,
    coverage_state text,
    provenance text,
    status text,
    uncertainty text,
    previous_value numeric,
    rolling_median numeric,
    rolling_mad numeric,
    baseline_sample_count bigint DEFAULT 0,
    workload_adjusted_residual numeric,
    status_reason text,
    confidence_label text,
    exclusions jsonb DEFAULT '[]'::jsonb,
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
    cause text,
    exception_check text,
    counterevidence jsonb DEFAULT '[]'::jsonb,
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
    action_code text,
    policy_version text,
    cooldown_until timestamptz,
    evidence_revision text,
    target_surface text,
    action_text text,
    expected_movement text,
    protected_guardrails jsonb DEFAULT '[]'::jsonb,
    risks jsonb DEFAULT '[]'::jsonb,
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

CREATE TABLE retention_action_entities (
    retention_action_entity_id uuid PRIMARY KEY,
    retention_action_id uuid,
    archive_batch_id uuid,
    entity_kind text,
    planned_count bigint,
    archived_count bigint,
    deleted_count bigint,
    archive_digest bytea,
    deletion_digest bytea,
    recorded_at timestamptz
);

-- +goose Down
SET search_path TO prompt_better, public;
DROP TABLE retention_action_entities;
DROP TABLE archive_batches;
DROP TABLE retention_actions;
DROP TABLE retention_policies;
DROP TABLE recommendation_events;
DROP TABLE recommendations;
DROP TABLE findings;
DROP TABLE metric_results;
DROP TABLE metric_definitions;
DROP TABLE evaluation_runs;
DROP TABLE audit_revisions;
DROP TABLE audit_windows;
DROP TABLE observations;
DROP TABLE evidence_links;
DROP TABLE evidence_artifacts;
DROP TABLE execution_events;
DROP TABLE tool_calls;
DROP TABLE state_epochs;
DROP TABLE phases;
DROP TABLE items;
DROP TABLE responses;
DROP TABLE turns;
DROP TABLE trajectories;
DROP TABLE tasks;
DROP TABLE sessions;
DROP TABLE collection_cursors;
DROP TABLE project_aliases;
DROP TABLE key_versions;
DROP TABLE source_versions;
DROP TABLE sources;
DROP TABLE project_versions;
DROP TABLE projects;
