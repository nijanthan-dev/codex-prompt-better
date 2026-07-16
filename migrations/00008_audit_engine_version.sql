-- +goose Up
SET search_path TO prompt_better, public;

ALTER TABLE audit_revisions
    ADD COLUMN engine_version text NOT NULL DEFAULT 'audit-v1';

-- +goose Down
SET search_path TO prompt_better, public;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM audit_revisions WHERE engine_version <> 'audit-v1'
    ) THEN
        RAISE EXCEPTION 'cannot remove audit engine provenance while non-v1 revisions exist';
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE audit_revisions
    DROP COLUMN engine_version;
