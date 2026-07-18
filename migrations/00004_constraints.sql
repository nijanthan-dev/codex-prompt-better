-- +goose Up
SET search_path TO prompt_better, public;

ALTER TABLE projects ALTER created_at SET NOT NULL;
ALTER TABLE project_versions
    ALTER project_id SET NOT NULL, ALTER version_number SET NOT NULL,
    ALTER classification SET NOT NULL, ALTER lifecycle_state SET NOT NULL,
    ALTER version_hash SET NOT NULL, ALTER valid_from SET NOT NULL,
    ADD CONSTRAINT project_versions_number_uq UNIQUE (project_id, version_number),
    ADD CONSTRAINT project_versions_current_uq EXCLUDE USING gist
        (project_id WITH =, tstzrange(valid_from, COALESCE(valid_to, 'infinity'::timestamptz), '[)') WITH &&),
    ADD CONSTRAINT project_versions_number_ck CHECK (version_number >= 1),
    ADD CONSTRAINT project_versions_range_ck CHECK (valid_to IS NULL OR valid_to > valid_from),
    ADD CONSTRAINT project_versions_hash_ck CHECK (octet_length(version_hash) = 32),
    ADD CONSTRAINT project_versions_classification_ck CHECK (classification IN ('public','internal','confidential','restricted','unknown')),
    ADD CONSTRAINT project_versions_lifecycle_ck CHECK (lifecycle_state IN ('active','disabled','deleted','unknown')),
    ADD CONSTRAINT project_versions_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE;
CREATE UNIQUE INDEX project_versions_one_current_uq ON project_versions (project_id) WHERE valid_to IS NULL;

ALTER TABLE sources ALTER source_kind SET NOT NULL, ALTER created_at SET NOT NULL;
ALTER TABLE source_versions
    ALTER source_id SET NOT NULL, ALTER version_number SET NOT NULL,
    ALTER product_surface SET NOT NULL, ALTER coverage_state SET NOT NULL,
    ALTER enabled SET NOT NULL, ALTER version_hash SET NOT NULL,
    ALTER valid_from SET NOT NULL,
    ADD CONSTRAINT source_versions_number_uq UNIQUE (source_id, version_number),
    ADD CONSTRAINT source_versions_current_uq EXCLUDE USING gist
        (source_id WITH =, tstzrange(valid_from, COALESCE(valid_to, 'infinity'::timestamptz), '[)') WITH &&),
    ADD CONSTRAINT source_versions_number_ck CHECK (version_number >= 1),
    ADD CONSTRAINT source_versions_range_ck CHECK (valid_to IS NULL OR valid_to > valid_from),
    ADD CONSTRAINT source_versions_hash_ck CHECK (octet_length(version_hash) = 32),
    ADD CONSTRAINT source_versions_surface_ck CHECK (product_surface IN ('codex_subscription','openai_api','local','unknown')),
    ADD CONSTRAINT source_versions_coverage_ck CHECK (coverage_state IN ('complete','partial','missing','unknown')),
    ADD CONSTRAINT source_versions_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
CREATE UNIQUE INDEX source_versions_one_current_uq ON source_versions (source_id) WHERE valid_to IS NULL;

ALTER TABLE key_versions
    ALTER key_reference SET NOT NULL, ALTER algorithm SET NOT NULL,
    ALTER state SET NOT NULL, ALTER created_at SET NOT NULL,
    ADD CONSTRAINT key_versions_reference_uq UNIQUE (key_reference),
    ADD CONSTRAINT key_versions_algorithm_ck CHECK (algorithm = 'hmac-sha256'),
    ADD CONSTRAINT key_versions_state_ck CHECK (state IN ('pending','active','retired','lost','deleted')),
    ADD CONSTRAINT key_versions_no_material_ck CHECK (length(key_reference) BETWEEN 1 AND 200);

ALTER TABLE project_aliases
    ALTER project_id SET NOT NULL, ALTER source_id SET NOT NULL,
    ALTER key_version_id SET NOT NULL, ALTER alias_kind SET NOT NULL,
    ALTER alias_digest SET NOT NULL, ALTER created_at SET NOT NULL,
    ADD CONSTRAINT project_aliases_digest_ck CHECK (octet_length(alias_digest) = 32),
    ADD CONSTRAINT project_aliases_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT project_aliases_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT project_aliases_key_fk FOREIGN KEY (key_version_id) REFERENCES key_versions ON DELETE RESTRICT;
