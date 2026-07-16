-- +goose Up
SET search_path TO prompt_better, public;

CREATE VIEW current_project_dimensions
WITH (security_barrier = true) AS
SELECT p.project_id, p.created_at, v.project_version_id, v.version_number,
       v.classification, v.lifecycle_state, v.valid_from
FROM projects p
JOIN project_versions v ON v.project_id = p.project_id AND v.valid_to IS NULL;
COMMENT ON VIEW current_project_dimensions IS
  'Exactly one current SCD2 project dimension per project; history remains in project_versions.';

CREATE VIEW current_source_dimensions
WITH (security_barrier = true) AS
SELECT s.source_id, s.source_kind, s.created_at, v.source_version_id,
       v.version_number, v.adapter_version, v.product_surface,
       v.coverage_state, v.enabled, v.valid_from
FROM sources s
JOIN source_versions v ON v.source_id = s.source_id AND v.valid_to IS NULL;
COMMENT ON VIEW current_source_dimensions IS
  'Exactly one current SCD2 source dimension per source; history remains in source_versions.';

CREATE VIEW collection_freshness
WITH (security_barrier = true) AS
SELECT
    s.source_id,
    s.source_kind,
    s.coverage_state,
    s.enabled,
    max(c.updated_at) AS latest_cursor_at,
    count(c.collection_cursor_id) AS cursor_count,
    CASE
        WHEN NOT s.enabled THEN 'disabled'
        WHEN count(c.collection_cursor_id) = 0 THEN 'missing'
        ELSE s.coverage_state
    END AS effective_coverage
FROM current_source_dimensions s
LEFT JOIN collection_cursors c ON c.source_id = s.source_id
GROUP BY s.source_id, s.source_kind, s.coverage_state, s.enabled;
COMMENT ON VIEW collection_freshness IS
  'One row per configured source; zero cursors remains explicit missing coverage.';

CREATE VIEW project_coverage
WITH (security_barrier = true) AS
SELECT
    p.project_id,
    count(DISTINCT s.session_id) AS session_count,
    count(e.evidence_artifact_id) AS evidence_count,
    count(e.evidence_artifact_id) FILTER (WHERE e.coverage_state = 'complete') AS complete_evidence_count,
    CASE
        WHEN count(e.evidence_artifact_id) = 0 THEN NULL
        ELSE count(e.evidence_artifact_id) FILTER (WHERE e.coverage_state = 'complete')::numeric
             / count(e.evidence_artifact_id)::numeric
    END AS coverage_ratio,
    CASE WHEN count(e.evidence_artifact_id) = 0 THEN 'missing' ELSE 'observed' END AS denominator_state
FROM projects p
LEFT JOIN sessions s ON s.project_id = p.project_id
LEFT JOIN evidence_artifacts e
    ON e.project_id = p.project_id AND e.deleted_at IS NULL
GROUP BY p.project_id;
COMMENT ON VIEW project_coverage IS
  'Denominator is retained evidence rows; no rows returns NULL ratio and missing state, never zero.';

CREATE VIEW trajectory_usage
WITH (security_barrier = true) AS
SELECT
    trajectory_id,
    metric_kind,
    product_surface,
    accounting_regime,
    usage_unit,
    knowledge_state,
    count(*) AS observation_count,
    sum(value_numeric) FILTER (WHERE knowledge_state IN ('observed', 'derived')) AS known_value
FROM usage_observations
GROUP BY trajectory_id, metric_kind, product_surface, accounting_regime,
         usage_unit, knowledge_state;
COMMENT ON VIEW trajectory_usage IS
  'Product surface, accounting regime, native unit, and knowledge state remain separate; unknown is not zero.';

CREATE VIEW governance_metric_results
WITH (security_barrier = true) AS
SELECT
    aw.project_id,
    aw.audit_window_id,
    ar.audit_revision_id,
    ar.revision_number,
    ar.source_watermark_at,
    ar.coverage_state,
    mr.metric_result_id,
    mr.metric_name,
    mr.metric_version,
    mr.native_value,
    mr.native_unit,
    mr.numerator,
    mr.denominator,
    mr.sample_count,
    CASE
        WHEN mr.denominator IS NULL THEN 'not_applicable_or_unknown'
        WHEN mr.denominator = 0 THEN 'invalid'
        ELSE 'explicit_nonzero'
    END AS denominator_semantics,
    mr.provenance
