-- +goose Up
SET search_path TO prompt_better, public;

ALTER TABLE projects
    ALTER created_at SET NOT NULL;
ALTER TABLE project_versions
    ALTER project_id SET NOT NULL,
    ALTER version_number SET NOT NULL,
    ALTER classification SET NOT NULL,
    ALTER lifecycle_state SET NOT NULL,
    ALTER version_hash SET NOT NULL,
    ALTER valid_from SET NOT NULL,
    ADD CONSTRAINT project_versions_number_uq UNIQUE (project_id, version_number),
    ADD CONSTRAINT project_versions_current_uq EXCLUDE USING gist
        (project_id WITH =, tstzrange(valid_from, COALESCE(valid_to, 'infinity'::timestamptz), '[)') WITH &&),
    ADD CONSTRAINT project_versions_number_ck CHECK (version_number >= 1),
    ADD CONSTRAINT project_versions_range_ck CHECK (valid_to IS NULL OR valid_to > valid_from),
    ADD CONSTRAINT project_versions_hash_ck CHECK (octet_length(version_hash) = 32),
    ADD CONSTRAINT project_versions_classification_ck CHECK (classification IN ('public', 'internal', 'confidential', 'restricted', 'unknown')),
    ADD CONSTRAINT project_versions_lifecycle_ck CHECK (lifecycle_state IN ('active', 'disabled', 'deleted', 'unknown')),
    ADD CONSTRAINT project_versions_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE;
CREATE UNIQUE INDEX project_versions_one_current_uq
    ON project_versions (project_id) WHERE valid_to IS NULL;
ALTER TABLE sources
    ALTER source_kind SET NOT NULL,
    ALTER created_at SET NOT NULL;
ALTER TABLE source_versions
    ALTER source_id SET NOT NULL,
    ALTER version_number SET NOT NULL,
    ALTER product_surface SET NOT NULL,
    ALTER coverage_state SET NOT NULL,
    ALTER enabled SET NOT NULL,
    ALTER version_hash SET NOT NULL,
    ALTER valid_from SET NOT NULL,
    ADD CONSTRAINT source_versions_number_uq UNIQUE (source_id, version_number),
    ADD CONSTRAINT source_versions_current_uq EXCLUDE USING gist
        (source_id WITH =, tstzrange(valid_from, COALESCE(valid_to, 'infinity'::timestamptz), '[)') WITH &&),
    ADD CONSTRAINT source_versions_number_ck CHECK (version_number >= 1),
    ADD CONSTRAINT source_versions_range_ck CHECK (valid_to IS NULL OR valid_to > valid_from),
    ADD CONSTRAINT source_versions_hash_ck CHECK (octet_length(version_hash) = 32),
    ADD CONSTRAINT source_versions_surface_ck CHECK (product_surface IN ('codex_subscription', 'openai_api', 'local', 'unknown')),
    ADD CONSTRAINT source_versions_coverage_ck CHECK (coverage_state IN ('complete', 'partial', 'missing', 'unknown')),
    ADD CONSTRAINT source_versions_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
CREATE UNIQUE INDEX source_versions_one_current_uq
    ON source_versions (source_id) WHERE valid_to IS NULL;
ALTER TABLE key_versions
    ALTER key_reference SET NOT NULL,
    ALTER algorithm SET NOT NULL,
    ALTER state SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT key_versions_reference_uq UNIQUE (key_reference),
    ADD CONSTRAINT key_versions_algorithm_ck CHECK (algorithm = 'hmac-sha256'),
    ADD CONSTRAINT key_versions_state_ck CHECK (state IN ('pending', 'active', 'retired', 'lost', 'deleted')),
    ADD CONSTRAINT key_versions_no_material_ck CHECK (length(key_reference) BETWEEN 1 AND 200);
