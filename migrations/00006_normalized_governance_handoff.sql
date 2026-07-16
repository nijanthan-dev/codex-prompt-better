-- +goose Up
SET search_path TO prompt_better, public;

CREATE TABLE tasks (
    task_id uuid PRIMARY KEY,
    project_id uuid,
    source_id uuid,
    external_alias_id uuid,
    task_kind text NOT NULL,
    attribution_state text NOT NULL,
    confidence double precision,
    algorithm_version text NOT NULL,
    observed_at timestamptz NOT NULL,
    knowledge_state text NOT NULL,
    CONSTRAINT tasks_project_fk FOREIGN KEY (project_id) REFERENCES projects ON DELETE CASCADE,
    CONSTRAINT tasks_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    CONSTRAINT tasks_confidence_ck CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1)
);

CREATE TABLE state_epochs (
    state_epoch_id uuid PRIMARY KEY,
    trajectory_id uuid NOT NULL,
    source_id uuid NOT NULL,
    state_hash bytea NOT NULL,
    mutation_state text NOT NULL,
    started_at timestamptz NOT NULL,
    ended_at timestamptz,
    knowledge_state text NOT NULL,
    CONSTRAINT state_epochs_trajectory_fk FOREIGN KEY (trajectory_id) REFERENCES trajectories ON DELETE CASCADE,
    CONSTRAINT state_epochs_source_fk FOREIGN KEY (source_id) REFERENCES sources ON DELETE CASCADE,
    CONSTRAINT state_epochs_hash_ck CHECK (octet_length(state_hash) = 32),
    CONSTRAINT state_epochs_range_ck CHECK (ended_at IS NULL OR ended_at >= started_at)
);

ALTER TABLE turns
    ADD COLUMN task_id uuid,
    ADD CONSTRAINT turns_task_fk FOREIGN KEY (task_id) REFERENCES tasks ON DELETE SET NULL;

ALTER TABLE tool_calls
    ADD COLUMN state_epoch_id uuid,
    ADD COLUMN canonical_call_hash bytea,
    ADD COLUMN result_state text,
    ADD COLUMN output_modality text,
    ADD COLUMN output_size_bytes bigint,
    ADD COLUMN wait_state text,
    ADD CONSTRAINT tool_calls_state_epoch_fk FOREIGN KEY (state_epoch_id) REFERENCES state_epochs ON DELETE SET NULL,
    ADD CONSTRAINT tool_calls_call_hash_ck CHECK (canonical_call_hash IS NULL OR octet_length(canonical_call_hash) = 32),
    ADD CONSTRAINT tool_calls_output_size_ck CHECK (output_size_bytes IS NULL OR output_size_bytes >= 0);

CREATE INDEX tasks_project_time_idx ON tasks (project_id, observed_at);
CREATE INDEX state_epochs_trajectory_time_idx ON state_epochs (trajectory_id, started_at);
CREATE INDEX tool_calls_state_epoch_call_idx ON tool_calls (state_epoch_id, canonical_call_hash);

GRANT SELECT, INSERT, UPDATE ON tasks, state_epochs TO prompt_better_runtime;
GRANT SELECT, INSERT, UPDATE ON tasks, state_epochs TO prompt_better_collector;
GRANT SELECT ON tasks, state_epochs TO prompt_better_reporter;

COMMENT ON TABLE tasks IS
  'Opaque normalized task identity and attribution; ambiguous/unattributed states are preserved.';
COMMENT ON TABLE state_epochs IS
  'Redacted state-change boundaries for unchanged-call and validation/retry classification.';
COMMENT ON COLUMN tool_calls.canonical_call_hash IS
  'Hash of a redacted canonical tool signature; raw payload is never stored.';

-- +goose Down
SET search_path TO prompt_better, public;

DROP INDEX tool_calls_state_epoch_call_idx;
DROP INDEX state_epochs_trajectory_time_idx;
DROP INDEX tasks_project_time_idx;

ALTER TABLE tool_calls
    DROP CONSTRAINT tool_calls_output_size_ck,
    DROP CONSTRAINT tool_calls_call_hash_ck,
    DROP CONSTRAINT tool_calls_state_epoch_fk,
    DROP COLUMN wait_state,
    DROP COLUMN output_size_bytes,
    DROP COLUMN output_modality,
    DROP COLUMN result_state,
    DROP COLUMN canonical_call_hash,
    DROP COLUMN state_epoch_id;

ALTER TABLE turns
    DROP CONSTRAINT turns_task_fk,
    DROP COLUMN task_id;

DROP TABLE state_epochs;
DROP TABLE tasks;
