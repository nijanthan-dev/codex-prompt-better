package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

// AuditSession reads bounded evidence with explicit current or as-of dimensions.
func (r *Repository) AuditSession(ctx context.Context, sessionID string, configuredSources []string, asOf *time.Time) (contracts.AuditSessionResult, []contracts.ProvenanceLabel, error) {
	var coverage, sourceID, sourceKind string
	var projectID *string
	if err := r.pool.QueryRow(ctx, `SELECT se.coverage_state, se.source_id::text,
		s.source_kind, se.project_id::text
		FROM prompt_better.sessions se
		JOIN prompt_better.sources s ON s.source_id=se.source_id
		WHERE se.session_id=$1`, sessionID).Scan(&coverage, &sourceID, &sourceKind, &projectID); errors.Is(err, pgx.ErrNoRows) {
		return contracts.AuditSessionResult{}, nil, contracts.NewError(contracts.ErrorCodeNotFound, "audit session unavailable", "reference", false)
	} else if err != nil {
		return contracts.AuditSessionResult{}, nil, contracts.NewError(contracts.ErrorCodeInternal, "audit session read failed", "reference", true)
	}
	allowed := make(map[string]bool, len(configuredSources))
	for _, source := range configuredSources {
		allowed[source] = true
	}
	if !allowed[sourceKind] {
		return contracts.AuditSessionResult{}, nil, contracts.NewError(contracts.ErrorCodePermissionDenied, "session source is not configured for audit", "configured_sources", false)
	}
	dimensionIncomplete := false
	if err := r.validateAuditDimensions(ctx, sourceID, projectID, asOf); err != nil {
		coverage = string(contracts.CoverageStatePartial)
		dimensionIncomplete = true
	}

	rows, err := r.pool.Query(ctx, `SELECT e.evidence_artifact_id::text, e.source_id::text, s.source_kind,
		e.coverage_state, e.redaction_state, e.provenance
		FROM prompt_better.evidence_artifacts e
		JOIN prompt_better.sources s ON s.source_id=e.source_id
		WHERE e.session_id=$1 AND e.deleted_at IS NULL AND s.source_kind = ANY($2::text[])
		ORDER BY e.observed_at, e.evidence_artifact_id LIMIT 101`, sessionID, configuredSources)
	if err != nil {
		return contracts.AuditSessionResult{}, nil, contracts.NewError(contracts.ErrorCodeInternal, "audit evidence read failed", "reference", true)
	}
	defer rows.Close()
	refs := make([]string, 0, 100)
	provenance := []contracts.ProvenanceLabel{}
	seenProvenance := map[contracts.ProvenanceLabel]bool{}
	redaction := false
	validatedSources := map[string]bool{sourceID: true}
	omitted := 0
	for rows.Next() {
		var reference, evidenceSourceID, kind, evidenceCoverage, redactionState, label string
		if err := rows.Scan(&reference, &evidenceSourceID, &kind, &evidenceCoverage, &redactionState, &label); err != nil {
			return contracts.AuditSessionResult{}, nil, contracts.NewError(contracts.ErrorCodeInternal, "audit evidence decode failed", "reference", true)
		}
		if !validatedSources[evidenceSourceID] {
			validatedSources[evidenceSourceID] = true
			if err := r.validateAuditDimensions(ctx, evidenceSourceID, nil, asOf); err != nil {
				coverage = string(contracts.CoverageStatePartial)
				dimensionIncomplete = true
			}
		}
		if len(refs) == 100 {
			omitted++
			continue
		}
		refs = append(refs, reference)
		if evidenceCoverage != "complete" {
			coverage = string(contracts.CoverageStatePartial)
		}
		redaction = redaction || redactionState == "applied"
		parsed := provenanceLabel(label)
		if !seenProvenance[parsed] && len(provenance) < 20 {
			seenProvenance[parsed] = true
			provenance = append(provenance, parsed)
		}
	}
	if err := rows.Err(); err != nil {
		return contracts.AuditSessionResult{}, nil, contracts.NewError(contracts.ErrorCodeInternal, "audit evidence iteration failed", "reference", true)
	}
	var filtered int
	if err := r.pool.QueryRow(ctx, `SELECT count(*)
		FROM prompt_better.evidence_artifacts e
		JOIN prompt_better.sources s ON s.source_id=e.source_id
		WHERE e.session_id=$1 AND e.deleted_at IS NULL AND NOT (s.source_kind = ANY($2::text[]))`, sessionID, configuredSources).Scan(&filtered); err != nil {
		return contracts.AuditSessionResult{}, nil, contracts.NewError(contracts.ErrorCodeInternal, "audit evidence count failed", "reference", true)
	}
	if len(refs) == 0 {
		coverage = string(contracts.CoverageStateMissing)
	}
	findings := []string{fmt.Sprintf("evidence_count:%d", len(refs))}
	if asOf == nil {
		findings = append(findings, "dimension_semantics:current")
	} else {
		findings = append(findings, "dimension_semantics:as_of")
	}
	if filtered > 0 {
		findings = append(findings, fmt.Sprintf("source_filtered:%d", filtered))
	}
	if dimensionIncomplete {
		findings = append(findings, "dimension_coverage:incomplete")
	}
	if omitted > 0 {
		findings = append(findings, fmt.Sprintf("evidence_omitted:%d", omitted))
	}
	return contracts.AuditSessionResult{
		SchemaVersion: contracts.SchemaVersion, Kind: "result", Coverage: contracts.CoverageState(coverage),
		EvidenceRefs: refs, Findings: findings, RedactionApplied: redaction,
	}, provenance, nil
}

func (r *Repository) validateAuditDimensions(ctx context.Context, sourceID string, projectID *string, asOf *time.Time) error {
	var err error
	if asOf == nil {
		_, err = r.CurrentSource(ctx, sourceID)
	} else {
		_, err = r.SourceAsOf(ctx, sourceID, *asOf)
	}
	if err != nil {
		return err
	}
	if projectID == nil {
		return nil
	}
	if asOf == nil {
		_, err = r.CurrentProject(ctx, *projectID)
	} else {
		_, err = r.ProjectAsOf(ctx, *projectID, *asOf)
	}
	return err
}

func provenanceLabel(value string) contracts.ProvenanceLabel {
	switch contracts.ProvenanceLabel(value) {
	case contracts.ProvenanceOfficialCurrent, contracts.ProvenanceStaffClarification,
		contracts.ProvenancePractitionerHypothesis, contracts.ProvenanceRuntimeObserved,
		contracts.ProvenanceDerived:
		return contracts.ProvenanceLabel(value)
	default:
		return contracts.ProvenanceUnknown
	}
}