ALTER TABLE project_aliases
    ALTER project_id SET NOT NULL,
    ALTER source_id SET NOT NULL,
    ALTER key_version_id SET NOT NULL,
    ALTER alias_kind SET NOT NULL,
    ALTER alias_digest SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT project_aliases_digest_ck CHECK (octet_length(alias_digest) = 32),
    ADD CONSTRAINT project_aliases_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT project_aliases_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT project_aliases_key_fk FOREIGN KEY (key_version_id) REFERENCES key_versions ON DELETE RESTRICT;
CREATE UNIQUE INDEX project_aliases_identity_uq
    ON project_aliases (source_id, key_version_id, alias_kind, alias_digest)
    WHERE deleted_at IS NULL;
ALTER TABLE project_attributions
    ALTER project_id SET NOT NULL,
    ALTER source_id SET NOT NULL,
    ALTER attribution_state SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ADD CONSTRAINT project_attributions_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    ADD CONSTRAINT project_attributions_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT project_attributions_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
ALTER TABLE source_assertions
    ALTER source_id SET NOT NULL,
    ALTER assertion_kind SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ALTER provenance SET NOT NULL,
    ALTER asserted_at SET NOT NULL,
    ADD CONSTRAINT source_assertions_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT source_assertions_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE;
ALTER TABLE collection_cursors
    ALTER source_id SET NOT NULL,
    ALTER cursor_kind SET NOT NULL,
    ALTER cursor_value SET NOT NULL,
    ALTER cursor_state SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ALTER updated_at SET NOT NULL,
    ADD CONSTRAINT collection_cursors_source_kind_uq UNIQUE (source_id, cursor_kind),
    ADD CONSTRAINT collection_cursors_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
ALTER TABLE policy_snapshots
    ALTER project_id SET NOT NULL,
    ALTER schema_version SET NOT NULL,
    ALTER policy_hash SET NOT NULL,
    ALTER raw_retention_enabled SET NOT NULL,
    ALTER telemetry_enabled SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT policy_snapshots_hash_ck CHECK (octet_length(policy_hash) = 32),
    ADD CONSTRAINT policy_snapshots_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE;
ALTER TABLE sessions
    ALTER source_id SET NOT NULL,
    ALTER started_at SET NOT NULL,
    ALTER coverage_state SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT sessions_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE SET NULL,
    ADD CONSTRAINT sessions_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
ALTER TABLE trajectories
    ALTER session_id SET NOT NULL,
    ALTER source_id SET NOT NULL,
    ALTER started_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT trajectories_session_fk FOREIGN KEY (session_id) REFERENCES sessions ON DELETE CASCADE,
    ADD CONSTRAINT trajectories_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT trajectories_parent_fk FOREIGN KEY (parent_trajectory_id) REFERENCES trajectories ON DELETE SET NULL;
ALTER TABLE turns
    ALTER trajectory_id SET NOT NULL,
    ALTER source_id SET NOT NULL,
    ALTER ordinal SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT turns_trajectory_ordinal_uq UNIQUE (trajectory_id, ordinal),
    ADD CONSTRAINT turns_ordinal_ck CHECK (ordinal >= 0),
    ADD CONSTRAINT turns_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT turns_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
ALTER TABLE responses
    ALTER turn_id SET NOT NULL,
    ALTER source_id SET NOT NULL,
    ALTER started_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT responses_turn_fk FOREIGN KEY (turn_id) REFERENCES turns ON DELETE CASCADE,
    ADD CONSTRAINT responses_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT responses_parent_fk FOREIGN KEY (parent_response_id) REFERENCES responses ON DELETE SET NULL;
CREATE UNIQUE INDEX responses_source_alias_uq
    ON responses (source_id, external_alias_id) WHERE external_alias_id IS NOT NULL;
ALTER TABLE items
    ALTER response_id SET NOT NULL,
    ALTER source_id SET NOT NULL,
    ALTER item_kind SET NOT NULL,
    ALTER ordinal SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT items_response_ordinal_uq UNIQUE (response_id, ordinal),
    ADD CONSTRAINT items_content_hash_ck CHECK (content_hash IS NULL OR octet_length(content_hash) = 32),
    ADD CONSTRAINT items_content_length_ck CHECK (content_length IS NULL OR content_length >= 0),
    ADD CONSTRAINT items_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE CASCADE,
    ADD CONSTRAINT items_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
