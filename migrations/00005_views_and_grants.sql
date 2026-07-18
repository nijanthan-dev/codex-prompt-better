-- +goose Up
SET search_path TO prompt_better, public;

CREATE VIEW current_project_dimensions
WITH (security_barrier = true) AS
SELECT p.project_id, p.created_at, v.project_version_id, v.version_number,
       v.classification, v.lifecycle_state, v.valid_from
FROM projects p
JOIN project_versions v ON v.project_id=p.project_id AND v.valid_to IS NULL;

CREATE VIEW current_source_dimensions
WITH (security_barrier = true) AS
SELECT s.source_id, s.source_kind, s.created_at, v.source_version_id,
       v.version_number, v.adapter_version, v.product_surface,
       v.coverage_state, v.enabled, v.valid_from
FROM sources s
JOIN source_versions v ON v.source_id=s.source_id AND v.valid_to IS NULL;

CREATE VIEW collection_freshness
WITH (security_barrier = true) AS
SELECT s.source_id, s.source_kind, s.coverage_state, s.enabled,
       max(c.updated_at) AS latest_cursor_at,
       count(c.collection_cursor_id) AS cursor_count,
       CASE WHEN NOT s.enabled THEN 'disabled'
            WHEN count(c.collection_cursor_id)=0 THEN 'missing'
            ELSE s.coverage_state END AS effective_coverage
FROM current_source_dimensions s
LEFT JOIN collection_cursors c ON c.source_id=s.source_id
GROUP BY s.source_id,s.source_kind,s.coverage_state,s.enabled;