FROM audit_windows aw
JOIN audit_revisions ar ON ar.audit_window_id = aw.audit_window_id
JOIN metric_results mr ON mr.audit_revision_id = ar.audit_revision_id;
COMMENT ON VIEW governance_metric_results IS
  'Immutable audit revision metrics with explicit numerator, denominator, native unit, coverage, and provenance.';

CREATE VIEW rare_cohort_safe_metrics
WITH (security_barrier = true) AS
SELECT
    project_id,
    audit_window_id,
    audit_revision_id,
    metric_result_id,
    metric_name,
    metric_version,
    native_unit,
    CASE WHEN sample_count >= 5 THEN native_value ELSE NULL END AS native_value,
    CASE WHEN sample_count >= 5 THEN numerator ELSE NULL END AS numerator,
    CASE WHEN sample_count >= 5 THEN denominator ELSE NULL END AS denominator,
    sample_count,
    sample_count IS NULL OR sample_count < 5 AS suppressed,
    coverage_state,
    provenance
FROM governance_metric_results;
COMMENT ON VIEW rare_cohort_safe_metrics IS
  'Values and components are suppressed below five samples; sample and coverage remain visible.';

CREATE VIEW active_recommendations
WITH (security_barrier = true) AS
SELECT
    r.recommendation_id,
    r.project_id,
    r.finding_id,
    r.recommendation_kind,
    r.lifecycle_state,
    r.approval_required,
    r.verification_kind,
    r.created_at,
    r.updated_at,
    f.finding_kind,
    f.confidence
FROM recommendations r
LEFT JOIN findings f ON f.finding_id = r.finding_id AND f.deleted_at IS NULL
WHERE r.deleted_at IS NULL;
COMMENT ON VIEW active_recommendations IS
  'Non-deleted recommendation lifecycle with optional surviving finding context.';

CREATE VIEW retention_candidates
WITH (security_barrier = true) AS
SELECT
    e.evidence_artifact_id,
    e.project_id,
    e.source_id,
    e.classification,
    p.retention_policy_id,
    e.retained_until
FROM evidence_artifacts e
JOIN retention_policies p
    ON p.project_id = e.project_id
   AND p.classification = e.classification
   AND p.retired_at IS NULL
WHERE e.deleted_at IS NULL
  AND COALESCE(
      e.retained_until,
      e.observed_at + p.retain_for_seconds * interval '1 second'
  ) <= statement_timestamp()
  AND NOT p.legal_hold;
COMMENT ON VIEW retention_candidates IS
  'Due normalized evidence only; default policy is 30 days, explicit earlier/later cutoffs apply, and legal holds are excluded.';

REVOKE ALL ON SCHEMA prompt_better FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA prompt_better FROM PUBLIC;
GRANT USAGE ON SCHEMA prompt_better TO prompt_better_migrator;
GRANT USAGE ON SCHEMA prompt_better TO prompt_better_runtime;
GRANT USAGE ON SCHEMA prompt_better TO prompt_better_collector;
GRANT USAGE ON SCHEMA prompt_better TO prompt_better_reporter;
GRANT ALL ON ALL TABLES IN SCHEMA prompt_better TO prompt_better_migrator;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA prompt_better TO prompt_better_runtime;
REVOKE ALL ON key_versions FROM prompt_better_runtime;
GRANT SELECT ON key_versions TO prompt_better_runtime;
GRANT SELECT ON sources, source_versions, current_source_dimensions, projects,
    project_aliases, retention_policies TO prompt_better_collector;
GRANT SELECT, INSERT, UPDATE ON collection_cursors, evidence_artifacts,
    project_attributions, source_assertions, usage_observations,
    cache_observations TO prompt_better_collector;
GRANT SELECT ON collection_freshness, project_coverage, trajectory_usage,
    rare_cohort_safe_metrics, active_recommendations, current_project_dimensions,
    current_source_dimensions TO prompt_better_reporter;

-- +goose Down
SET search_path TO prompt_better, public;
DROP VIEW retention_candidates;
DROP VIEW active_recommendations;
DROP VIEW rare_cohort_safe_metrics;
DROP VIEW governance_metric_results;
DROP VIEW trajectory_usage;
DROP VIEW project_coverage;
DROP VIEW collection_freshness;
DROP VIEW current_source_dimensions;
DROP VIEW current_project_dimensions;
