-- +goose Up
SET search_path TO prompt_better, public;

CREATE TABLE metric_definitions (
    metric_definition_id uuid PRIMARY KEY,
    metric_name text NOT NULL,
    metric_version text NOT NULL,
    native_unit text NOT NULL,
    polarity text NOT NULL,
    minimum_sample bigint NOT NULL,
    practical_change double precision NOT NULL,
    definition_hash bytea NOT NULL,
    definition_json jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    retired_at timestamptz,
    CONSTRAINT metric_definitions_identity_uq UNIQUE (metric_name, metric_version),
    CONSTRAINT metric_definitions_sample_ck CHECK (minimum_sample >= 1),
    CONSTRAINT metric_definitions_change_ck CHECK (practical_change > 0),
    CONSTRAINT metric_definitions_hash_ck CHECK (octet_length(definition_hash) = 32)
);

ALTER TABLE audit_windows ALTER COLUMN project_id DROP NOT NULL;
ALTER TABLE recommendations ALTER COLUMN project_id DROP NOT NULL;

ALTER TABLE evaluation_runs
    ADD COLUMN fixture_hash bytea,
    ADD COLUMN config_hash bytea,
    ADD COLUMN model_hash bytea,
    ADD COLUMN compiler_hash bytea,
    ADD COLUMN policy_hash bytea,
    ADD COLUMN metric_hash bytea,
    ADD COLUMN run_hash bytea,
    ADD COLUMN decision text,
    ADD CONSTRAINT evaluation_runs_fixture_hash_ck CHECK (fixture_hash IS NULL OR octet_length(fixture_hash) = 32),
    ADD CONSTRAINT evaluation_runs_config_hash_ck CHECK (config_hash IS NULL OR octet_length(config_hash) = 32),
    ADD CONSTRAINT evaluation_runs_model_hash_ck CHECK (model_hash IS NULL OR octet_length(model_hash) = 32),
    ADD CONSTRAINT evaluation_runs_compiler_hash_ck CHECK (compiler_hash IS NULL OR octet_length(compiler_hash) = 32),
    ADD CONSTRAINT evaluation_runs_policy_hash_ck CHECK (policy_hash IS NULL OR octet_length(policy_hash) = 32),
    ADD CONSTRAINT evaluation_runs_metric_hash_ck CHECK (metric_hash IS NULL OR octet_length(metric_hash) = 32),
    ADD CONSTRAINT evaluation_runs_run_hash_ck CHECK (run_hash IS NULL OR octet_length(run_hash) = 32);

ALTER TABLE metric_results
    ADD COLUMN metric_definition_id uuid,
    ADD COLUMN status text,
    ADD COLUMN uncertainty text,
    ADD COLUMN previous_value numeric,
    ADD COLUMN rolling_median numeric,
    ADD COLUMN rolling_mad numeric,
    ADD COLUMN baseline_sample_count bigint NOT NULL DEFAULT 0,
    ADD COLUMN workload_adjusted_residual numeric,
    ADD COLUMN status_reason text,
    ADD COLUMN confidence_label text,
    ADD COLUMN exclusions jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT metric_results_definition_fk FOREIGN KEY (metric_definition_id)
        REFERENCES metric_definitions ON DELETE RESTRICT;

ALTER TABLE recommendations
    ADD COLUMN action_code text,
    ADD COLUMN policy_version text,
    ADD COLUMN cooldown_until timestamptz,
    ADD COLUMN evidence_revision text;

ALTER TABLE findings
    ADD COLUMN cause text,
    ADD COLUMN exception_check text,
    ADD COLUMN counterevidence jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE recommendations
    ADD COLUMN target_surface text,
    ADD COLUMN action_text text,
    ADD COLUMN expected_movement text,
    ADD COLUMN protected_guardrails jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN risks jsonb NOT NULL DEFAULT '[]'::jsonb;

CREATE INDEX metric_definitions_current_name_idx
    ON metric_definitions (metric_name, metric_version)
    WHERE retired_at IS NULL;

CREATE INDEX evaluation_runs_run_hash_idx
    ON evaluation_runs (run_hash)
    WHERE run_hash IS NOT NULL;

COMMENT ON TABLE metric_definitions IS
  'Versioned transparent formulas, denominator rules, exclusions, thresholds, limitations, and misuse risks.';
COMMENT ON COLUMN metric_results.status IS
  'Coverage- and guardrail-aware improved/worsened/flat/mixed/insufficient result.';
COMMENT ON COLUMN recommendations.action_code IS
  'Preview-only bounded action identifier; Prompt Better never executes it.';

GRANT SELECT ON metric_definitions TO prompt_better_reporter;
GRANT SELECT, INSERT, UPDATE ON metric_definitions TO prompt_better_runtime;

-- +goose Down
SET search_path TO prompt_better, public;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM recommendations WHERE project_id IS NULL)
       OR EXISTS (SELECT 1 FROM audit_windows WHERE project_id IS NULL) THEN
        RAISE EXCEPTION 'migration 00007 rollback blocked: portfolio governance rows require export or forward repair';
    END IF;
END $$;
-- +goose StatementEnd

DROP INDEX evaluation_runs_run_hash_idx;
DROP INDEX metric_definitions_current_name_idx;

ALTER TABLE recommendations
    DROP COLUMN risks,
    DROP COLUMN protected_guardrails,
    DROP COLUMN expected_movement,
    DROP COLUMN action_text,
    DROP COLUMN target_surface;

ALTER TABLE findings
    DROP COLUMN counterevidence,
    DROP COLUMN exception_check,
    DROP COLUMN cause;

ALTER TABLE recommendations
    DROP COLUMN evidence_revision,
    DROP COLUMN cooldown_until,
    DROP COLUMN policy_version,
    DROP COLUMN action_code;

ALTER TABLE metric_results
    DROP CONSTRAINT metric_results_definition_fk,
    DROP COLUMN exclusions,
    DROP COLUMN uncertainty,
    DROP COLUMN confidence_label,
    DROP COLUMN status_reason,
    DROP COLUMN workload_adjusted_residual,
    DROP COLUMN baseline_sample_count,
    DROP COLUMN rolling_mad,
    DROP COLUMN rolling_median,
    DROP COLUMN previous_value,
    DROP COLUMN status,
    DROP COLUMN metric_definition_id;

ALTER TABLE evaluation_runs
    DROP CONSTRAINT evaluation_runs_run_hash_ck,
    DROP CONSTRAINT evaluation_runs_metric_hash_ck,
    DROP CONSTRAINT evaluation_runs_policy_hash_ck,
    DROP CONSTRAINT evaluation_runs_compiler_hash_ck,
    DROP CONSTRAINT evaluation_runs_model_hash_ck,
    DROP CONSTRAINT evaluation_runs_config_hash_ck,
    DROP CONSTRAINT evaluation_runs_fixture_hash_ck,
    DROP COLUMN decision,
    DROP COLUMN run_hash,
    DROP COLUMN metric_hash,
    DROP COLUMN policy_hash,
    DROP COLUMN compiler_hash,
    DROP COLUMN model_hash,
    DROP COLUMN config_hash,
    DROP COLUMN fixture_hash;

DROP TABLE metric_definitions;

ALTER TABLE recommendations ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE audit_windows ALTER COLUMN project_id SET NOT NULL;
