-- +goose Up
SET search_path TO prompt_better, public;

CREATE INDEX project_versions_asof_idx ON project_versions (project_id, valid_from DESC, valid_to);
CREATE INDEX source_versions_asof_idx ON source_versions (source_id, valid_from DESC, valid_to);
CREATE INDEX project_aliases_lookup_idx ON project_aliases (source_id, alias_kind, alias_digest);
CREATE INDEX collection_cursors_source_kind_idx ON collection_cursors (source_id, cursor_kind);
CREATE INDEX sessions_project_time_idx ON sessions (project_id, started_at);
CREATE INDEX tasks_project_time_idx ON tasks (project_id, observed_at);
CREATE INDEX trajectories_session_time_idx ON trajectories (session_id, started_at);
CREATE INDEX turns_trajectory_ordinal_idx ON turns (trajectory_id, ordinal);
CREATE INDEX responses_turn_time_idx ON responses (turn_id, started_at);
CREATE INDEX items_response_ordinal_idx ON items (response_id, ordinal);
CREATE INDEX phases_trajectory_ordinal_idx ON phases (trajectory_id, ordinal);
CREATE INDEX state_epochs_trajectory_time_idx ON state_epochs (trajectory_id, started_at);
CREATE INDEX tool_calls_response_time_idx ON tool_calls (response_id, started_at);
CREATE INDEX tool_calls_state_epoch_call_idx ON tool_calls (state_epoch_id, canonical_call_hash);
CREATE INDEX execution_events_trajectory_time_idx ON execution_events (trajectory_id, event_kind, observed_at);
CREATE INDEX execution_events_session_time_idx ON execution_events (session_id, event_kind, observed_at);
CREATE INDEX execution_events_observed_brin_idx ON execution_events USING brin (observed_at);
CREATE INDEX evidence_artifacts_source_time_idx ON evidence_artifacts (source_id, observed_at);
CREATE INDEX evidence_artifacts_observed_brin_idx ON evidence_artifacts USING brin (observed_at);
CREATE INDEX evidence_artifacts_project_retention_idx ON evidence_artifacts (project_id, classification, retained_until)
    WHERE deleted_at IS NULL;
CREATE INDEX evidence_links_target_idx ON evidence_links (target_kind, target_id);
CREATE INDEX observations_trajectory_metric_idx ON observations (trajectory_id, metric_kind, observed_at)
    WHERE trajectory_id IS NOT NULL;
CREATE INDEX observations_audit_kind_idx ON observations (audit_revision_id, observation_kind, observed_at)
    WHERE audit_revision_id IS NOT NULL;
CREATE INDEX observations_source_kind_time_idx ON observations (source_id, observation_kind, observed_at)
    WHERE source_id IS NOT NULL;
CREATE INDEX observations_observed_brin_idx ON observations USING brin (observed_at);
CREATE INDEX audit_windows_project_asof_idx ON audit_windows (project_id, as_of);
CREATE INDEX audit_revisions_window_revision_idx ON audit_revisions (audit_window_id, revision_number DESC);
CREATE INDEX evaluation_runs_run_hash_idx ON evaluation_runs (run_hash) WHERE run_hash IS NOT NULL;
CREATE INDEX metric_definitions_current_name_idx ON metric_definitions (metric_name, metric_version)
    WHERE retired_at IS NULL;
CREATE INDEX metric_results_revision_name_idx ON metric_results (audit_revision_id, metric_name);
CREATE INDEX findings_revision_status_idx ON findings (audit_revision_id, status) WHERE deleted_at IS NULL;
CREATE INDEX recommendations_project_state_idx ON recommendations (project_id, lifecycle_state)
    WHERE deleted_at IS NULL;
CREATE INDEX recommendation_events_recommendation_time_idx ON recommendation_events (recommendation_id, observed_at);
CREATE INDEX retention_actions_project_time_idx ON retention_actions (project_id, started_at);
CREATE INDEX archive_batches_project_range_idx ON archive_batches (project_id, range_start, range_end);
CREATE INDEX archive_batches_status_idx ON archive_batches (status, created_at);
CREATE INDEX retention_action_entities_action_kind_idx ON retention_action_entities (retention_action_id, entity_kind);

-- +goose Down
SET search_path TO prompt_better, public;
DROP INDEX retention_action_entities_action_kind_idx;
DROP INDEX archive_batches_status_idx;
DROP INDEX archive_batches_project_range_idx;
DROP INDEX retention_actions_project_time_idx;
DROP INDEX recommendation_events_recommendation_time_idx;
DROP INDEX recommendations_project_state_idx;
DROP INDEX findings_revision_status_idx;
DROP INDEX metric_results_revision_name_idx;
DROP INDEX metric_definitions_current_name_idx;
DROP INDEX evaluation_runs_run_hash_idx;
DROP INDEX audit_revisions_window_revision_idx;
DROP INDEX audit_windows_project_asof_idx;
DROP INDEX observations_observed_brin_idx;
DROP INDEX observations_source_kind_time_idx;
DROP INDEX observations_audit_kind_idx;
DROP INDEX observations_trajectory_metric_idx;
DROP INDEX evidence_links_target_idx;
DROP INDEX evidence_artifacts_project_retention_idx;
DROP INDEX evidence_artifacts_observed_brin_idx;
DROP INDEX evidence_artifacts_source_time_idx;
DROP INDEX execution_events_observed_brin_idx;
DROP INDEX execution_events_session_time_idx;
DROP INDEX execution_events_trajectory_time_idx;
DROP INDEX tool_calls_state_epoch_call_idx;
DROP INDEX tool_calls_response_time_idx;
DROP INDEX state_epochs_trajectory_time_idx;
DROP INDEX phases_trajectory_ordinal_idx;
DROP INDEX items_response_ordinal_idx;
DROP INDEX responses_turn_time_idx;
DROP INDEX turns_trajectory_ordinal_idx;
DROP INDEX trajectories_session_time_idx;
DROP INDEX tasks_project_time_idx;
DROP INDEX sessions_project_time_idx;
DROP INDEX collection_cursors_source_kind_idx;
DROP INDEX project_aliases_lookup_idx;
DROP INDEX source_versions_asof_idx;
DROP INDEX project_versions_asof_idx;
