-- +goose Up
SET search_path TO prompt_better, public;

CREATE INDEX project_versions_asof_idx
    ON project_versions (project_id, valid_from DESC, valid_to);
CREATE INDEX source_versions_asof_idx
    ON source_versions (source_id, valid_from DESC, valid_to);
CREATE INDEX project_aliases_lookup_idx
    ON project_aliases (source_id, key_version_id, alias_kind, alias_digest)
    WHERE deleted_at IS NULL;
CREATE INDEX project_attributions_project_time_idx
    ON project_attributions (project_id, observed_at DESC);
CREATE INDEX source_assertions_project_time_idx
    ON source_assertions (project_id, asserted_at DESC);
CREATE INDEX collection_cursors_source_kind_idx
    ON collection_cursors (source_id, cursor_kind);
CREATE INDEX sessions_project_time_idx
    ON sessions (project_id, started_at DESC);
CREATE INDEX trajectories_session_time_idx
    ON trajectories (session_id, started_at);
CREATE INDEX turns_trajectory_ordinal_idx
    ON turns (trajectory_id, ordinal);
CREATE INDEX responses_turn_time_idx
    ON responses (turn_id, started_at);
CREATE INDEX items_response_ordinal_idx
    ON items (response_id, ordinal);
CREATE INDEX phases_trajectory_ordinal_idx
    ON phases (trajectory_id, ordinal);
CREATE INDEX tool_calls_response_time_idx
    ON tool_calls (response_id, started_at);
CREATE INDEX tool_loops_trajectory_ordinal_idx
    ON tool_loops (trajectory_id, ordinal);
CREATE INDEX delegation_events_trajectory_time_idx
    ON delegation_events (trajectory_id, observed_at);
CREATE INDEX compaction_events_trajectory_time_idx
    ON compaction_events (trajectory_id, observed_at);
CREATE INDEX stop_events_trajectory_time_idx
    ON stop_events (trajectory_id, observed_at);
CREATE INDEX host_capabilities_session_name_idx
    ON host_capability_snapshots (session_id, capability_name, observed_at DESC);
CREATE INDEX boundaries_session_category_idx
    ON boundaries (session_id, category, observed_at DESC);
CREATE INDEX checkpoints_session_time_idx
    ON checkpoints (session_id, created_at DESC);
CREATE INDEX evidence_artifacts_source_time_idx
    ON evidence_artifacts (source_id, observed_at DESC);
CREATE INDEX evidence_artifacts_observed_brin_idx
    ON evidence_artifacts USING brin (observed_at) WITH (pages_per_range = 32);
CREATE INDEX evidence_artifacts_project_retention_idx
    ON evidence_artifacts (project_id, retained_until)
    WHERE deleted_at IS NULL;
CREATE INDEX evidence_links_target_idx
    ON evidence_links (target_kind, target_id);
CREATE INDEX usage_observations_trajectory_metric_idx
    ON usage_observations (trajectory_id, metric_kind, observed_at);
CREATE INDEX usage_observations_observed_brin_idx
    ON usage_observations USING brin (observed_at) WITH (pages_per_range = 32);
CREATE INDEX cache_observations_trajectory_time_idx
    ON cache_observations (trajectory_id, observed_at);
CREATE INDEX cache_observations_observed_brin_idx
    ON cache_observations USING brin (observed_at) WITH (pages_per_range = 32);
CREATE INDEX audit_windows_project_asof_idx
    ON audit_windows (project_id, as_of DESC);
CREATE INDEX audit_revisions_window_revision_idx
    ON audit_revisions (audit_window_id, revision_number DESC);
CREATE INDEX metric_results_revision_name_idx
    ON metric_results (audit_revision_id, metric_name, metric_version);
CREATE INDEX findings_revision_status_idx
    ON findings (audit_revision_id, status)
    WHERE deleted_at IS NULL;
CREATE INDEX recommendations_project_state_idx
    ON recommendations (project_id, lifecycle_state)
    WHERE deleted_at IS NULL;
CREATE INDEX recommendation_events_recommendation_time_idx
    ON recommendation_events (recommendation_id, observed_at);
CREATE INDEX confounders_revision_kind_idx
    ON confounder_labels (audit_revision_id, label_kind);
CREATE INDEX governance_overhead_revision_kind_idx
    ON governance_overhead (audit_revision_id, overhead_kind);
CREATE INDEX retention_actions_project_time_idx
    ON retention_actions (project_id, started_at DESC);
CREATE INDEX archive_batches_project_range_idx
    ON archive_batches (project_id, range_end DESC);
CREATE INDEX archive_batches_status_idx
    ON archive_batches (status, created_at);
CREATE INDEX archive_entities_batch_kind_idx
    ON archive_entities (archive_batch_id, entity_kind);
CREATE INDEX deletion_audits_action_kind_idx
    ON deletion_audits (retention_action_id, entity_kind);

-- +goose Down
SET search_path TO prompt_better, public;
DROP INDEX deletion_audits_action_kind_idx;
DROP INDEX archive_entities_batch_kind_idx;
DROP INDEX archive_batches_status_idx;
DROP INDEX archive_batches_project_range_idx;
DROP INDEX retention_actions_project_time_idx;
DROP INDEX governance_overhead_revision_kind_idx;
DROP INDEX confounders_revision_kind_idx;
DROP INDEX recommendation_events_recommendation_time_idx;
DROP INDEX recommendations_project_state_idx;
DROP INDEX findings_revision_status_idx;
DROP INDEX metric_results_revision_name_idx;
DROP INDEX audit_revisions_window_revision_idx;
DROP INDEX audit_windows_project_asof_idx;
DROP INDEX cache_observations_trajectory_time_idx;
DROP INDEX cache_observations_observed_brin_idx;
DROP INDEX usage_observations_observed_brin_idx;
DROP INDEX usage_observations_trajectory_metric_idx;
DROP INDEX evidence_links_target_idx;
DROP INDEX evidence_artifacts_project_retention_idx;
DROP INDEX evidence_artifacts_observed_brin_idx;
DROP INDEX evidence_artifacts_source_time_idx;
DROP INDEX checkpoints_session_time_idx;
DROP INDEX boundaries_session_category_idx;
DROP INDEX host_capabilities_session_name_idx;
DROP INDEX stop_events_trajectory_time_idx;
DROP INDEX compaction_events_trajectory_time_idx;
DROP INDEX delegation_events_trajectory_time_idx;
DROP INDEX tool_loops_trajectory_ordinal_idx;
DROP INDEX tool_calls_response_time_idx;
DROP INDEX phases_trajectory_ordinal_idx;
DROP INDEX items_response_ordinal_idx;
DROP INDEX responses_turn_time_idx;
DROP INDEX turns_trajectory_ordinal_idx;
DROP INDEX trajectories_session_time_idx;
DROP INDEX sessions_project_time_idx;
DROP INDEX collection_cursors_source_kind_idx;
DROP INDEX source_assertions_project_time_idx;
DROP INDEX project_attributions_project_time_idx;
DROP INDEX project_aliases_lookup_idx;
DROP INDEX source_versions_asof_idx;
DROP INDEX project_versions_asof_idx;