ALTER TABLE phases
    ALTER trajectory_id SET NOT NULL,
    ALTER phase_kind SET NOT NULL,
    ALTER ordinal SET NOT NULL,
    ALTER started_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT phases_trajectory_ordinal_uq UNIQUE (trajectory_id, ordinal),
    ADD CONSTRAINT phases_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT phases_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE SET NULL;
ALTER TABLE tool_calls
    ALTER response_id SET NOT NULL,
    ALTER source_id SET NOT NULL,
    ALTER call_path SET NOT NULL,
    ALTER tool_kind SET NOT NULL,
    ALTER started_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT tool_calls_path_ck CHECK (call_path IN ('direct', 'programmatic', 'unknown')),
    ADD CONSTRAINT tool_calls_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE CASCADE,
    ADD CONSTRAINT tool_calls_item_fk FOREIGN KEY (item_id) REFERENCES items ON DELETE SET NULL,
    ADD CONSTRAINT tool_calls_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
CREATE UNIQUE INDEX tool_calls_source_alias_uq
    ON tool_calls (source_id, external_alias_id) WHERE external_alias_id IS NOT NULL;
ALTER TABLE tool_loops
    ALTER trajectory_id SET NOT NULL,
    ALTER ordinal SET NOT NULL,
    ALTER started_at SET NOT NULL,
    ADD CONSTRAINT tool_loops_trajectory_ordinal_uq UNIQUE (trajectory_id, ordinal),
    ADD CONSTRAINT tool_loops_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT tool_loops_phase_fk FOREIGN KEY (phase_id) REFERENCES phases ON DELETE SET NULL;
ALTER TABLE delegation_events
    ALTER trajectory_id SET NOT NULL,
    ALTER source_id SET NOT NULL,
    ALTER event_kind SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT delegation_events_depth_ck CHECK (depth IS NULL OR depth >= 0),
    ADD CONSTRAINT delegation_events_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT delegation_events_parent_fk FOREIGN KEY (parent_trajectory_id) REFERENCES trajectories ON DELETE SET NULL,
    ADD CONSTRAINT delegation_events_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
ALTER TABLE compaction_events
    ALTER trajectory_id SET NOT NULL,
    ALTER source_id SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT compaction_events_threshold_ck CHECK (threshold_value IS NULL OR threshold_value >= 0),
    ADD CONSTRAINT compaction_events_hash_ck CHECK (opaque_item_hash IS NULL OR octet_length(opaque_item_hash) = 32),
    ADD CONSTRAINT compaction_events_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT compaction_events_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE SET NULL,
    ADD CONSTRAINT compaction_events_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
ALTER TABLE stop_events
    ALTER trajectory_id SET NOT NULL,
    ALTER event_kind SET NOT NULL,
    ALTER outcome SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT stop_events_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT stop_events_phase_fk FOREIGN KEY (phase_id) REFERENCES phases ON DELETE SET NULL;
ALTER TABLE prompt_plans
    ALTER project_id SET NOT NULL,
    ALTER schema_version SET NOT NULL,
    ALTER plan_hash SET NOT NULL,
    ALTER phase_scope SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT prompt_plans_hash_ck CHECK (octet_length(plan_hash) = 32),
    ADD CONSTRAINT prompt_plans_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT prompt_plans_session_fk FOREIGN KEY (session_id) REFERENCES sessions ON DELETE SET NULL;
ALTER TABLE execution_budgets
    ALTER prompt_plan_id SET NOT NULL,
    ALTER schema_version SET NOT NULL,
    ALTER enforcement SET NOT NULL,
    ALTER delegation_policy SET NOT NULL,
    ALTER exhaustion_outcome SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT execution_budgets_nonnegative_ck CHECK (
        (max_tool_loops IS NULL OR max_tool_loops >= 0) AND
        (max_retries IS NULL OR max_retries >= 0) AND
        (max_retrieval_expansions IS NULL OR max_retrieval_expansions >= 0) AND
        (max_agent_depth IS NULL OR max_agent_depth >= 0) AND
        (max_concurrency IS NULL OR max_concurrency >= 1)
    ),
    ADD CONSTRAINT execution_budgets_prompt_fk FOREIGN KEY (prompt_plan_id) REFERENCES prompt_plans ON DELETE CASCADE;