CREATE UNIQUE INDEX project_aliases_identity_uq
    ON project_aliases (source_id, key_version_id, alias_kind, alias_digest) WHERE deleted_at IS NULL;

ALTER TABLE collection_cursors
    ALTER source_id SET NOT NULL, ALTER cursor_kind SET NOT NULL,
    ALTER cursor_value SET NOT NULL, ALTER cursor_state SET NOT NULL,
    ALTER observed_at SET NOT NULL, ALTER updated_at SET NOT NULL,
    ADD CONSTRAINT collection_cursors_source_kind_uq UNIQUE (source_id, cursor_kind),
    ADD CONSTRAINT collection_cursors_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;

ALTER TABLE sessions
    ALTER source_id SET NOT NULL, ALTER started_at SET NOT NULL,
    ALTER coverage_state SET NOT NULL, ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT sessions_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE SET NULL,
    ADD CONSTRAINT sessions_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;

ALTER TABLE tasks
    ALTER source_id SET NOT NULL, ALTER task_kind SET NOT NULL,
    ALTER attribution_state SET NOT NULL, ALTER algorithm_version SET NOT NULL,
    ALTER observed_at SET NOT NULL, ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT tasks_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    ADD CONSTRAINT tasks_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT tasks_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;

ALTER TABLE trajectories
    ALTER session_id SET NOT NULL, ALTER source_id SET NOT NULL,
    ALTER started_at SET NOT NULL, ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT trajectories_session_fk FOREIGN KEY (session_id) REFERENCES sessions ON DELETE CASCADE,
    ADD CONSTRAINT trajectories_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT trajectories_parent_fk FOREIGN KEY (parent_trajectory_id) REFERENCES trajectories ON DELETE SET NULL;

ALTER TABLE turns
    ALTER trajectory_id SET NOT NULL, ALTER source_id SET NOT NULL,
    ALTER ordinal SET NOT NULL, ALTER observed_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT turns_trajectory_ordinal_uq UNIQUE (trajectory_id, ordinal),
    ADD CONSTRAINT turns_ordinal_ck CHECK (ordinal >= 0),
    ADD CONSTRAINT turns_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT turns_task_fk FOREIGN KEY (task_id) REFERENCES tasks ON DELETE SET NULL,
    ADD CONSTRAINT turns_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;

ALTER TABLE responses
    ALTER turn_id SET NOT NULL, ALTER source_id SET NOT NULL,
    ALTER started_at SET NOT NULL, ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT responses_turn_fk FOREIGN KEY (turn_id) REFERENCES turns ON DELETE CASCADE,
    ADD CONSTRAINT responses_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT responses_parent_fk FOREIGN KEY (parent_response_id) REFERENCES responses ON DELETE SET NULL;
CREATE UNIQUE INDEX responses_source_alias_uq ON responses (source_id, external_alias_id) WHERE external_alias_id IS NOT NULL;

ALTER TABLE items
    ALTER response_id SET NOT NULL, ALTER source_id SET NOT NULL,
    ALTER item_kind SET NOT NULL, ALTER ordinal SET NOT NULL,
    ALTER observed_at SET NOT NULL, ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT items_response_ordinal_uq UNIQUE (response_id, ordinal),
    ADD CONSTRAINT items_content_hash_ck CHECK (content_hash IS NULL OR octet_length(content_hash) = 32),
    ADD CONSTRAINT items_content_length_ck CHECK (content_length IS NULL OR content_length >= 0),
    ADD CONSTRAINT items_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE CASCADE,
    ADD CONSTRAINT items_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;

ALTER TABLE phases
    ALTER trajectory_id SET NOT NULL, ALTER phase_kind SET NOT NULL,
    ALTER ordinal SET NOT NULL, ALTER started_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT phases_trajectory_ordinal_uq UNIQUE (trajectory_id, ordinal),
    ADD CONSTRAINT phases_range_ck CHECK (ended_at IS NULL OR ended_at >= started_at),
    ADD CONSTRAINT phases_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT phases_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE SET NULL;

ALTER TABLE state_epochs
    ALTER trajectory_id SET NOT NULL, ALTER source_id SET NOT NULL,
    ALTER state_hash SET NOT NULL, ALTER mutation_state SET NOT NULL,
    ALTER started_at SET NOT NULL, ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT state_epochs_hash_ck CHECK (octet_length(state_hash) = 32),
    ADD CONSTRAINT state_epochs_range_ck CHECK (ended_at IS NULL OR ended_at >= started_at),
    ADD CONSTRAINT state_epochs_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT state_epochs_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;

