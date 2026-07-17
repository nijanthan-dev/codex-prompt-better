-- +goose Up
SET search_path TO prompt_better, public;

CREATE OR REPLACE VIEW project_coverage
WITH (security_barrier = true) AS
WITH session_counts AS (
    SELECT project_id,count(*) AS session_count
    FROM sessions GROUP BY project_id
), evidence_counts AS (
    SELECT project_id,count(*) AS evidence_count,
        count(*) FILTER (WHERE coverage_state='complete') AS complete_evidence_count
    FROM evidence_artifacts WHERE deleted_at IS NULL GROUP BY project_id
)
SELECT p.project_id,
    coalesce(s.session_count,0) AS session_count,
    coalesce(e.evidence_count,0) AS evidence_count,
    coalesce(e.complete_evidence_count,0) AS complete_evidence_count,
    CASE WHEN coalesce(e.evidence_count,0)=0 THEN NULL
         ELSE e.complete_evidence_count::numeric/e.evidence_count::numeric END AS coverage_ratio,
    CASE WHEN coalesce(e.evidence_count,0)=0 THEN 'missing' ELSE 'observed' END AS denominator_state
FROM projects p
LEFT JOIN session_counts s USING (project_id)
LEFT JOIN evidence_counts e USING (project_id);

COMMENT ON VIEW project_coverage IS
  'Independent project session and retained-evidence counts; no evidence returns NULL ratio and missing state.';

-- +goose Down
-- Correct denominator semantics are retained across a version rollback.
SELECT 1;