ALTER TABLE host_capability_snapshots
    ALTER session_id SET NOT NULL,
    ALTER source_id SET NOT NULL,
    ALTER capability_name SET NOT NULL,
    ALTER capability_state SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT host_capabilities_session_fk FOREIGN KEY (session_id) REFERENCES sessions ON DELETE CASCADE,
    ADD CONSTRAINT host_capabilities_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE;
ALTER TABLE boundaries
    ALTER category SET NOT NULL,
    ALTER outcome SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ADD CONSTRAINT boundaries_risk_ck CHECK (risk IS NULL OR risk BETWEEN 0 AND 100),
    ADD CONSTRAINT boundaries_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    ADD CONSTRAINT boundaries_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT boundaries_session_fk FOREIGN KEY (session_id) REFERENCES sessions ON DELETE CASCADE,
    ADD CONSTRAINT boundaries_prompt_fk FOREIGN KEY (prompt_plan_id) REFERENCES prompt_plans ON DELETE CASCADE;
ALTER TABLE checkpoints
    ALTER session_id SET NOT NULL,
    ALTER checkpoint_hash SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT checkpoints_hash_ck CHECK (octet_length(checkpoint_hash) = 32),
    ADD CONSTRAINT checkpoints_counts_ck CHECK (completed_count >= 0 AND blocker_count >= 0 AND next_action_count >= 0 AND gate_count >= 0),
    ADD CONSTRAINT checkpoints_session_fk FOREIGN KEY (session_id) REFERENCES sessions ON DELETE CASCADE,
    ADD CONSTRAINT checkpoints_prompt_fk FOREIGN KEY (prompt_plan_id) REFERENCES prompt_plans ON DELETE SET NULL;
ALTER TABLE evidence_artifacts
    ALTER source_id SET NOT NULL,
    ALTER schema_version SET NOT NULL,
    ALTER content_hash SET NOT NULL,
    ALTER content_length SET NOT NULL,
    ALTER classification SET NOT NULL,
    ALTER redaction_state SET NOT NULL,
    ALTER coverage_state SET NOT NULL,
    ALTER provenance SET NOT NULL,
    ALTER product_surface SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ADD CONSTRAINT evidence_artifacts_hash_ck CHECK (octet_length(content_hash) = 32),
    ADD CONSTRAINT evidence_artifacts_length_ck CHECK (content_length >= 0),
    ADD CONSTRAINT evidence_artifacts_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    ADD CONSTRAINT evidence_artifacts_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT evidence_artifacts_session_fk FOREIGN KEY (session_id) REFERENCES sessions ON DELETE CASCADE;
CREATE UNIQUE INDEX evidence_artifacts_source_alias_uq
    ON evidence_artifacts (source_id, external_alias_id)
    WHERE external_alias_id IS NOT NULL;
ALTER TABLE project_attributions
    ADD CONSTRAINT project_attributions_evidence_fk FOREIGN KEY (evidence_artifact_id) REFERENCES evidence_artifacts ON DELETE CASCADE;
ALTER TABLE evidence_links
    ALTER evidence_artifact_id SET NOT NULL,
    ALTER target_kind SET NOT NULL,
    ALTER target_id SET NOT NULL,
    ALTER link_kind SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT evidence_links_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    ADD CONSTRAINT evidence_links_artifact_fk FOREIGN KEY (evidence_artifact_id) REFERENCES evidence_artifacts ON DELETE CASCADE;