ALTER TABLE tool_calls
    ALTER response_id SET NOT NULL, ALTER source_id SET NOT NULL,
    ALTER call_path SET NOT NULL, ALTER tool_kind SET NOT NULL,
    ALTER started_at SET NOT NULL, ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT tool_calls_path_ck CHECK (call_path IN ('direct','programmatic','unknown')),
    ADD CONSTRAINT tool_calls_call_hash_ck CHECK (canonical_call_hash IS NULL OR octet_length(canonical_call_hash) = 32),
    ADD CONSTRAINT tool_calls_output_size_ck CHECK (output_size_bytes IS NULL OR output_size_bytes >= 0),
    ADD CONSTRAINT tool_calls_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE CASCADE,
    ADD CONSTRAINT tool_calls_phase_fk FOREIGN KEY (phase_id) REFERENCES phases ON DELETE SET NULL,
    ADD CONSTRAINT tool_calls_item_fk FOREIGN KEY (item_id) REFERENCES items ON DELETE SET NULL,
    ADD CONSTRAINT tool_calls_epoch_fk FOREIGN KEY (state_epoch_id) REFERENCES state_epochs ON DELETE SET NULL,
    ADD CONSTRAINT tool_calls_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
CREATE UNIQUE INDEX tool_calls_source_alias_uq ON tool_calls (source_id, external_alias_id) WHERE external_alias_id IS NOT NULL;

ALTER TABLE execution_events
    ALTER event_kind SET NOT NULL, ALTER schema_version SET NOT NULL,
    ALTER evidence_artifact_id SET NOT NULL,
    ALTER attributes SET NOT NULL, ALTER observed_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT execution_events_kind_ck CHECK (event_kind IN
        ('delegation','compaction','stop','plan_snapshot','budget_snapshot','policy_snapshot','host_capability','boundary','checkpoint')),
    ADD CONSTRAINT execution_events_depth_ck CHECK (depth IS NULL OR depth >= 0),
    ADD CONSTRAINT execution_events_numeric_ck CHECK (numeric_value IS NULL OR numeric_value >= 0),
    ADD CONSTRAINT execution_events_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    ADD CONSTRAINT execution_events_hash_ck CHECK (content_hash IS NULL OR octet_length(content_hash) = 32),
    ADD CONSTRAINT execution_events_attributes_ck CHECK (
        jsonb_typeof(attributes) = 'object' AND pg_column_size(attributes) <= 4096),
    ADD CONSTRAINT execution_events_kind_contract_ck CHECK (CASE event_kind
        WHEN 'delegation' THEN trajectory_id IS NOT NULL AND source_id IS NOT NULL AND event_name IS NOT NULL
        WHEN 'compaction' THEN trajectory_id IS NOT NULL AND source_id IS NOT NULL AND event_name IS NOT NULL
        WHEN 'stop' THEN trajectory_id IS NOT NULL AND event_name IS NOT NULL AND outcome IS NOT NULL
        WHEN 'plan_snapshot' THEN project_id IS NOT NULL AND content_hash IS NOT NULL
            AND event_name IS NOT NULL AND outcome IS NOT NULL
        WHEN 'budget_snapshot' THEN related_event_id IS NOT NULL AND event_name IS NOT NULL
            AND state_value IS NOT NULL AND outcome IS NOT NULL
        WHEN 'policy_snapshot' THEN project_id IS NOT NULL AND content_hash IS NOT NULL
            AND event_name IS NOT NULL
            AND jsonb_typeof(attributes->'raw_retention_enabled') = 'boolean'
            AND jsonb_typeof(attributes->'telemetry_enabled') = 'boolean'
        WHEN 'host_capability' THEN session_id IS NOT NULL AND source_id IS NOT NULL
            AND event_name IS NOT NULL AND state_value IS NOT NULL
        WHEN 'boundary' THEN event_name IS NOT NULL AND outcome IS NOT NULL
        WHEN 'checkpoint' THEN session_id IS NOT NULL AND content_hash IS NOT NULL
        ELSE false END),
    ADD CONSTRAINT execution_events_budget_attributes_ck CHECK (event_kind <> 'budget_snapshot' OR (
        (NOT attributes ? 'max_tool_loops' OR
            (jsonb_typeof(attributes->'max_tool_loops')='number' AND (attributes->>'max_tool_loops')::numeric >= 0)) AND
        (NOT attributes ? 'max_retries' OR
            (jsonb_typeof(attributes->'max_retries')='number' AND (attributes->>'max_retries')::numeric >= 0)) AND
        (NOT attributes ? 'max_retrieval_expansions' OR
            (jsonb_typeof(attributes->'max_retrieval_expansions')='number' AND (attributes->>'max_retrieval_expansions')::numeric >= 0)) AND
        (NOT attributes ? 'max_agent_depth' OR
            (jsonb_typeof(attributes->'max_agent_depth')='number' AND (attributes->>'max_agent_depth')::numeric >= 0)) AND
        (NOT attributes ? 'max_concurrency' OR
            (jsonb_typeof(attributes->'max_concurrency')='number' AND (attributes->>'max_concurrency')::numeric >= 1)))),
    ADD CONSTRAINT execution_events_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT execution_events_session_fk FOREIGN KEY (session_id) REFERENCES sessions ON DELETE CASCADE,
    ADD CONSTRAINT execution_events_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT execution_events_parent_trajectory_fk FOREIGN KEY (parent_trajectory_id) REFERENCES trajectories ON DELETE SET NULL,
    ADD CONSTRAINT execution_events_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE SET NULL,
    ADD CONSTRAINT execution_events_phase_fk FOREIGN KEY (phase_id) REFERENCES phases ON DELETE SET NULL,
    ADD CONSTRAINT execution_events_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT execution_events_evidence_fk FOREIGN KEY (evidence_artifact_id) REFERENCES evidence_artifacts ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT execution_events_related_fk FOREIGN KEY (related_event_id) REFERENCES execution_events ON DELETE SET NULL;

