-- +goose Up
CREATE SCHEMA IF NOT EXISTS prompt_better AUTHORIZATION CURRENT_USER;
CREATE EXTENSION IF NOT EXISTS btree_gist;
COMMENT ON SCHEMA prompt_better IS
  'Prompt Better normalized local evidence and governance data';

-- +goose Down
DROP SCHEMA IF EXISTS prompt_better;
