package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nijanthan-dev/codex-prompt-better/internal/audit"
	"github.com/nijanthan-dev/codex-prompt-better/internal/baseline"
	"github.com/nijanthan-dev/codex-prompt-better/internal/eval"
	"github.com/nijanthan-dev/codex-prompt-better/internal/metrics"
	"github.com/nijanthan-dev/codex-prompt-better/internal/recommendation"
	"github.com/nijanthan-dev/codex-prompt-better/internal/replay"
	"github.com/nijanthan-dev/codex-prompt-better/internal/workload"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

// AuditProject computes one bounded project audit from normalized PostgreSQL data.
func (r *Repository) AuditProject(ctx context.Context, request contracts.AuditProjectRequest) (contracts.AuditProjectResult, error) {
	lock, err := r.pool.Acquire(ctx)
	if err != nil {
		return contracts.AuditProjectResult{}, errors.New("acquire audit revision lock")
	}
	defer lock.Release()
	windowKey := fmt.Sprintf("audit-window:%s:%s:%s:%s",
		request.Scope, request.Reference, request.StartsAt.UTC(), request.EndsAt.UTC())
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, windowKey); err != nil {
		return contracts.AuditProjectResult{}, errors.New("lock audit revision")
	}
	defer func() {
		_, _ = lock.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, windowKey)
	}()
	engine, err := audit.New(projectAuditSource{repository: r})
	if err != nil {
		return contracts.AuditProjectResult{}, err
	}
	result, err := engine.Run(ctx, request)
	if err != nil {
		return contracts.AuditProjectResult{}, err
	}
	if err := r.persistAuditProject(ctx, request, result); err != nil {
		return contracts.AuditProjectResult{}, err
	}
	return result, nil
}

func (r *Repository) persistAuditProject(ctx context.Context,
	request contracts.AuditProjectRequest,
	result contracts.AuditProjectResult,
) error {
	revisionHash, err := hex.DecodeString(result.RevisionHash)
	if err != nil || len(revisionHash) != sha256.Size {
		return errors.New("invalid audit revision hash")
	}
	windowID := collectionBatchUUID(fmt.Sprintf("audit-window:%s:%s:%s:%s",
		request.Scope, request.Reference, request.StartsAt.UTC(), request.EndsAt.UTC()))
	revisionID := collectionBatchUUID("audit-revision:" + result.RevisionHash)
	projectID, err := r.projectIDForAudit(ctx, request)
	if err != nil {
		return err
	}
	return r.WithSerializable(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.audit_windows
			(audit_window_id,project_id,window_kind,starts_at,ends_at,as_of,
			 timezone_name,immutable_since)
			VALUES ($1,$2,$3,$4,$5,$6,'UTC',$6)
			ON CONFLICT (audit_window_id) DO NOTHING`,
			windowID, projectID, string(request.Scope), request.StartsAt,
			request.EndsAt, request.AsOf); err != nil {
			return errors.New("persist project audit window")
		}
		var revisionNumber int
		var priorRevisionID *string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(revision_number),0)+1,
			(SELECT audit_revision_id::text FROM prompt_better.audit_revisions
			 WHERE audit_window_id=$1 ORDER BY revision_number DESC LIMIT 1)
			FROM prompt_better.audit_revisions WHERE audit_window_id=$1`,
			windowID).Scan(&revisionNumber, &priorRevisionID); err != nil {
			return errors.New("read project audit revision")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.audit_revisions
			(audit_revision_id,audit_window_id,revision_number,source_watermark_at,
			 coverage_state,revision_hash,created_at,prior_revision_id)
			VALUES ($1,$2,$3,$4,$5,$6,$4,$7)
			ON CONFLICT (audit_revision_id) DO NOTHING`,
			revisionID, windowID, revisionNumber, request.AsOf,
			result.Coverage, revisionHash, priorRevisionID); err != nil {
			return errors.New("persist project audit revision")
		}
		if err := persistMetricDefinitions(ctx, tx); err != nil {
			return err
		}
		if err := persistRevisionEvidence(ctx, tx, revisionID, request); err != nil {
			return err
		}
		evaluationID, err := persistEvaluationRun(ctx, tx, revisionID, request, result)
		if err != nil {
			return err
		}
		for _, metric := range result.Metrics {
			if err := persistMetricResult(ctx, tx, revisionID, evaluationID,
				request.AsOf, metric); err != nil {
				return err
			}
		}
		for _, confounder := range result.Confounders {
			confounderID := collectionBatchUUID(revisionID + ":confounder:" +
				confounder.Kind + ":" + confounder.State + ":" + confounder.Provenance)
			if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.confounder_labels
				(confounder_label_id,audit_revision_id,label_kind,label_state,
				 provenance,confidence,observed_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (confounder_label_id) DO NOTHING`,
				confounderID, revisionID, confounder.Kind, confounder.State,
				confounder.Provenance, confidenceValue(confounder.Confidence),
				request.AsOf); err != nil {
				return errors.New("persist project audit confounder")
			}
		}
		findingIDs := map[string]string{}
		for _, finding := range result.Findings {
			findingID := collectionBatchUUID(revisionID + ":finding:" + finding.Code)
			findingIDs[finding.Code] = findingID
			counterevidence, marshalErr := json.Marshal(finding.Counterevidence)
			if marshalErr != nil {
				return errors.New("encode project audit counterevidence")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.findings
				(finding_id,audit_revision_id,finding_kind,detector_name,
				 detector_version,confidence,status,created_at,cause,exception_check,
				 counterevidence)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
				ON CONFLICT (finding_id) DO NOTHING`,
				findingID, revisionID, finding.Code, finding.Detector,
				finding.DetectorVersion, confidenceValue(finding.Confidence),
				finding.Classification, request.AsOf, finding.Cause,
				finding.ExceptionCheck, counterevidence); err != nil {
				return errors.New("persist project audit finding")
			}
			if err := persistEvidenceLinks(ctx, tx, revisionID, "finding", findingID,
				finding.EvidenceRefs, request.AsOf); err != nil {
				return err
			}
		}
		for _, recommendation := range result.Recommendations {
			findingID := findingIDs[recommendationFindingCode(recommendation.Code)]
			if findingID == "" {
				findingID = firstFindingID(findingIDs)
			}
			recommendationID := collectionBatchUUID(revisionID + ":recommendation:" + recommendation.Code)
			protectedGuardrails, marshalErr := json.Marshal(recommendation.ProtectedGuardrails)
			if marshalErr != nil {
				return errors.New("encode recommendation guardrails")
			}
			risks, marshalErr := json.Marshal(recommendation.Risks)
			if marshalErr != nil {
				return errors.New("encode recommendation risks")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.recommendations
				(recommendation_id,finding_id,project_id,recommendation_kind,
				 lifecycle_state,approval_required,verification_kind,action_code,
				 policy_version,cooldown_until,evidence_revision,created_at,updated_at,target_surface,
				 action_text,expected_movement,protected_guardrails,risks)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$4,$8,NULL,$9,$10,$10,$11,$12,$13,$14,$15)
				ON CONFLICT (recommendation_id) DO NOTHING`,
				recommendationID, nullableString(findingID), projectID,
				recommendation.Code, recommendation.LifecycleState,
				recommendation.ApprovalRequired, recommendation.Verification,
				recommendation.PolicyVersion, evidenceRevision(recommendation.EvidenceRefs),
				request.AsOf,
				recommendation.TargetSurface, recommendation.Action,
				recommendation.ExpectedMovement, protectedGuardrails, risks); err != nil {
				return errors.New("persist project audit recommendation")
			}
		}
		return nil
	})
}