ALTER TABLE evidence_artifacts
    ALTER source_id SET NOT NULL, ALTER schema_version SET NOT NULL,
    ALTER content_hash SET NOT NULL, ALTER content_length SET NOT NULL,
    ALTER classification SET NOT NULL, ALTER redaction_state SET NOT NULL,
    ALTER coverage_state SET NOT NULL, ALTER provenance SET NOT NULL,
    ALTER product_surface SET NOT NULL, ALTER observed_at SET NOT NULL,
    ADD CONSTRAINT evidence_artifacts_hash_ck CHECK (octet_length(content_hash) = 32),
    ADD CONSTRAINT evidence_artifacts_length_ck CHECK (content_length >= 0),
    ADD CONSTRAINT evidence_artifacts_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT evidence_artifacts_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT evidence_artifacts_session_fk FOREIGN KEY (session_id) REFERENCES sessions ON DELETE CASCADE;
CREATE UNIQUE INDEX evidence_artifacts_source_alias_uq ON evidence_artifacts (source_id, external_alias_id)
    WHERE external_alias_id IS NOT NULL;

ALTER TABLE evidence_links
    ALTER evidence_artifact_id SET NOT NULL, ALTER target_kind SET NOT NULL,
    ALTER target_id SET NOT NULL, ALTER link_kind SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT evidence_links_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    ADD CONSTRAINT evidence_links_artifact_fk FOREIGN KEY (evidence_artifact_id) REFERENCES evidence_artifacts ON DELETE CASCADE;