ALTER TABLE usage_observations
    ALTER metric_kind SET NOT NULL,
    ALTER usage_unit SET NOT NULL,
    ALTER product_surface SET NOT NULL,
    ALTER accounting_regime SET NOT NULL,
    ALTER provenance SET NOT NULL,
    ALTER source_adapter SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT usage_observations_value_ck CHECK (value_numeric IS NULL OR value_numeric >= 0),
    ADD CONSTRAINT usage_observations_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT usage_observations_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE CASCADE,
    ADD CONSTRAINT usage_observations_evidence_fk FOREIGN KEY (evidence_artifact_id) REFERENCES evidence_artifacts ON DELETE CASCADE;
ALTER TABLE cache_observations
    ALTER cache_kind SET NOT NULL,
    ALTER usage_unit SET NOT NULL,
    ALTER provenance SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT cache_observations_value_ck CHECK (value_numeric IS NULL OR value_numeric >= 0),
    ADD CONSTRAINT cache_observations_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    ADD CONSTRAINT cache_observations_response_fk FOREIGN KEY (response_id) REFERENCES responses ON DELETE CASCADE,
    ADD CONSTRAINT cache_observations_evidence_fk FOREIGN KEY (evidence_artifact_id) REFERENCES evidence_artifacts ON DELETE CASCADE;
ALTER TABLE audit_windows
    ALTER project_id SET NOT NULL,
    ALTER window_kind SET NOT NULL,
    ALTER starts_at SET NOT NULL,
    ALTER ends_at SET NOT NULL,
    ALTER as_of SET NOT NULL,
    ADD CONSTRAINT audit_windows_range_ck CHECK (starts_at < ends_at AND as_of >= starts_at),
    ADD CONSTRAINT audit_windows_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    ADD CONSTRAINT audit_windows_policy_fk FOREIGN KEY (policy_snapshot_id) REFERENCES policy_snapshots ON DELETE SET NULL;
ALTER TABLE audit_revisions
    ALTER audit_window_id SET NOT NULL,
    ALTER revision_number SET NOT NULL,
    ALTER source_watermark_at SET NOT NULL,
    ALTER coverage_state SET NOT NULL,
    ALTER revision_hash SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT audit_revisions_window_number_uq UNIQUE (audit_window_id, revision_number),
    ADD CONSTRAINT audit_revisions_number_ck CHECK (revision_number >= 1),
    ADD CONSTRAINT audit_revisions_hash_ck CHECK (octet_length(revision_hash) = 32),
    ADD CONSTRAINT audit_revisions_window_fk FOREIGN KEY (audit_window_id) REFERENCES audit_windows ON DELETE CASCADE,
    ADD CONSTRAINT audit_revisions_prior_fk FOREIGN KEY (prior_revision_id) REFERENCES audit_revisions ON DELETE SET NULL;
ALTER TABLE evaluation_runs
    ALTER audit_revision_id SET NOT NULL,
    ALTER evaluator_name SET NOT NULL,
    ALTER evaluator_version SET NOT NULL,
    ALTER started_at SET NOT NULL,
    ALTER outcome SET NOT NULL,
    ALTER knowledge_state SET NOT NULL,
    ADD CONSTRAINT evaluation_runs_revision_fk FOREIGN KEY (audit_revision_id) REFERENCES audit_revisions ON DELETE CASCADE;
ALTER TABLE metric_results
    ALTER audit_revision_id SET NOT NULL,
    ALTER metric_name SET NOT NULL,
    ALTER metric_version SET NOT NULL,
    ALTER native_unit SET NOT NULL,
    ALTER coverage_state SET NOT NULL,
    ALTER provenance SET NOT NULL,
    ALTER computed_at SET NOT NULL,
    ADD CONSTRAINT metric_results_identity_uq UNIQUE (audit_revision_id, metric_name, metric_version),
    ADD CONSTRAINT metric_results_denominator_ck CHECK (denominator IS NULL OR denominator > 0),
    ADD CONSTRAINT metric_results_sample_ck CHECK (sample_count IS NULL OR sample_count >= 0),
    ADD CONSTRAINT metric_results_revision_fk FOREIGN KEY (audit_revision_id) REFERENCES audit_revisions ON DELETE CASCADE,
    ADD CONSTRAINT metric_results_evaluation_fk FOREIGN KEY (evaluation_run_id) REFERENCES evaluation_runs ON DELETE CASCADE;