func (r *Repository) projectIDForAudit(ctx context.Context,
	request contracts.AuditProjectRequest,
) (*string, error) {
	if request.Scope == contracts.AuditScopePortfolio {
		return nil, nil
	}
	if request.Scope == contracts.AuditScopeProject {
		value := request.Reference
		return &value, nil
	}
	filter, _, args, err := auditFilter(request)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT DISTINCT s.project_id::text
		FROM prompt_better.sessions s
		LEFT JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
		WHERE $1::timestamptz IS NOT NULL AND $2::timestamptz IS NOT NULL
		  AND $3::timestamptz IS NOT NULL AND %s LIMIT 2`, filter)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, errors.New("resolve audit project lineage")
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, errors.New("decode audit project lineage")
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("iterate audit project lineage")
	}
	if len(values) != 1 {
		return nil, contracts.NewError(contracts.ErrorCodeCoverageIncomplete,
			"single project lineage unavailable", "reference", false)
	}
	return &values[0], nil
}

func persistEvaluationRun(ctx context.Context, tx pgx.Tx, revisionID string,
	request contracts.AuditProjectRequest, result contracts.AuditProjectResult,
) (string, error) {
	fixture := struct {
		Scope     contracts.AuditScope
		Reference string
		StartsAt  time.Time
		EndsAt    time.Time
		AsOf      time.Time
		Sources   []string
	}{request.Scope, request.Reference, request.StartsAt, request.EndsAt, request.AsOf,
		append([]string{}, request.ConfiguredSources...)}
	comparison := replay.Comparison{
		CandidateHash:    result.RevisionHash,
		Statuses:         map[string]contracts.MetricStatus{},
		Outcome:          evaluationOutcome(result),
		QualityGateState: evaluationQualityGate(result),
	}
	for _, metric := range result.Metrics {
		comparison.Statuses[metric.Name] = metric.Status
	}
	run, err := eval.NewRun("eval-v1", fixture, result.Window, eval.Components{
		Model: "observed-local-model", Compiler: "compiler-v1",
		Policy: "recommendation-v1", Metrics: metrics.Definitions(),
	}, comparison)
	if err != nil {
		return "", err
	}
	fixtureHash, _ := hex.DecodeString(run.FixtureHash)
	configHash, _ := hex.DecodeString(run.ConfigHash)
	modelHash, _ := hex.DecodeString(run.ModelHash)
	compilerHash, _ := hex.DecodeString(run.CompilerHash)
	policyHash, _ := hex.DecodeString(run.PolicyHash)
	metricHash, _ := hex.DecodeString(run.MetricHash)
	runHash, _ := hex.DecodeString(run.RunHash)
	id := collectionBatchUUID(revisionID + ":evaluation")
	if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.evaluation_runs
		(evaluation_run_id,audit_revision_id,evaluator_name,evaluator_version,
		 started_at,completed_at,outcome,knowledge_state,fixture_hash,config_hash,
		 model_hash,compiler_hash,policy_hash,metric_hash,run_hash,decision)
		VALUES ($1,$2,'deterministic_governance','eval-v1',$3,$3,'complete','observed',
		 $4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (evaluation_run_id) DO NOTHING`,
		id, revisionID, request.AsOf, fixtureHash, configHash, modelHash,
		compilerHash, policyHash, metricHash, runHash, run.Decision); err != nil {
		return "", errors.New("persist project audit evaluation")
	}
	return id, nil
}

func evaluationQualityGate(result contracts.AuditProjectResult) string {
	for _, guardrail := range result.Guardrails {
		if guardrail.State == "fail" {
			return "fail"
		}
		if guardrail.State != "pass" {
			return "unknown"
		}
	}
	return "pass"
}

func evaluationOutcome(result contracts.AuditProjectResult) string {
	improved := false
	for _, metric := range result.Metrics {
		switch metric.Status {
		case contracts.MetricStatusWorsened:
			return "regressed"
		case contracts.MetricStatusMixed, contracts.MetricStatusInsufficient:
			return "mixed"
		case contracts.MetricStatusImproved:
			improved = true
		}
	}
	if improved {
		return "improved"
	}
	return "unchanged"
}

func persistMetricDefinitions(ctx context.Context, tx pgx.Tx) error {
	for _, definition := range metrics.Definitions() {
		encoded, err := json.Marshal(definition)
		if err != nil {
			return errors.New("encode metric definition")
		}
		digest := sha256.Sum256(encoded)
		id := collectionBatchUUID("metric-definition:" + definition.Name + ":" + definition.Version)
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.metric_definitions
			(metric_definition_id,metric_name,metric_version,native_unit,polarity,
			 minimum_sample,practical_change,definition_hash,definition_json,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,statement_timestamp())
			ON CONFLICT (metric_name,metric_version) DO NOTHING`,
			id, definition.Name, definition.Version, definition.NativeUnit,
			definition.Polarity, definition.MinimumSample, definition.PracticalChange,
			digest[:], encoded); err != nil {
			return errors.New("persist metric definition")
		}
	}
	return nil
}

func persistMetricResult(ctx context.Context, tx pgx.Tx, revisionID, evaluationID string,
	computedAt time.Time, metric contracts.MetricResult,
) error {
	id := collectionBatchUUID(revisionID + ":metric:" + metric.Name + ":" + metric.Version)
	definitionID := collectionBatchUUID("metric-definition:" + metric.Name + ":" + metric.Version)
	exclusions, err := json.Marshal(metric.Exclusions)
	if err != nil {
		return errors.New("encode metric exclusions")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.metric_results
		(metric_result_id,audit_revision_id,evaluation_run_id,metric_definition_id,metric_name,
		 metric_version,native_value,native_unit,numerator,denominator,sample_count,
		 coverage_state,provenance,status,uncertainty,exclusions,computed_at,
		 previous_value,rolling_median,rolling_mad,baseline_sample_count,
		 workload_adjusted_residual,status_reason,confidence_label)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'derived',$13,$14,$15,$16,
		 $17,$18,$19,$20,$21,$22,$23)
		ON CONFLICT (audit_revision_id,metric_name,metric_version) DO NOTHING`,
		id, revisionID, evaluationID, definitionID, metric.Name, metric.Version, metric.NativeValue,
		metric.NativeUnit, metric.Numerator, metric.Denominator, metric.SampleCount,
		metric.Coverage, metric.Status, metric.Uncertainty, exclusions, computedAt,
		metric.PreviousValue, metric.RollingMedian, metric.RollingMAD,
		metric.BaselineSampleCount, metric.WorkloadAdjustedResidual,
		metric.StatusReason, metric.Confidence); err != nil {
		return errors.New("persist project audit metric")
	}
	return persistEvidenceLinks(ctx, tx, revisionID, "metric_result", id,
		metric.EvidenceRefs, computedAt)
}