CREATE VIEW project_coverage
WITH (security_barrier = true) AS
WITH session_counts AS (
    SELECT project_id,count(*) AS session_count FROM sessions GROUP BY project_id
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

CREATE VIEW trajectory_usage
WITH (security_barrier = true) AS
SELECT trajectory_id,metric_kind,product_surface,accounting_regime,native_unit,
       knowledge_state,count(*) AS observation_count,
       sum(value_numeric) FILTER (WHERE knowledge_state IN ('observed','derived')) AS known_value
FROM observations
WHERE observation_kind='usage'
GROUP BY trajectory_id,metric_kind,product_surface,accounting_regime,native_unit,knowledge_state;

CREATE VIEW tool_loop_summaries
WITH (security_barrier = true) AS
SELECT p.trajectory_id,p.phase_id,p.ordinal,p.started_at,p.ended_at,
       count(DISTINCT tc.tool_call_id) AS tool_call_count,
       max(e.event_name) FILTER (WHERE e.event_kind='stop') AS stop_reason,
       p.knowledge_state
FROM phases p
LEFT JOIN tool_calls tc ON tc.phase_id=p.phase_id
LEFT JOIN execution_events e ON e.phase_id=p.phase_id AND e.event_kind='stop'
WHERE p.phase_kind='tool'
GROUP BY p.trajectory_id,p.phase_id,p.ordinal,p.started_at,p.ended_at,p.knowledge_state;

CREATE VIEW governance_metric_results
WITH (security_barrier = true) AS
SELECT aw.project_id,aw.audit_window_id,ar.audit_revision_id,ar.revision_number,
       ar.source_watermark_at,ar.coverage_state,mr.metric_result_id,mr.metric_name,
       mr.metric_version,mr.native_value,mr.native_unit,mr.numerator,mr.denominator,
       mr.sample_count,
       CASE WHEN mr.denominator IS NULL THEN 'not_applicable_or_unknown'
            WHEN mr.denominator=0 THEN 'invalid' ELSE 'explicit_nonzero' END AS denominator_semantics,
       mr.provenance
FROM audit_windows aw
JOIN audit_revisions ar ON ar.audit_window_id=aw.audit_window_id
JOIN metric_results mr ON mr.audit_revision_id=ar.audit_revision_id;

CREATE VIEW rare_cohort_safe_metrics
WITH (security_barrier = true) AS
SELECT project_id,audit_window_id,audit_revision_id,metric_result_id,metric_name,
       metric_version,native_unit,
       CASE WHEN sample_count>=5 THEN native_value ELSE NULL END AS native_value,
       CASE WHEN sample_count>=5 THEN numerator ELSE NULL END AS numerator,
       CASE WHEN sample_count>=5 THEN denominator ELSE NULL END AS denominator,
       sample_count,sample_count IS NULL OR sample_count<5 AS suppressed,
       coverage_state,provenance
FROM governance_metric_results;

CREATE VIEW active_recommendations
WITH (security_barrier = true) AS
SELECT r.recommendation_id,r.project_id,r.finding_id,r.recommendation_kind,
       r.lifecycle_state,r.approval_required,r.verification_kind,r.created_at,
       r.updated_at,f.finding_kind,f.confidence
FROM recommendations r
LEFT JOIN findings f ON f.finding_id=r.finding_id AND f.deleted_at IS NULL
WHERE r.deleted_at IS NULL;

CREATE VIEW retention_candidates
WITH (security_barrier = true) AS
SELECT e.evidence_artifact_id,e.project_id,e.source_id,e.classification,
       p.retention_policy_id,e.retained_until
FROM evidence_artifacts e
JOIN retention_policies p ON p.project_id=e.project_id
 AND p.classification=e.classification AND p.retired_at IS NULL
WHERE e.deleted_at IS NULL
  AND coalesce(e.retained_until,e.observed_at+p.retain_for_seconds*interval '1 second')<=statement_timestamp()
  AND NOT p.legal_hold;

COMMENT ON VIEW current_project_dimensions IS 'Exactly one current SCD2 project dimension per project.';
COMMENT ON VIEW current_source_dimensions IS 'Exactly one current SCD2 source dimension per source.';
COMMENT ON VIEW collection_freshness IS 'Configured sources retain explicit missing and disabled coverage.';
COMMENT ON VIEW project_coverage IS 'Independent session and retained-evidence counts with explicit missing denominators.';
COMMENT ON VIEW trajectory_usage IS 'Usage facts retain source-native unit, surface, regime, and knowledge state.';
COMMENT ON VIEW tool_loop_summaries IS 'Derived tool-loop summaries; loops are not persisted independently.';
COMMENT ON VIEW governance_metric_results IS 'Immutable revision metrics with explicit denominator semantics.';
COMMENT ON VIEW rare_cohort_safe_metrics IS 'Metric components are suppressed below five samples.';
COMMENT ON VIEW active_recommendations IS 'Active recommendation lifecycle with optional finding context.';
COMMENT ON VIEW retention_candidates IS 'Due retained evidence excluding legal holds.';

REVOKE ALL ON SCHEMA prompt_better FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA prompt_better FROM PUBLIC;
GRANT USAGE ON SCHEMA prompt_better TO prompt_better_migrator,prompt_better_runtime,prompt_better_collector,prompt_better_reporter;
GRANT ALL ON ALL TABLES IN SCHEMA prompt_better TO prompt_better_migrator;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA prompt_better TO prompt_better_runtime;
REVOKE ALL ON key_versions FROM prompt_better_runtime;
GRANT SELECT ON key_versions TO prompt_better_runtime;
GRANT SELECT ON current_source_dimensions,projects,project_aliases,retention_policies TO prompt_better_collector;
GRANT SELECT,INSERT,UPDATE ON sources,source_versions TO prompt_better_collector;
GRANT SELECT,INSERT,UPDATE ON collection_cursors,sessions,tasks,trajectories,turns,responses,items,phases,state_epochs,tool_calls,
    execution_events,evidence_artifacts,evidence_links,observations TO prompt_better_collector;
GRANT SELECT ON collection_freshness,project_coverage,trajectory_usage,tool_loop_summaries,
    rare_cohort_safe_metrics,active_recommendations,current_project_dimensions,current_source_dimensions TO prompt_better_reporter;
GRANT SELECT ON public.schema_migrations TO prompt_better_runtime,prompt_better_collector;

-- +goose Down
SET search_path TO prompt_better, public;
DROP VIEW retention_candidates;
DROP VIEW active_recommendations;
DROP VIEW rare_cohort_safe_metrics;
DROP VIEW governance_metric_results;
DROP VIEW tool_loop_summaries;
DROP VIEW trajectory_usage;
DROP VIEW project_coverage;
DROP VIEW collection_freshness;
DROP VIEW current_source_dimensions;
DROP VIEW current_project_dimensions;
