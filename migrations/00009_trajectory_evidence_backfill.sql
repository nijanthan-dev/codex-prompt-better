-- +goose Up
SET search_path TO prompt_better, public;

INSERT INTO evidence_links
    (evidence_link_id, evidence_artifact_id, target_kind, target_id, link_kind, created_at)
SELECT md5('trajectory-evidence:' || e.evidence_artifact_id::text || ':' || lineage.trajectory_id::text)::uuid,
    e.evidence_artifact_id, 'trajectory', lineage.trajectory_id, 'observed_in', e.observed_at
FROM evidence_artifacts e
JOIN (
    SELECT evidence_artifact_id, trajectory_id FROM usage_observations
    WHERE evidence_artifact_id IS NOT NULL AND trajectory_id IS NOT NULL
    UNION
    SELECT evidence_artifact_id, trajectory_id FROM cache_observations
    WHERE evidence_artifact_id IS NOT NULL AND trajectory_id IS NOT NULL
) lineage USING (evidence_artifact_id)
ON CONFLICT (evidence_link_id) DO NOTHING;

-- +goose Down
-- Backfilled provenance is retained because later collection may depend on it.
SELECT 1;