func persistEvidenceLinks(ctx context.Context, tx pgx.Tx, revisionID, kind, targetID string,
	refs []string, observedAt time.Time,
) error {
	for _, ref := range refs {
		id := collectionBatchUUID(revisionID + ":" + kind + ":" + targetID + ":" + ref)
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.evidence_links
			(evidence_link_id,evidence_artifact_id,target_kind,target_id,link_kind,created_at)
			SELECT $1,$2,$3,$4,'supports',$5
			WHERE EXISTS (
				SELECT 1 FROM prompt_better.evidence_artifacts
				WHERE evidence_artifact_id=$2)
			ON CONFLICT (evidence_link_id) DO NOTHING`,
			id, ref, kind, targetID, observedAt); err != nil {
			return errors.New("persist audit evidence link")
		}
	}
	return nil
}

func persistRevisionEvidence(ctx context.Context, tx pgx.Tx, revisionID string,
	request contracts.AuditProjectRequest,
) error {
	filter, _, args, err := auditFilter(request)
	if err != nil {
		return err
	}
	revisionParameter := len(args) + 1
	query := fmt.Sprintf(`
		INSERT INTO prompt_better.evidence_links
			(evidence_link_id,evidence_artifact_id,target_kind,target_id,link_kind,created_at)
		SELECT md5($%d::text || ':' || e.evidence_artifact_id::text)::uuid,
			e.evidence_artifact_id,'audit_revision',$%d::uuid,'supports',$3
		FROM prompt_better.sessions s
		LEFT JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
		JOIN prompt_better.evidence_artifacts e ON e.session_id=s.session_id
			AND e.observed_at >= $1 AND e.observed_at < $2
			AND e.observed_at <= $3 AND e.deleted_at IS NULL
		WHERE %s
		ON CONFLICT (evidence_link_id) DO NOTHING`,
		revisionParameter, revisionParameter, filter)
	args = append(args, revisionID)
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return errors.New("persist audit revision evidence links")
	}
	return nil
}

func confidenceValue(value string) *float64 {
	numeric := map[string]float64{"high": 0.90, "medium": 0.65, "low": 0.35}
	result, ok := numeric[value]
	if !ok {
		return nil
	}
	return &result
}

func firstFindingID(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return values[keys[0]]
}

func recommendationFindingCode(code string) string {
	return map[string]string{
		"restore_scope_attribution":    "scope_attribution_gap",
		"complete_checkpoint_contract": "checkpoint_gap",
		"wait_on_state_change":         "passive_polling",
		"reuse_unchanged_evidence":     "repeated_unchanged_call",
		"validate_after_mutation":      "missing_validation",
		"restore_redaction_coverage":   "privacy_redaction_gap",
	}[code]
}

type projectAuditSource struct {
	repository *Repository
}

func (source projectAuditSource) Snapshot(ctx context.Context, request contracts.AuditProjectRequest) (audit.Snapshot, error) {
	filter, turnFilter, args, err := auditFilter(request)
	if err != nil {
		return audit.Snapshot{}, err
	}
	metricsInput, err := source.metrics(ctx, filter, turnFilter, args)
	if err != nil {
		return audit.Snapshot{}, err
	}
	privacySuppressed := (request.Scope == contracts.AuditScopeTask ||
		request.Scope == contracts.AuditScopeTrajectory) && metricsInput.CompletedTurns < 3
	if privacySuppressed {
		metricsInput.EvidenceRefs = []string{}
		metricsInput.Coverage = contracts.CoverageStatePartial
	}
	overhead, err := source.overhead(ctx, filter, args)
	if err != nil {
		return audit.Snapshot{}, err
	}
	passiveRefs, err := source.toolRefs(ctx, filter, turnFilter, args, "passive_wait")
	if err != nil {
		return audit.Snapshot{}, err
	}
	repeatedRefs, err := source.toolRefs(ctx, filter, turnFilter, args, "repeated_unchanged")
	if err != nil {
		return audit.Snapshot{}, err
	}
	comparisons, err := source.comparisons(ctx, request, metricsInput, privacySuppressed)
	if err != nil {
		return audit.Snapshot{}, err
	}
	contributions, err := source.contributions(ctx, request)
	if err != nil {
		return audit.Snapshot{}, err
	}
	confounders, err := source.confounders(ctx, request)
	if err != nil {
		return audit.Snapshot{}, err
	}
	confoundersMatched, err := source.previousConfoundersMatched(ctx, request)
	if err != nil {
		return audit.Snapshot{}, err
	}
	for index := range confounders {
		confounders[index].Matched = confoundersMatched
	}
	signature, err := source.dimensionSignature(ctx, request)
	if err != nil {
		return audit.Snapshot{}, err
	}
	guardrails := currentGuardrails(metricsInput)
	invocations, err := source.invocationCounts(ctx, filter, turnFilter, args)
	if err != nil {
		return audit.Snapshot{}, err
	}
	workloadEffects, err := source.workloadEffects(ctx, request)
	if err != nil {
		return audit.Snapshot{}, err
	}
	revision, lateEvidence, err := source.revisionState(ctx, request)
	if err != nil {
		return audit.Snapshot{}, err
	}
	recommendationContext, err := source.recommendationContext(ctx, request)
	if err != nil {
		return audit.Snapshot{}, err
	}
	recommendationContext.MaterialEvidenceRevision = evidenceRevision(metricsInput.EvidenceRefs)
	reference := request.Reference
	if privacySuppressed {
		reference = opaqueAuditAlias(request.Reference)
		passiveRefs = []string{}
		repeatedRefs = []string{}
		contributions = []contracts.ScopeContribution{}
		confounders = []contracts.ConfounderStratum{}
		invocations = contracts.InvocationCounts{}
		workloadEffects = []contracts.WorkloadDecomposition{}
	}
	omitted := 0
	contributions, omitted = boundedSlice(contributions, 100, omitted)
	confounders, omitted = boundedSlice(confounders, 100, omitted)
	workloadEffects, omitted = boundedSlice(workloadEffects, 40, omitted)
	versions := opaqueVersions(signature)
	versions, omitted = boundedSlice(versions, 100, omitted)
	return audit.Snapshot{
		Reference: reference, Scope: request.Scope,
		StartsAt: request.StartsAt, EndsAt: request.EndsAt, AsOf: request.AsOf,
		Coverage: metricsInput.Coverage, Metrics: metricsInput,
		GovernanceOverhead: overhead, PassivePollingRefs: passiveRefs,
		RepeatedCallRefs: repeatedRefs,
		Comparisons:      comparisons,
		Window: contracts.AuditWindowProvenance{
			StartsAt: request.StartsAt.UTC(), EndsAt: request.EndsAt.UTC(),
			AsOf: request.AsOf.UTC(), Timezone: "UTC",
			BaselineVersion: baseline.Version, CountingMode: "logical-invocation-v1",
			WorkloadVersion: "workload-v1", SourceVersions: versions,
			Revision: revision, LateEvidence: lateEvidence,
		},
		Contributions: contributions, ConfounderStrata: confounders,
		Guardrails: guardrails, InvocationCounts: invocations,
		WorkloadEffects: workloadEffects, RecommendationContext: recommendationContext,
		OmittedCount: omitted,
	}, nil
}

func boundedSlice[T any](values []T, limit, omitted int) ([]T, int) {
	if len(values) <= limit {
		return values, omitted
	}
	return values[:limit], omitted + len(values) - limit
}

func opaqueAuditAlias(value string) string {
	digest := sha256.Sum256([]byte("audit-reference:" + value))
	return "opaque-" + hex.EncodeToString(digest[:12])
}

func opaqueVersions(signature string) []string {
	parts := strings.Split(signature, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, opaqueAuditAlias(part))
		}
	}
	return result
}

func (source projectAuditSource) contributions(ctx context.Context,
	request contracts.AuditProjectRequest,
) ([]contracts.ScopeContribution, error) {
	filter, turnFilter, args, err := auditFilter(request)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		WITH selected_turns AS (
			SELECT s.project_id, t.turn_id, tr.trajectory_id,
				coalesce(task.task_kind,'unknown') AS workload_class
			FROM prompt_better.sessions s
			JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
			JOIN prompt_better.turns t ON t.trajectory_id=tr.trajectory_id
			LEFT JOIN prompt_better.tasks task ON task.task_id=t.task_id
			WHERE %s AND %s
			  AND t.observed_at >= $1 AND t.observed_at < $2
			  AND t.observed_at <= $3::timestamptz
		), class_counts AS (
			SELECT project_id,trajectory_id,workload_class,
				count(DISTINCT turn_id)::numeric AS turn_count
			FROM selected_turns GROUP BY project_id,trajectory_id,workload_class
		), trajectory_counts AS (
			SELECT trajectory_id,sum(turn_count) AS turn_count
			FROM class_counts GROUP BY trajectory_id
		), usage_by_trajectory AS (
			SELECT trajectory_id,coalesce(sum(value_numeric),0) AS tokens
			FROM prompt_better.usage_observations
			WHERE metric_kind='total_tokens' AND observed_at >= $1 AND observed_at < $2
			  AND observed_at <= $3::timestamptz
			GROUP BY trajectory_id
		), project_values AS (
			SELECT class.project_id,class.workload_class,
				coalesce(sum(usage.tokens * class.turn_count /
					nullif(total.turn_count,0)),0) AS numerator,
				sum(class.turn_count) AS denominator
			FROM class_counts class
			JOIN trajectory_counts total USING (trajectory_id)
			LEFT JOIN usage_by_trajectory usage USING (trajectory_id)
			GROUP BY class.project_id,class.workload_class
		)
		SELECT project_id::text,workload_class,numerator,denominator,
			sum(numerator) OVER () AS total_numerator
		FROM project_values ORDER BY project_id`, filter, turnFilter)
	rows, err := source.repository.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, errors.New("query audit contributions")
	}
	defer rows.Close()
	result := []contracts.ScopeContribution{}
	for rows.Next() {
		var reference, workloadClass string
		var numerator, total float64
		var denominator int
		if err := rows.Scan(&reference, &workloadClass, &numerator, &denominator, &total); err != nil {
			return nil, errors.New("decode audit contributions")
		}
		var contribution *float64
		coverage := contracts.CoverageStateComplete
		attributionState := "attributed"
		if denominator < 3 {
			coverage = contracts.CoverageStatePartial
			attributionState = "privacy_suppressed"
		} else if total > 0 {
			value := numerator / total
			contribution = &value
		}
		result = append(result, contracts.ScopeContribution{
			Scope: contracts.AuditScopeProject, Reference: reference,
			WorkloadClass: workloadClass,
			Numerator:     numerator, Denominator: float64(denominator),
			Contribution: contribution, Coverage: coverage,
			AttributionState: attributionState,
		})
	}
	return result, rows.Err()
}