ALTER TABLE observations
    ALTER observation_kind SET NOT NULL, ALTER schema_version SET NOT NULL,
    ALTER provenance SET NOT NULL, ALTER attributes SET NOT NULL,
    ALTER observed_at SET NOT NULL, ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT observations_kind_ck CHECK (observation_kind IN
        ('usage','cache','confounder','governance_overhead','project_attribution','source_assertion')),
    ADD CONSTRAINT observations_value_ck CHECK (value_numeric IS NULL OR value_numeric >= 0),
    ADD CONSTRAINT observations_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    ADD CONSTRAINT observations_range_ck CHECK (valid_to IS NULL OR valid_from IS NULL OR valid_to > valid_from),
    ADD CONSTRAINT observations_attributes_ck CHECK (
        jsonb_typeof(attributes) = 'object' AND pg_column_size(attributes) <= 4096),
    ADD CONSTRAINT observations_kind_contract_ck CHECK (CASE observation_kind
        WHEN 'usage' THEN metric_kind IS NOT NULL AND value_numeric IS NOT NULL
            AND native_unit IS NOT NULL AND source_adapter IS NOT NULL AND source_version IS NOT NULL
            AND (trajectory_id IS NOT NULL OR response_id IS NOT NULL OR evidence_artifact_id IS NOT NULL)
        WHEN 'cache' THEN metric_kind IS NOT NULL AND value_numeric IS NOT NULL
            AND native_unit IS NOT NULL AND source_adapter IS NOT NULL AND source_version IS NOT NULL
            AND (trajectory_id IS NOT NULL OR response_id IS NOT NULL OR evidence_artifact_id IS NOT NULL)
        WHEN 'confounder' THEN metric_kind IS NOT NULL AND state_value IS NOT NULL
            AND (audit_revision_id IS NOT NULL OR trajectory_id IS NOT NULL)
        WHEN 'governance_overhead' THEN trajectory_id IS NOT NULL AND audit_revision_id IS NOT NULL
            AND metric_kind IS NOT NULL AND state_value IS NOT NULL AND native_unit IS NOT NULL
        WHEN 'project_attribution' THEN project_id IS NOT NULL AND source_id IS NOT NULL
            AND evidence_artifact_id IS NOT NULL AND state_value IS NOT NULL
            AND source_version IS NOT NULL AND valid_from IS NOT NULL
        WHEN 'source_assertion' THEN source_id IS NOT NULL AND metric_kind IS NOT NULL
            AND state_value IS NOT NULL AND source_version IS NOT NULL AND valid_from IS NOT NULL
        ELSE false END),
    ADD CONSTRAINT observations_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT observations_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT observations_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT observations_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE CASCADE,
    ADD CONSTRAINT observations_evidence_fk FOREIGN KEY (evidence_artifact_id) REFERENCES evidence_artifacts ON DELETE CASCADE,
    ADD CONSTRAINT observations_revision_fk FOREIGN KEY (audit_revision_id) REFERENCES audit_revisions ON DELETE CASCADE;

ALTER TABLE audit_windows
    ALTER window_kind SET NOT NULL, ALTER starts_at SET NOT NULL,
    ALTER ends_at SET NOT NULL, ALTER as_of SET NOT NULL,
    ADD CONSTRAINT audit_windows_range_ck CHECK (starts_at < ends_at AND as_of >= starts_at),
    ADD CONSTRAINT audit_windows_policy_hash_ck CHECK (policy_hash IS NULL OR octet_length(policy_hash) = 32),
    ADD CONSTRAINT audit_windows_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE;

ALTER TABLE audit_revisions
    ALTER audit_window_id SET NOT NULL, ALTER revision_number SET NOT NULL,
    ALTER source_watermark_at SET NOT NULL, ALTER coverage_state SET NOT NULL,
    ALTER revision_hash SET NOT NULL, ALTER engine_version SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT audit_revisions_window_number_uq UNIQUE (audit_window_id, revision_number),
    ADD CONSTRAINT audit_revisions_number_ck CHECK (revision_number >= 1),
    ADD CONSTRAINT audit_revisions_hash_ck CHECK (octet_length(revision_hash) = 32),
    ADD CONSTRAINT audit_revisions_window_fk FOREIGN KEY (audit_window_id) REFERENCES audit_windows ON DELETE CASCADE,
    ADD CONSTRAINT audit_revisions_prior_fk FOREIGN KEY (prior_revision_id) REFERENCES audit_revisions ON DELETE SET NULL;

ALTER TABLE evaluation_runs
    ALTER audit_revision_id SET NOT NULL, ALTER evaluator_name SET NOT NULL,
    ALTER evaluator_version SET NOT NULL, ALTER started_at SET NOT NULL,
    ALTER outcome SET NOT NULL, ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT evaluation_runs_hashes_ck CHECK (
        (fixture_hash IS NULL OR octet_length(fixture_hash)=32) AND
        (config_hash IS NULL OR octet_length(config_hash)=32) AND
        (model_hash IS NULL OR octet_length(model_hash)=32) AND
        (compiler_hash IS NULL OR octet_length(compiler_hash)=32) AND
        (policy_hash IS NULL OR octet_length(policy_hash)=32) AND
        (metric_hash IS NULL OR octet_length(metric_hash)=32) AND
        (run_hash IS NULL OR octet_length(run_hash)=32)),
    ADD CONSTRAINT evaluation_runs_revision_fk FOREIGN KEY (audit_revision_id) REFERENCES audit_revisions ON DELETE CASCADE;