ALTER TABLE findings
    ALTER audit_revision_id SET NOT NULL,
    ALTER finding_kind SET NOT NULL,
    ALTER detector_name SET NOT NULL,
    ALTER detector_version SET NOT NULL,
    ALTER status SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT findings_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    ADD CONSTRAINT findings_revision_fk FOREIGN KEY (audit_revision_id) REFERENCES audit_revisions ON DELETE CASCADE,
    ADD CONSTRAINT findings_evaluation_fk FOREIGN KEY (evaluation_run_id) REFERENCES evaluation_runs ON DELETE CASCADE;
ALTER TABLE recommendations
    ALTER project_id SET NOT NULL,
    ALTER recommendation_kind SET NOT NULL,
    ALTER lifecycle_state SET NOT NULL,
    ALTER approval_required SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ALTER updated_at SET NOT NULL,
    ADD CONSTRAINT recommendations_finding_fk FOREIGN KEY (finding_id) REFERENCES findings ON DELETE CASCADE,
    ADD CONSTRAINT recommendations_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE;
ALTER TABLE recommendation_events
    ALTER recommendation_id SET NOT NULL,
    ALTER event_kind SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ADD CONSTRAINT recommendation_events_recommendation_fk FOREIGN KEY (recommendation_id) REFERENCES recommendations ON DELETE CASCADE,
    ADD CONSTRAINT recommendation_events_evidence_fk FOREIGN KEY (evidence_artifact_id) REFERENCES evidence_artifacts ON DELETE SET NULL;
ALTER TABLE confounder_labels
    ALTER audit_revision_id SET NOT NULL,
    ALTER label_kind SET NOT NULL,
    ALTER label_state SET NOT NULL,
    ALTER provenance SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ADD CONSTRAINT confounders_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    ADD CONSTRAINT confounders_revision_fk FOREIGN KEY (audit_revision_id) REFERENCES audit_revisions ON DELETE CASCADE,
    ADD CONSTRAINT confounders_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE;
ALTER TABLE governance_overhead
    ALTER audit_revision_id SET NOT NULL,
    ALTER overhead_kind SET NOT NULL,
    ALTER native_unit SET NOT NULL,
    ALTER required_state SET NOT NULL,
    ALTER observed_at SET NOT NULL,
    ADD CONSTRAINT governance_overhead_value_ck CHECK (native_value IS NULL OR native_value >= 0),
    ADD CONSTRAINT governance_overhead_revision_fk FOREIGN KEY (audit_revision_id) REFERENCES audit_revisions ON DELETE CASCADE,
    ADD CONSTRAINT governance_overhead_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE;
ALTER TABLE retention_policies
    ALTER policy_version SET NOT NULL,
    ALTER classification SET NOT NULL,
    ALTER retain_for_seconds SET NOT NULL,
    ALTER legal_hold SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT retention_policies_duration_ck CHECK (retain_for_seconds >= 0),
    ADD CONSTRAINT retention_policies_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE;
CREATE UNIQUE INDEX retention_policies_current_uq
    ON retention_policies (project_id, classification) WHERE retired_at IS NULL;
ALTER TABLE retention_actions
    ALTER retention_policy_id SET NOT NULL,
    ALTER project_id SET NOT NULL,
    ALTER action_key SET NOT NULL,
    ALTER mode SET NOT NULL,
    ALTER cutoff_at SET NOT NULL,
    ALTER planned_count SET NOT NULL,
    ALTER applied_count SET NOT NULL,
    ALTER status SET NOT NULL,
    ALTER started_at SET NOT NULL,
    ADD CONSTRAINT retention_actions_key_uq UNIQUE (action_key),
    ADD CONSTRAINT retention_actions_counts_ck CHECK (planned_count >= 0 AND applied_count >= 0 AND applied_count <= planned_count),
    ADD CONSTRAINT retention_actions_policy_fk FOREIGN KEY (retention_policy_id) REFERENCES retention_policies ON DELETE RESTRICT,
    ADD CONSTRAINT retention_actions_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE RESTRICT;