func (source projectAuditSource) confounders(ctx context.Context,
	request contracts.AuditProjectRequest,
) ([]contracts.ConfounderStratum, error) {
	filter, _, args, err := auditFilter(request)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		SELECT DISTINCT label.label_kind,label.label_state,label.provenance,
			coalesce(label.confidence,0)
		FROM prompt_better.sessions s
		LEFT JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
		JOIN prompt_better.confounder_labels label ON label.trajectory_id=tr.trajectory_id
		WHERE %s AND label.observed_at >= $1 AND label.observed_at < $2
		  AND label.observed_at <= $3::timestamptz
		ORDER BY 1,2,3`, filter)
	rows, err := source.repository.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, errors.New("query audit confounders")
	}
	defer rows.Close()
	result := []contracts.ConfounderStratum{}
	for rows.Next() {
		var item contracts.ConfounderStratum
		var confidence float64
		if err := rows.Scan(&item.Kind, &item.State, &item.Provenance, &confidence); err != nil {
			return nil, errors.New("decode audit confounders")
		}
		item.Confidence = confidenceLabel(confidence)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (source projectAuditSource) previousConfoundersMatched(ctx context.Context,
	request contracts.AuditProjectRequest,
) (bool, error) {
	previous, ok := previousAuditWindow(request)
	if !ok {
		return false, errors.New("invalid confounder comparison duration")
	}
	currentSignature, err := source.confounderSignature(ctx, request)
	if err != nil {
		return false, err
	}
	previousSignature, err := source.confounderSignature(ctx, previous)
	if err != nil {
		return false, err
	}
	return currentSignature != "unknown" && currentSignature == previousSignature, nil
}

func (source projectAuditSource) invocationCounts(ctx context.Context, filter, turnFilter string,
	args []any,
) (contracts.InvocationCounts, error) {
	query := fmt.Sprintf(`
		WITH selected_tools AS (
			SELECT c.external_alias_id, c.caller_alias_id, c.outcome
			FROM prompt_better.sessions s
			JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
			JOIN prompt_better.turns t ON t.trajectory_id=tr.trajectory_id
				AND t.observed_at >= $1 AND t.observed_at < $2
				AND t.observed_at <= $3::timestamptz
			JOIN prompt_better.responses r ON r.turn_id=t.turn_id
			JOIN prompt_better.tool_calls c ON c.response_id=r.response_id
			WHERE %s AND %s
		), logical AS (
			SELECT external_alias_id, max(caller_alias_id::text)::uuid AS caller_alias_id,
				bool_or(outcome IN ('success','completed')) AS has_result
			FROM selected_tools WHERE external_alias_id IS NOT NULL
			GROUP BY external_alias_id
		)
		SELECT
			count(*) FILTER (WHERE caller_alias_id IS NULL),
			count(*) FILTER (WHERE NOT EXISTS (
				SELECT 1 FROM logical child WHERE child.caller_alias_id=logical.external_alias_id)),
			count(*) FILTER (WHERE caller_alias_id IS NULL AND has_result),
			count(*) FILTER (WHERE has_result AND NOT EXISTS (
				SELECT 1 FROM logical child WHERE child.caller_alias_id=logical.external_alias_id)),
			(SELECT count(*) FROM selected_tools WHERE external_alias_id IS NULL)
		FROM logical`, filter, turnFilter)
	var result contracts.InvocationCounts
	if err := source.repository.pool.QueryRow(ctx, query, args...).Scan(
		&result.HostCalls, &result.LeafCalls, &result.HostResults,
		&result.LeafResults, &result.UnknownCalls,
	); err != nil {
		return contracts.InvocationCounts{}, errors.New("query logical invocation counts")
	}
	return result, nil
}

func (source projectAuditSource) workloadEffects(ctx context.Context,
	request contracts.AuditProjectRequest,
) ([]contracts.WorkloadDecomposition, error) {
	previous, ok := previousAuditWindow(request)
	if !ok {
		return nil, errors.New("invalid workload duration")
	}
	currentValues, err := source.workloadValues(ctx, request)
	if err != nil {
		return nil, err
	}
	previousValues, err := source.workloadValues(ctx, previous)
	if err != nil {
		return nil, err
	}
	return []contracts.WorkloadDecomposition{
		workload.Decompose("total_tokens", previousValues, currentValues),
	}, nil
}

func (source projectAuditSource) workloadValues(ctx context.Context,
	request contracts.AuditProjectRequest,
) ([]workload.ClassValue, error) {
	filter, turnFilter, args, err := auditFilter(request)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		WITH selected_turns AS (
			SELECT t.turn_id, tr.trajectory_id, coalesce(task.task_kind,'unknown') AS workload_class
			FROM prompt_better.sessions s
			JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
			JOIN prompt_better.turns t ON t.trajectory_id=tr.trajectory_id
			LEFT JOIN prompt_better.tasks task ON task.task_id=t.task_id
			WHERE %s AND %s AND t.observed_at >= $1 AND t.observed_at < $2
			  AND t.observed_at <= $3::timestamptz
		), usage_by_trajectory AS (
			SELECT trajectory_id, coalesce(sum(value_numeric),0) AS tokens
			FROM prompt_better.usage_observations
			WHERE metric_kind='total_tokens' AND observed_at >= $1 AND observed_at < $2
			  AND observed_at <= $3::timestamptz
			GROUP BY trajectory_id
		), class_counts AS (
			SELECT trajectory_id,workload_class,count(DISTINCT turn_id)::numeric AS turn_count
			FROM selected_turns GROUP BY trajectory_id,workload_class
		), trajectory_counts AS (
			SELECT trajectory_id,sum(turn_count) AS turn_count
			FROM class_counts GROUP BY trajectory_id
		)
		SELECT class_counts.workload_class, sum(class_counts.turn_count),
			coalesce(sum(usage.tokens * class_counts.turn_count / nullif(total.turn_count,0)),0)
		FROM class_counts
		JOIN trajectory_counts total USING (trajectory_id)
		LEFT JOIN usage_by_trajectory usage USING (trajectory_id)
		GROUP BY class_counts.workload_class ORDER BY class_counts.workload_class`, filter, turnFilter)
	rows, err := source.repository.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, errors.New("query workload strata")
	}
	defer rows.Close()
	result := []workload.ClassValue{}
	for rows.Next() {
		var value workload.ClassValue
		if err := rows.Scan(&value.Class, &value.Count, &value.Numerator); err != nil {
			return nil, errors.New("decode workload stratum")
		}
		value.Denominator = value.Count
		result = append(result, value)
	}
	return result, rows.Err()
}