ALTER TABLE metric_definitions
    ALTER metric_name SET NOT NULL, ALTER metric_version SET NOT NULL,
    ALTER native_unit SET NOT NULL, ALTER polarity SET NOT NULL,
    ALTER minimum_sample SET NOT NULL, ALTER practical_change SET NOT NULL,
    ALTER definition_hash SET NOT NULL, ALTER definition_json SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT metric_definitions_identity_uq UNIQUE (metric_name, metric_version),
    ADD CONSTRAINT metric_definitions_sample_ck CHECK (minimum_sample >= 1),
    ADD CONSTRAINT metric_definitions_change_ck CHECK (practical_change > 0),
    ADD CONSTRAINT metric_definitions_hash_ck CHECK (octet_length(definition_hash) = 32);

ALTER TABLE metric_results
    ALTER audit_revision_id SET NOT NULL, ALTER metric_name SET NOT NULL,
    ALTER metric_version SET NOT NULL, ALTER native_unit SET NOT NULL,
    ALTER coverage_state SET NOT NULL, ALTER provenance SET NOT NULL,
    ALTER baseline_sample_count SET NOT NULL, ALTER exclusions SET NOT NULL,
    ALTER computed_at SET NOT NULL,
    ADD CONSTRAINT metric_results_identity_uq UNIQUE (audit_revision_id, metric_name, metric_version),
    ADD CONSTRAINT metric_results_denominator_ck CHECK (denominator IS NULL OR denominator > 0),
    ADD CONSTRAINT metric_results_sample_ck CHECK (sample_count IS NULL OR sample_count >= 0),
    ADD CONSTRAINT metric_results_revision_fk FOREIGN KEY (audit_revision_id) REFERENCES audit_revisions ON DELETE CASCADE,
    ADD CONSTRAINT metric_results_evaluation_fk FOREIGN KEY (evaluation_run_id) REFERENCES evaluation_runs ON DELETE CASCADE,
    ADD CONSTRAINT metric_results_definition_fk FOREIGN KEY (metric_definition_id) REFERENCES metric_definitions ON DELETE RESTRICT;

ALTER TABLE findings
    ALTER audit_revision_id SET NOT NULL, ALTER finding_kind SET NOT NULL,
    ALTER detector_name SET NOT NULL, ALTER detector_version SET NOT NULL,
    ALTER status SET NOT NULL, ALTER counterevidence SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT findings_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    ADD CONSTRAINT findings_revision_fk FOREIGN KEY (audit_revision_id) REFERENCES audit_revisions ON DELETE CASCADE,
    ADD CONSTRAINT findings_evaluation_fk FOREIGN KEY (evaluation_run_id) REFERENCES evaluation_runs ON DELETE CASCADE;

ALTER TABLE recommendations
    ALTER audit_revision_id SET NOT NULL, ALTER recommendation_kind SET NOT NULL,
    ALTER lifecycle_state SET NOT NULL,
    ALTER approval_required SET NOT NULL, ALTER protected_guardrails SET NOT NULL,
    ALTER risks SET NOT NULL, ALTER created_at SET NOT NULL, ALTER updated_at SET NOT NULL,
    ADD CONSTRAINT recommendations_revision_fk FOREIGN KEY (audit_revision_id) REFERENCES audit_revisions ON DELETE CASCADE,
    ADD CONSTRAINT recommendations_finding_fk FOREIGN KEY (finding_id) REFERENCES findings ON DELETE CASCADE,
    ADD CONSTRAINT recommendations_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE;

ALTER TABLE recommendation_events
    ALTER recommendation_id SET NOT NULL, ALTER event_kind SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ADD CONSTRAINT recommendation_events_recommendation_fk FOREIGN KEY (recommendation_id) REFERENCES recommendations ON DELETE CASCADE,
    ADD CONSTRAINT recommendation_events_evidence_fk FOREIGN KEY (evidence_artifact_id) REFERENCES evidence_artifacts ON DELETE SET NULL;

ALTER TABLE retention_policies
    ALTER policy_version SET NOT NULL, ALTER classification SET NOT NULL,
    ALTER retain_for_seconds SET NOT NULL, ALTER legal_hold SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT retention_policies_duration_ck CHECK (retain_for_seconds >= 0),
    ADD CONSTRAINT retention_policies_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE;
CREATE UNIQUE INDEX retention_policies_current_uq ON retention_policies (project_id, classification) WHERE retired_at IS NULL;