ALTER TABLE archive_batches
    ALTER project_id SET NOT NULL,
    ALTER retention_action_id SET NOT NULL,
    ALTER archive_reference SET NOT NULL,
    ALTER encryption_key_reference SET NOT NULL,
    ALTER format_version SET NOT NULL,
    ALTER range_start SET NOT NULL,
    ALTER range_end SET NOT NULL,
    ALTER entity_count SET NOT NULL,
    ALTER plaintext_digest SET NOT NULL,
    ALTER encrypted_digest SET NOT NULL,
    ALTER status SET NOT NULL,
    ALTER created_at SET NOT NULL,
    ADD CONSTRAINT archive_batches_reference_uq UNIQUE (archive_reference),
    ADD CONSTRAINT archive_batches_range_ck CHECK (range_start < range_end),
    ADD CONSTRAINT archive_batches_count_ck CHECK (entity_count >= 0),
    ADD CONSTRAINT archive_batches_digests_ck CHECK (octet_length(plaintext_digest) = 32 AND octet_length(encrypted_digest) = 32),
    ADD CONSTRAINT archive_batches_status_ck CHECK (status IN ('pending', 'encrypted', 'verified', 'failed')),
    ADD CONSTRAINT archive_batches_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE RESTRICT,
    ADD CONSTRAINT archive_batches_action_fk FOREIGN KEY (retention_action_id) REFERENCES retention_actions ON DELETE RESTRICT;
ALTER TABLE archive_entities
    ALTER archive_batch_id SET NOT NULL,
    ALTER entity_kind SET NOT NULL,
    ALTER entity_count SET NOT NULL,
    ALTER integrity_digest SET NOT NULL,
    ADD CONSTRAINT archive_entities_batch_kind_uq UNIQUE (archive_batch_id, entity_kind),
    ADD CONSTRAINT archive_entities_count_ck CHECK (entity_count >= 0),
    ADD CONSTRAINT archive_entities_digest_ck CHECK (octet_length(integrity_digest) = 32),
    ADD CONSTRAINT archive_entities_batch_fk FOREIGN KEY (archive_batch_id) REFERENCES archive_batches ON DELETE RESTRICT;
ALTER TABLE deletion_audits
    ALTER retention_action_id SET NOT NULL,
    ALTER entity_kind SET NOT NULL,
    ALTER deleted_count SET NOT NULL,
    ALTER result_hash SET NOT NULL,
    ALTER recorded_at SET NOT NULL,
    ADD CONSTRAINT deletion_audits_count_ck CHECK (deleted_count >= 0),
    ADD CONSTRAINT deletion_audits_hash_ck CHECK (octet_length(result_hash) = 32),
    ADD CONSTRAINT deletion_audits_action_kind_uq UNIQUE (retention_action_id, entity_kind),
    ADD CONSTRAINT deletion_audits_action_fk FOREIGN KEY (retention_action_id) REFERENCES retention_actions ON DELETE RESTRICT;

-- +goose Down
-- Forward repair only after a persisted release. This complete down path exists
-- for pre-release transactional validation and runs before migration 3 down.
SET search_path TO prompt_better, public;
DROP INDEX tool_calls_source_alias_uq;
DROP INDEX responses_source_alias_uq;
DROP INDEX evidence_artifacts_source_alias_uq;
DROP INDEX project_aliases_identity_uq;
DROP INDEX retention_policies_current_uq;
DROP INDEX source_versions_one_current_uq;
DROP INDEX project_versions_one_current_uq;

DO $down$
DECLARE
    row record;
BEGIN
    FOR row IN
        SELECT conrelid::regclass AS table_name, conname
        FROM pg_constraint
        WHERE connamespace = 'prompt_better'::regnamespace
          AND contype <> 'p'
    LOOP
        EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', row.table_name, row.conname);
    END LOOP;
END
$down$;