func (source projectAuditSource) revisionState(ctx context.Context,
	request contracts.AuditProjectRequest,
) (int, bool, error) {
	windowID := collectionBatchUUID(fmt.Sprintf("audit-window:%s:%s:%s:%s",
		request.Scope, request.Reference, request.StartsAt.UTC(), request.EndsAt.UTC()))
	var revision int
	var priorWatermark *time.Time
	err := source.repository.pool.QueryRow(ctx, `SELECT count(*), max(source_watermark_at)
		FROM prompt_better.audit_revisions WHERE audit_window_id=$1`, windowID).
		Scan(&revision, &priorWatermark)
	if err != nil {
		return 0, false, errors.New("read audit revision state")
	}
	if priorWatermark != nil && request.AsOf.Equal(*priorWatermark) {
		return revision, revision > 1, nil
	}
	return revision + 1, priorWatermark != nil && request.AsOf.After(*priorWatermark), nil
}

func (source projectAuditSource) recommendationContext(ctx context.Context,
	request contracts.AuditProjectRequest,
) (recommendation.Context, error) {
	result := recommendation.Context{
		AsOf: request.AsOf, ExistingRuleCoverage: map[string]bool{},
		Feedback:                 map[string]recommendation.Feedback{},
		MaterialEvidenceRevision: "",
	}
	if request.Scope != contracts.AuditScopeProject {
		return result, nil
	}
	rows, err := source.repository.pool.Query(ctx, `
		SELECT finding.finding_kind,
			coalesce((SELECT event.event_kind
				FROM prompt_better.recommendation_events event
				WHERE event.recommendation_id=recommendation.recommendation_id
				  AND event.observed_at <= $2
				ORDER BY event.observed_at DESC,event.recommendation_event_id DESC LIMIT 1),
				CASE WHEN recommendation.updated_at <= $2
					THEN recommendation.lifecycle_state ELSE 'proposed' END),
			CASE WHEN recommendation.updated_at <= $2
				THEN coalesce(cooldown_until,'0001-01-01 00:00:00+00'::timestamptz)
				ELSE '0001-01-01 00:00:00+00'::timestamptz END,
			coalesce(recommendation.evidence_revision,'')
		FROM prompt_better.recommendations recommendation
		JOIN prompt_better.findings finding ON finding.finding_id=recommendation.finding_id
		WHERE recommendation.project_id=$1 AND recommendation.created_at <= $2
		  AND (recommendation.deleted_at IS NULL OR recommendation.deleted_at > $2)
		GROUP BY recommendation.recommendation_id,finding.finding_kind,lifecycle_state,
		 cooldown_until,recommendation.updated_at,recommendation.evidence_revision
		ORDER BY recommendation.updated_at DESC`, request.Reference, request.AsOf)
	if err != nil {
		return recommendation.Context{}, errors.New("query recommendation feedback")
	}
	defer rows.Close()
	for rows.Next() {
		var code, state, evidenceRevision string
		var cooldown time.Time
		if err := rows.Scan(&code, &state, &cooldown, &evidenceRevision); err != nil {
			return recommendation.Context{}, errors.New("decode recommendation feedback")
		}
		if _, exists := result.Feedback[code]; !exists {
			result.Feedback[code] = recommendation.Feedback{
				State: state, CooldownUntil: cooldown, EvidenceRevision: evidenceRevision,
			}
			result.ExistingRuleCoverage[code] = state == "verified"
		}
	}
	return result, rows.Err()
}

func previousAuditWindow(request contracts.AuditProjectRequest) (contracts.AuditProjectRequest, bool) {
	duration := request.EndsAt.Sub(request.StartsAt)
	if duration <= 0 {
		return contracts.AuditProjectRequest{}, false
	}
	previous := request
	previous.EndsAt = request.StartsAt
	previous.StartsAt = previous.EndsAt.Add(-duration)
	previous.AsOf = previous.EndsAt
	return previous, true
}