ALTER TABLE retention_actions
    ALTER retention_policy_id SET NOT NULL, ALTER project_id SET NOT NULL,
    ALTER action_key SET NOT NULL, ALTER mode SET NOT NULL,
    ALTER cutoff_at SET NOT NULL, ALTER planned_count SET NOT NULL,
    ALTER applied_count SET NOT NULL, ALTER status SET NOT NULL,
    ALTER started_at SET NOT NULL,
    ADD CONSTRAINT retention_actions_key_uq UNIQUE (action_key),
    ADD CONSTRAINT retention_actions_counts_ck CHECK (planned_count >= 0 AND applied_count >= 0 AND applied_count <= planned_count),
    ADD CONSTRAINT retention_actions_policy_fk FOREIGN KEY (retention_policy_id) REFERENCES retention_policies ON DELETE RESTRICT,
    ADD CONSTRAINT retention_actions_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE RESTRICT;

ALTER TABLE archive_batches
    ALTER project_id SET NOT NULL, ALTER retention_action_id SET NOT NULL,
    ALTER archive_reference SET NOT NULL, ALTER encryption_key_reference SET NOT NULL,
    ALTER format_version SET NOT NULL, ALTER range_start SET NOT NULL,
    ALTER range_end SET NOT NULL, ALTER entity_count SET NOT NULL,
    ALTER plaintext_digest SET NOT NULL, ALTER encrypted_digest SET NOT NULL,
    ALTER status SET NOT NULL, ALTER created_at SET NOT NULL,
    ADD CONSTRAINT archive_batches_reference_uq UNIQUE (archive_reference),
    ADD CONSTRAINT archive_batches_range_ck CHECK (range_start < range_end),
    ADD CONSTRAINT archive_batches_count_ck CHECK (entity_count >= 0),
    ADD CONSTRAINT archive_batches_digests_ck CHECK (octet_length(plaintext_digest)=32 AND octet_length(encrypted_digest)=32),
    ADD CONSTRAINT archive_batches_status_ck CHECK (status IN ('pending','encrypted','verified','failed')),
    ADD CONSTRAINT archive_batches_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE RESTRICT,
    ADD CONSTRAINT archive_batches_action_fk FOREIGN KEY (retention_action_id) REFERENCES retention_actions ON DELETE RESTRICT;

ALTER TABLE retention_action_entities
    ALTER retention_action_id SET NOT NULL, ALTER entity_kind SET NOT NULL,
    ALTER planned_count SET NOT NULL, ALTER archived_count SET NOT NULL,
    ALTER deleted_count SET NOT NULL, ALTER archive_digest SET NOT NULL,
    ALTER deletion_digest SET NOT NULL, ALTER recorded_at SET NOT NULL,
    ADD CONSTRAINT retention_action_entities_counts_ck CHECK (
        planned_count >= 0 AND archived_count >= 0 AND deleted_count >= 0 AND
        archived_count = planned_count AND deleted_count <= archived_count),
    ADD CONSTRAINT retention_action_entities_digests_ck CHECK (
        octet_length(archive_digest)=32 AND octet_length(deletion_digest)=32),
    ADD CONSTRAINT retention_action_entities_action_kind_uq UNIQUE (retention_action_id, entity_kind),
    ADD CONSTRAINT retention_action_entities_action_fk FOREIGN KEY (retention_action_id) REFERENCES retention_actions ON DELETE RESTRICT,
    ADD CONSTRAINT retention_action_entities_batch_fk FOREIGN KEY (archive_batch_id) REFERENCES archive_batches ON DELETE RESTRICT;

-- +goose Down
SET search_path TO prompt_better, public;
DROP INDEX retention_policies_current_uq;
DROP INDEX evidence_artifacts_source_alias_uq;
DROP INDEX tool_calls_source_alias_uq;
DROP INDEX responses_source_alias_uq;
DROP INDEX project_aliases_identity_uq;
DROP INDEX source_versions_one_current_uq;
DROP INDEX project_versions_one_current_uq;

-- +goose StatementBegin
DO $down$
DECLARE row record;
BEGIN
    FOR row IN
        SELECT conrelid::regclass AS table_name, conname
        FROM pg_constraint
        WHERE connamespace = 'prompt_better'::regnamespace AND contype <> 'p'
    LOOP
        EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', row.table_name, row.conname);
    END LOOP;
END
$down$;
-- +goose StatementEnd