func evidenceRevision(refs []string) string {
	values := append([]string{}, refs...)
	sort.Strings(values)
	encoded, _ := json.Marshal(values)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func currentGuardrails(input metrics.Input) []contracts.GuardrailResult {
	results, err := metrics.Compute(input)
	if err != nil {
		return []contracts.GuardrailResult{}
	}
	byName := metricResultsByName(results)
	guardrails := []contracts.GuardrailResult{}
	for _, name := range []string{"tool_error_rate", "validation_presence", "privacy_redaction_coverage"} {
		metric := byName[name]
		guardrails = append(guardrails, contracts.GuardrailResult{
			Name: name, State: guardrailState(name, metric), Coverage: metric.Coverage,
			EvidenceRefs: append([]string{}, metric.EvidenceRefs...),
		})
	}
	return guardrails
}

func confidenceLabel(value float64) string {
	switch {
	case value >= 0.80:
		return "high"
	case value >= 0.50:
		return "medium"
	case value > 0:
		return "low"
	default:
		return "unknown"
	}
}

func (source projectAuditSource) comparisons(ctx context.Context,
	request contracts.AuditProjectRequest, currentInput metrics.Input, privacySuppressed bool,
) (map[string]baseline.Comparison, error) {
	duration := request.EndsAt.Sub(request.StartsAt)
	if duration <= 0 {
		return nil, errors.New("invalid comparison duration")
	}
	currentSignature, err := source.dimensionSignature(ctx, request)
	if err != nil {
		return nil, err
	}
	windows := make([]map[string]contracts.MetricResult, 0, 29)
	compatible := make([]bool, 0, 29)
	for offset := 1; offset <= 29; offset++ {
		prior := request
		prior.EndsAt = request.StartsAt.Add(-time.Duration(offset-1) * duration)
		prior.StartsAt = prior.EndsAt.Add(-duration)
		prior.AsOf = prior.EndsAt
		filter, turnFilter, args, err := auditFilter(prior)
		if err != nil {
			return nil, err
		}
		input, err := source.metrics(ctx, filter, turnFilter, args)
		if err != nil {
			return nil, err
		}
		results, err := metrics.Compute(input)
		if err != nil {
			return nil, err
		}
		windows = append(windows, metricResultsByName(results))
		signature, signatureErr := source.dimensionSignature(ctx, prior)
		compatible = append(compatible, signatureErr == nil && signature == currentSignature)
	}
	comparisons := map[string]baseline.Comparison{}
	currentResults, err := metrics.Compute(currentInput)
	if err != nil {
		return nil, err
	}
	currentByName := metricResultsByName(currentResults)
	for _, definition := range metrics.Definitions() {
		previousResult := windows[0][definition.Name]
		previous := baseline.Window{
			Value: previousResult.NativeValue, Coverage: previousResult.Coverage,
			MetricVersion: previousResult.Version, Comparable: compatible[0],
			GuardrailsPass: guardrailsPass(windows[0]),
		}
		rolling := make([]baseline.Window, 0, 7)
		for index := 1; index < 8 && index < len(windows); index++ {
			result := windows[index][definition.Name]
			rolling = append(rolling, baseline.Window{
				Value: result.NativeValue, Coverage: result.Coverage,
				MetricVersion: result.Version, Comparable: compatible[index],
			})
		}
		seasonal := make([]baseline.Window, 0, 4)
		for _, index := range []int{6, 13, 20, 27} {
			if index >= len(windows) {
				continue
			}
			result := windows[index][definition.Name]
			seasonal = append(seasonal, baseline.Window{
				Value: result.NativeValue, Coverage: result.Coverage,
				MetricVersion: result.Version, Comparable: compatible[index],
			})
		}
		persistenceCount, hysteresisActive := persistenceState(
			currentByName[definition.Name], previousResult, windows[1][definition.Name],
			definition)
		comparisons[definition.Name] = baseline.Comparison{
			Previous: previous, Rolling: rolling, Seasonal: seasonal,
			ConfoundersMatched: compatible[0] && compatibleCount(compatible[1:]) >= 3,
			PersistenceCount:   persistenceCount,
			HysteresisActive:   hysteresisActive,
			PrivacySuppressed:  privacySuppressed,
		}
	}
	return comparisons, nil
}

func persistenceState(current, previous, older contracts.MetricResult,
	definition metrics.Definition,
) (int, bool) {
	if current.NativeValue == nil || previous.NativeValue == nil || older.NativeValue == nil ||
		current.Version != previous.Version || previous.Version != older.Version {
		return 0, false
	}
	recent := relativeDirection(*current.NativeValue, *previous.NativeValue,
		definition.PracticalChange)
	prior := relativeDirection(*previous.NativeValue, *older.NativeValue,
		definition.PracticalChange)
	if recent == 0 {
		return 0, prior != 0
	}
	if recent == prior {
		return 2, false
	}
	return 1, prior != 0 && prior != recent
}

func relativeDirection(current, prior, threshold float64) int {
	if prior == 0 {
		if current == 0 {
			return 0
		}
		if current > 0 {
			return 1
		}
		return -1
	}
	change := (current - prior) / prior
	if change > threshold {
		return 1
	}
	if change < -threshold {
		return -1
	}
	return 0
}

func (source projectAuditSource) dimensionSignature(ctx context.Context,
	request contracts.AuditProjectRequest,
) (string, error) {
	filter, _, args, err := auditFilter(request)
	if err != nil {
		return "", err
	}
	query := fmt.Sprintf(`
		SELECT DISTINCT pv.project_version_id::text, sv.source_version_id::text
		FROM prompt_better.sessions s
		JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
		JOIN prompt_better.turns t ON t.trajectory_id=tr.trajectory_id
		JOIN prompt_better.project_versions pv ON pv.project_id=s.project_id
			AND pv.valid_from <= t.observed_at
			AND (pv.valid_to > t.observed_at OR pv.valid_to IS NULL)
			AND pv.valid_from <= $3
		JOIN prompt_better.source_versions sv ON sv.source_id=s.source_id
			AND sv.valid_from <= t.observed_at
			AND (sv.valid_to > t.observed_at OR sv.valid_to IS NULL)
			AND sv.valid_from <= $3
		WHERE t.observed_at >= $1 AND t.observed_at < $2
		  AND t.observed_at <= $3 AND %s ORDER BY 1, 2`, filter)
	rows, err := source.repository.pool.Query(ctx, query, args...)
	if err != nil {
		return "", errors.New("query audit dimension versions")
	}
	defer rows.Close()
	parts := []string{}
	for rows.Next() {
		var projectVersion, sourceVersion string
		if err := rows.Scan(&projectVersion, &sourceVersion); err != nil {
			return "", errors.New("decode audit dimension versions")
		}
		parts = append(parts, projectVersion+":"+sourceVersion)
	}
	if err := rows.Err(); err != nil {
		return "", errors.New("iterate audit dimension versions")
	}
	if len(parts) == 0 {
		return "", contracts.NewError(contracts.ErrorCodeCoverageIncomplete,
			"effective project/source versions unavailable", "as_of", false)
	}
	confounderSignature, err := source.confounderSignature(ctx, request)
	if err != nil {
		return "", err
	}
	parts = append(parts, "confounders:"+confounderSignature)
	sort.Strings(parts)
	return strings.Join(parts, ","), nil
}

func (source projectAuditSource) confounderSignature(ctx context.Context,
	request contracts.AuditProjectRequest,
) (string, error) {
	filter, _, args, err := auditFilter(request)
	if err != nil {
		return "", err
	}
	query := fmt.Sprintf(`
		SELECT DISTINCT label.label_kind,label.label_state,label.provenance
		FROM prompt_better.sessions s
		LEFT JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
		LEFT JOIN prompt_better.confounder_labels label ON label.trajectory_id=tr.trajectory_id
			AND label.observed_at >= $1 AND label.observed_at < $2
			AND label.observed_at <= $3::timestamptz
		WHERE %s AND label.label_kind IS NOT NULL ORDER BY 1,2,3`, filter)
	rows, err := source.repository.pool.Query(ctx, query, args...)
	if err != nil {
		return "", errors.New("query audit confounder signature")
	}
	defer rows.Close()
	parts := []string{}
	for rows.Next() {
		var kind, state, provenance string
		if err := rows.Scan(&kind, &state, &provenance); err != nil {
			return "", errors.New("decode audit confounder signature")
		}
		parts = append(parts, kind+"="+state+"@"+provenance)
	}
	if err := rows.Err(); err != nil {
		return "", errors.New("iterate audit confounder signature")
	}
	if len(parts) == 0 {
		return "unknown", nil
	}
	return strings.Join(parts, ","), nil
}

func guardrailsPass(results map[string]contracts.MetricResult) bool {
	for _, name := range []string{"tool_error_rate", "validation_presence", "privacy_redaction_coverage"} {
		if guardrailState(name, results[name]) != "pass" {
			return false
		}
	}
	return true
}

func guardrailState(name string, result contracts.MetricResult) string {
	if result.NativeValue == nil || result.Coverage != contracts.CoverageStateComplete {
		return "unknown"
	}
	if name == "tool_error_rate" {
		if *result.NativeValue > 0.05 {
			return "fail"
		}
		return "pass"
	}
	if *result.NativeValue < 0.95 {
		return "fail"
	}
	return "pass"
}

func metricResultsByName(results []contracts.MetricResult) map[string]contracts.MetricResult {
	byName := make(map[string]contracts.MetricResult, len(results))
	for _, result := range results {
		byName[result.Name] = result
	}
	return byName
}

func compatibleCount(values []bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func auditFilter(request contracts.AuditProjectRequest) (string, string, []any, error) {
	base := []any{request.StartsAt, request.EndsAt, request.AsOf, request.ConfiguredSources}
	sourceFilter := `s.source_id IN (
		SELECT source_id FROM prompt_better.sources WHERE source_kind=ANY($4::text[]))`
	switch request.Scope {
	case contracts.AuditScopePortfolio:
		if request.Reference != "all" {
			return "", "", nil, contracts.NewError(contracts.ErrorCodeInvalidSchema, "portfolio reference must be all", "reference", false)
		}
		return sourceFilter, "TRUE", base, nil
	case contracts.AuditScopeProject:
		return sourceFilter + " AND s.project_id = $5::uuid", "TRUE", append(base, request.Reference), nil
	case contracts.AuditScopeTask:
		return `EXISTS (
			SELECT 1 FROM prompt_better.trajectories task_trajectory
			JOIN prompt_better.turns task_turn
			  ON task_turn.trajectory_id=task_trajectory.trajectory_id
			WHERE task_trajectory.session_id=s.session_id
			  AND task_turn.task_id=$5::uuid) AND ` + sourceFilter,
			"t.task_id = $5::uuid", append(base, request.Reference), nil
	case contracts.AuditScopeTrajectory:
		return sourceFilter + " AND tr.trajectory_id = $5::uuid", "TRUE", append(base, request.Reference), nil
	default:
		return "", "", nil, contracts.NewError(contracts.ErrorCodeUnsupportedCapability, "audit scope unavailable", "scope", false)
	}
}

func (source projectAuditSource) metrics(
	ctx context.Context,
	filter string,
	turnFilter string,
	args []any,
) (metrics.Input, error) {
	sessionLifecycle := "TRUE"
	if turnFilter != "TRUE" || strings.Contains(filter, "tr.trajectory_id =") {
		sessionLifecycle = "FALSE"
	}
	query := fmt.Sprintf(`
		WITH selected AS (
			SELECT DISTINCT s.session_id, tr.trajectory_id
			FROM prompt_better.sessions s
			LEFT JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
			WHERE %s
		), selected_sessions AS (
			SELECT DISTINCT session_id FROM selected
		), selected_turns AS (
			SELECT t.turn_id
			FROM prompt_better.turns t
			JOIN selected selected_scope ON selected_scope.trajectory_id=t.trajectory_id
			WHERE t.observed_at >= $1 AND t.observed_at < $2
			  AND t.observed_at <= $3::timestamptz AND %s
		), selected_responses AS (
			SELECT r.response_id, r.started_at, r.completed_at
			FROM prompt_better.responses r
			JOIN selected_turns t ON t.turn_id=r.turn_id
			WHERE r.started_at <= $3::timestamptz
		), selected_tool_events AS (
			SELECT c.*
			FROM prompt_better.tool_calls c
			JOIN selected_responses r ON r.response_id=c.response_id
			WHERE c.started_at <= $3::timestamptz
		), selected_tools AS (
			SELECT coalesce(external_alias_id::text,tool_call_id::text) AS logical_tool_id,
				max(tool_kind) AS tool_kind,
				CASE
					WHEN bool_or(outcome IN ('error','failed')) THEN 'error'
					WHEN bool_or(outcome='repeated_unchanged') THEN 'repeated_unchanged'
					WHEN bool_or(outcome IN ('success','completed')) THEN 'success'
					ELSE 'unknown'
				END AS outcome,
				max(state_epoch_id::text)::uuid AS state_epoch_id
			FROM selected_tool_events
			GROUP BY coalesce(external_alias_id::text,tool_call_id::text)
		), selected_item_events AS (
			SELECT i.*
			FROM prompt_better.items i
			JOIN selected_responses r ON r.response_id=i.response_id
			WHERE i.observed_at <= $3::timestamptz
		), selected_items AS (
			SELECT coalesce(external_alias_id::text,item_id::text) AS logical_item_id,
				max(item_kind) AS item_kind, max(content_length) AS content_length,
				max(image_detail) AS image_detail, max(assistant_phase) AS assistant_phase
			FROM selected_item_events
			GROUP BY coalesce(external_alias_id::text,item_id::text)
		), selected_epochs AS (
			SELECT epoch.*
			FROM prompt_better.state_epochs epoch
			JOIN selected selected_scope ON selected_scope.trajectory_id=epoch.trajectory_id
			WHERE epoch.started_at >= $1 AND epoch.started_at < $2
			  AND epoch.started_at <= $3::timestamptz
		), selected_phases AS (
			SELECT phase.*
			FROM prompt_better.phases phase
			JOIN selected selected_scope ON selected_scope.trajectory_id=phase.trajectory_id
			WHERE phase.started_at >= $1 AND phase.started_at < $2
			  AND phase.started_at <= $3::timestamptz
		), response_ordered AS (
			SELECT *, max(completed_at) OVER (
				ORDER BY started_at,completed_at
				ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS prior_end
			FROM selected_responses WHERE completed_at IS NOT NULL
		), response_groups AS (
			SELECT *, sum(CASE WHEN prior_end IS NULL OR started_at > prior_end THEN 1 ELSE 0 END)
				OVER (ORDER BY started_at,completed_at) AS interval_group
			FROM response_ordered
		), response_unions AS (
			SELECT min(started_at) AS started_at,max(completed_at) AS ended_at
			FROM response_groups GROUP BY interval_group
		), phase_ordered AS (
			SELECT *, max(ended_at) OVER (
				ORDER BY started_at,ended_at
				ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS prior_end
			FROM selected_phases WHERE ended_at IS NOT NULL
		), phase_groups AS (
			SELECT *, sum(CASE WHEN prior_end IS NULL OR started_at > prior_end THEN 1 ELSE 0 END)
				OVER (ORDER BY started_at,ended_at) AS interval_group
			FROM phase_ordered
		), phase_unions AS (
			SELECT min(started_at) AS started_at,max(ended_at) AS ended_at
			FROM phase_groups GROUP BY interval_group
		)
		SELECT
			(SELECT count(*) FROM selected_turns),
			(SELECT count(*) FROM prompt_better.turns t
			 JOIN selected_turns selected_turn ON selected_turn.turn_id=t.turn_id
			 WHERE t.task_id IS NOT NULL),
			(SELECT count(*) FROM prompt_better.boundaries boundary
			 JOIN selected_sessions selected_scope ON selected_scope.session_id=boundary.session_id
			 WHERE boundary.observed_at >= $1 AND boundary.observed_at < $2
			   AND boundary.observed_at <= $3::timestamptz AND %s),
			(SELECT count(*) FROM prompt_better.boundaries boundary
			 JOIN selected_sessions selected_scope ON selected_scope.session_id=boundary.session_id
			 WHERE boundary.observed_at >= $1 AND boundary.observed_at < $2
			   AND boundary.observed_at <= $3::timestamptz
			   AND boundary.outcome IN ('denied','violated') AND %s),
			(SELECT count(*) FROM prompt_better.checkpoints checkpoint
			 JOIN selected_sessions selected_scope ON selected_scope.session_id=checkpoint.session_id
			 WHERE checkpoint.created_at >= $1 AND checkpoint.created_at < $2
			   AND checkpoint.created_at <= $3::timestamptz AND %s),
			(SELECT count(*) FROM prompt_better.checkpoints checkpoint
			 JOIN selected_sessions selected_scope ON selected_scope.session_id=checkpoint.session_id
			 WHERE checkpoint.created_at >= $1 AND checkpoint.created_at < $2
			   AND checkpoint.created_at <= $3::timestamptz
			   AND checkpoint.completed_count > 0 AND checkpoint.next_action_count > 0
			   AND checkpoint.gate_count > 0 AND %s),
			(SELECT coalesce(sum(u.value_numeric), 0)
			 FROM prompt_better.usage_observations u
			 JOIN selected selected_scope ON selected_scope.trajectory_id=u.trajectory_id
			 WHERE u.metric_kind='total_tokens' AND u.observed_at >= $1 AND u.observed_at < $2
			   AND u.observed_at <= $3::timestamptz),
			(SELECT coalesce(sum(u.value_numeric), 0)
			 FROM prompt_better.usage_observations u
			 JOIN selected selected_scope ON selected_scope.trajectory_id=u.trajectory_id
			 WHERE u.metric_kind='non_cached_input_tokens' AND u.observed_at >= $1 AND u.observed_at < $2
			   AND u.observed_at <= $3::timestamptz),
			(SELECT count(*) FROM selected_tools),
			(SELECT count(*) FROM selected_tools WHERE tool_kind='passive_wait'),
			(SELECT count(*) FROM selected_items WHERE item_kind='tool_result'),
			(SELECT count(*) FROM selected_items WHERE item_kind='tool_result'
			 AND content_length > 20480
			 AND coalesce(image_detail,'none') IN ('none','text')),
			(SELECT count(*) FROM selected_tools WHERE outcome='repeated_unchanged'),
			(SELECT count(*) FROM selected_tools WHERE outcome IN ('error','failed')),
			(SELECT count(*) FROM selected_epochs WHERE mutation_state='mutated'),
			(SELECT count(*) FROM selected_epochs epoch WHERE mutation_state='mutated'
			 AND EXISTS (
				SELECT 1 FROM selected_tools tool
				WHERE tool.state_epoch_id=epoch.state_epoch_id
				  AND tool.tool_kind='validation' AND tool.outcome='success')),
			(SELECT count(*) FROM selected_items WHERE assistant_phase='commentary'),
			(SELECT coalesce(sum(extract(epoch FROM (ended_at-started_at))), 0)
			 FROM response_unions),
			(SELECT coalesce(sum(extract(epoch FROM (ended_at-started_at))), 0)
			 FROM phase_unions),
			(SELECT count(*) FROM selected_phases WHERE ended_at IS NOT NULL),
			(SELECT count(DISTINCT e.evidence_artifact_id)
			 FROM prompt_better.evidence_artifacts e
			 JOIN selected_sessions selected_scope ON selected_scope.session_id=e.session_id
			 WHERE e.observed_at >= $1 AND e.observed_at < $2
			   AND e.observed_at <= $3::timestamptz AND e.deleted_at IS NULL),
			(SELECT count(DISTINCT e.evidence_artifact_id)
			 FROM prompt_better.evidence_artifacts e
			 JOIN selected_sessions selected_scope ON selected_scope.session_id=e.session_id
			 WHERE e.observed_at >= $1 AND e.observed_at < $2
			   AND e.observed_at <= $3::timestamptz
			   AND e.deleted_at IS NULL AND e.coverage_state='complete'),
			(SELECT count(DISTINCT e.evidence_artifact_id)
			 FROM prompt_better.evidence_artifacts e
			 JOIN selected_sessions selected_scope ON selected_scope.session_id=e.session_id
			 WHERE e.observed_at >= $1 AND e.observed_at < $2
			   AND e.observed_at <= $3::timestamptz
			   AND e.deleted_at IS NULL
			   AND e.redaction_state IN ('applied','not_needed'))`,
		filter, turnFilter, sessionLifecycle, sessionLifecycle,
		sessionLifecycle, sessionLifecycle)
	var input metrics.Input
	var evidenceCount, completeEvidence, completePhases int
	if err := source.repository.pool.QueryRow(ctx, query, args...).Scan(
		&input.CompletedTurns,
		&input.AttributedTurns,
		&input.BoundaryDecisions,
		&input.BoundaryViolations,
		&input.Checkpoints,
		&input.CompleteCheckpoints,
		&input.TotalTokens,
		&input.NonCachedInputTokens,
		&input.ToolCalls,
		&input.PassiveWaitCalls,
		&input.ToolResults,
		&input.OversizedResults,
		&input.RepeatedCalls,
		&input.ToolErrors,
		&input.ObservableMutations,
		&input.ValidatedMutations,
		&input.CommentaryMessages,
		&input.WallClockSeconds,
		&input.ActiveSeconds,
		&completePhases,
		&evidenceCount,
		&completeEvidence,
		&input.RedactedEvidence,
	); err != nil {
		return metrics.Input{}, errors.New("query project audit metrics")
	}
	input.AcceptedEvidence = evidenceCount
	input.ActiveRuntimeKnown = completePhases > 0
	input.Coverage = contracts.CoverageStateComplete
	if evidenceCount == 0 {
		input.Coverage = contracts.CoverageStateMissing
	} else if completeEvidence != evidenceCount {
		input.Coverage = contracts.CoverageStatePartial
	}
	evidenceRefs, err := source.evidenceRefs(ctx, filter, args)
	if err != nil {
		return metrics.Input{}, err
	}
	input.EvidenceRefs = evidenceRefs
	return input, nil
}

func (source projectAuditSource) overhead(
	ctx context.Context,
	filter string,
	args []any,
) (metrics.Input, error) {
	query := fmt.Sprintf(`
		SELECT coalesce(sum(go.native_value) FILTER (WHERE go.native_unit='tokens'), 0),
			count(DISTINCT go.governance_overhead_id)
		FROM prompt_better.sessions s
		LEFT JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
		LEFT JOIN prompt_better.governance_overhead go ON go.trajectory_id=tr.trajectory_id
			AND go.observed_at >= $1 AND go.observed_at < $2
			AND go.observed_at <= $3::timestamptz
		WHERE %s`, filter)
	var input metrics.Input
	if err := source.repository.pool.QueryRow(ctx, query, args...).Scan(
		&input.TotalTokens,
		&input.CompletedTurns,
	); err != nil {
		return metrics.Input{}, errors.New("query governance overhead")
	}
	input.Coverage = contracts.CoverageStateComplete
	return input, nil
}

func (source projectAuditSource) evidenceRefs(
	ctx context.Context,
	filter string,
	args []any,
) ([]string, error) {
	query := fmt.Sprintf(`
		SELECT DISTINCT e.evidence_artifact_id::text
		FROM prompt_better.sessions s
		LEFT JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
		JOIN prompt_better.evidence_artifacts e ON e.session_id=s.session_id
			AND e.observed_at >= $1 AND e.observed_at < $2
			AND e.observed_at <= $3::timestamptz AND e.deleted_at IS NULL
		WHERE %s ORDER BY 1 LIMIT 100`, filter)
	rows, err := source.repository.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, errors.New("query project audit evidence")
	}
	defer rows.Close()
	refs := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, errors.New("decode project audit evidence")
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (source projectAuditSource) toolRefs(
	ctx context.Context,
	filter string,
	turnFilter string,
	args []any,
	kind string,
) ([]string, error) {
	valueParameter := len(args) + 1
	predicate := fmt.Sprintf("c.tool_kind=$%d", valueParameter)
	if kind == "repeated_unchanged" {
		predicate = fmt.Sprintf("c.outcome=$%d", valueParameter)
	}
	query := fmt.Sprintf(`
		SELECT DISTINCT c.tool_call_id::text
		FROM prompt_better.sessions s
		JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
		JOIN prompt_better.turns t ON t.trajectory_id=tr.trajectory_id
			AND t.observed_at >= $1 AND t.observed_at < $2
			AND t.observed_at <= $3::timestamptz
		JOIN prompt_better.responses r ON r.turn_id=t.turn_id
		JOIN prompt_better.tool_calls c ON c.response_id=r.response_id
			AND c.started_at <= $3::timestamptz
		WHERE %s AND %s AND %s ORDER BY 1 LIMIT 100`,
		filter, turnFilter, predicate)
	toolArgs := append(append([]any{}, args...), kind)
	rows, err := source.repository.pool.Query(ctx, query, toolArgs...)
	if err != nil {
		return nil, errors.New("query audit tool evidence")
	}
	defer rows.Close()
	refs := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, errors.New("decode audit tool evidence")
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
