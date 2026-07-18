package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// DefaultRetention is the required local normalized-evidence retention window.
	DefaultRetention  = 30 * 24 * time.Hour
	maxRetentionBatch = 1000
)

// RetentionPolicy controls one project/classification window.
type RetentionPolicy struct {
	ID             string
	ProjectID      string
	Version        string
	Classification string
	RetainFor      time.Duration
	LegalHold      bool
	CreatedAt      time.Time
}

// PutRetentionPolicy stores the current policy for a project classification.
func (r *Repository) PutRetentionPolicy(ctx context.Context, policy RetentionPolicy) error {
	if policy.RetainFor < 0 {
		return errors.New("retention duration must not be negative")
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO prompt_better.retention_policies
        (retention_policy_id, project_id, policy_version, classification,
         retain_for_seconds, legal_hold, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7)
        ON CONFLICT (project_id, classification) WHERE retired_at IS NULL
        DO UPDATE SET policy_version=EXCLUDED.policy_version,
                      retain_for_seconds=EXCLUDED.retain_for_seconds,
                      legal_hold=EXCLUDED.legal_hold`, policy.ID, policy.ProjectID,
		policy.Version, policy.Classification, int64(policy.RetainFor/time.Second),
		policy.LegalHold, policy.CreatedAt)
	if err != nil {
		return errors.New("persist retention policy")
	}
	return nil
}

// ArchiveRecord is the normalized metadata allowed in encrypted archives.
type ArchiveRecord struct {
	EvidenceID      string     `json:"evidence_id"`
	SourceID        string     `json:"source_id"`
	ProjectID       string     `json:"project_id"`
	SessionID       *string    `json:"session_id,omitempty"`
	ExternalAliasID *string    `json:"external_alias_id,omitempty"`
	SchemaVersion   string     `json:"schema_version"`
	ContentHash     []byte     `json:"content_hash"`
	ContentLength   int64      `json:"content_length"`
	Classification  string     `json:"classification"`
	RedactionState  string     `json:"redaction_state"`
	CoverageState   string     `json:"coverage_state"`
	Provenance      string     `json:"provenance"`
	ProductSurface  string     `json:"product_surface"`
	ObservedAt      time.Time  `json:"observed_at"`
	RetainedUntil   *time.Time `json:"retained_until,omitempty"`
}

type archiveMatchRecord struct {
	EvidenceID      string     `json:"evidence_id"`
	SourceID        string     `json:"source_id"`
	ProjectID       string     `json:"project_id"`
	SessionID       *string    `json:"session_id"`
	ExternalAliasID *string    `json:"external_alias_id"`
	SchemaVersion   string     `json:"schema_version"`
	ContentHashHex  string     `json:"content_hash_hex"`
	ContentLength   int64      `json:"content_length"`
	Classification  string     `json:"classification"`
	RedactionState  string     `json:"redaction_state"`
	CoverageState   string     `json:"coverage_state"`
	Provenance      string     `json:"provenance"`
	ProductSurface  string     `json:"product_surface"`
	ObservedAt      time.Time  `json:"observed_at"`
	RetainedUntil   *time.Time `json:"retained_until"`
}

// RetentionPlan is an immutable dry-run result and archive input.
type RetentionPlan struct {
	PolicyID  string            `json:"policy_id"`
	ProjectID string            `json:"project_id"`
	AsOf      time.Time         `json:"as_of"`
	Records   []ArchiveRecord   `json:"records"`
	Digest    [sha256.Size]byte `json:"-"`
}

// PlanRetention returns the next bounded, deterministic archive/purge batch.
func (r *Repository) PlanRetention(ctx context.Context, policyID string, asOf time.Time, limit int) (RetentionPlan, error) {
	if policyID == "" || limit < 1 || limit > maxRetentionBatch {
		return RetentionPlan{}, errors.New("invalid retention plan request")
	}
	var projectID string
	if err := r.pool.QueryRow(ctx, `SELECT project_id FROM prompt_better.retention_policies
        WHERE retention_policy_id=$1 AND retired_at IS NULL`, policyID).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RetentionPlan{}, ErrNotFound
		}
		return RetentionPlan{}, errors.New("read retention policy")
	}
	rows, err := r.pool.Query(ctx, `SELECT e.evidence_artifact_id, e.source_id,
		e.project_id, e.session_id, e.external_alias_id, e.schema_version,
		e.content_hash, e.content_length, e.classification, e.redaction_state,
		e.coverage_state, e.provenance, e.product_surface, e.observed_at, e.retained_until
        FROM prompt_better.evidence_artifacts e
        JOIN prompt_better.retention_policies p
          ON p.project_id=e.project_id AND p.classification=e.classification
         AND p.retired_at IS NULL
		WHERE p.retention_policy_id=$1 AND e.deleted_at IS NULL AND NOT p.legal_hold
          AND COALESCE(e.retained_until,
              e.observed_at + p.retain_for_seconds * interval '1 second') <= $2
        ORDER BY e.observed_at, e.evidence_artifact_id LIMIT $3`,
		policyID, asOf, limit)
	if err != nil {
		return RetentionPlan{}, errors.New("plan retention")
	}
	defer rows.Close()
	plan := RetentionPlan{PolicyID: policyID, ProjectID: projectID, AsOf: asOf.UTC(), Records: []ArchiveRecord{}}
	for rows.Next() {
		var record ArchiveRecord
		if err := rows.Scan(&record.EvidenceID, &record.SourceID, &record.ProjectID,
			&record.SessionID, &record.ExternalAliasID, &record.SchemaVersion,
			&record.ContentHash, &record.ContentLength, &record.Classification,
			&record.RedactionState, &record.CoverageState, &record.Provenance,
			&record.ProductSurface, &record.ObservedAt, &record.RetainedUntil); err != nil {
			return RetentionPlan{}, errors.New("scan retention plan")
		}
		plan.Records = append(plan.Records, record)
	}
	if err := rows.Err(); err != nil {
		return RetentionPlan{}, errors.New("iterate retention plan")
	}
	encoded, err := EncodeArchive(plan)
	if err != nil {
		return RetentionPlan{}, err
	}
	plan.Digest = sha256.Sum256(encoded)
	return plan, nil
}

// EncodeArchive returns deterministic normalized archive plaintext.
func EncodeArchive(plan RetentionPlan) ([]byte, error) {
	encoded, err := json.Marshal(struct {
		Format    string          `json:"format"`
		PolicyID  string          `json:"policy_id"`
		ProjectID string          `json:"project_id"`
		AsOf      time.Time       `json:"as_of"`
		Records   []ArchiveRecord `json:"records"`
	}{Format: "prompt-better-retention-v2", PolicyID: plan.PolicyID, ProjectID: plan.ProjectID,
		AsOf: plan.AsOf.UTC(), Records: plan.Records})
	if err != nil {
		return nil, errors.New("encode normalized archive")
	}
	return encoded, nil
}

// ArchiveReceipt proves a verified encrypted archive exists before deletion.
type ArchiveReceipt struct {
	BatchID                string
	ArchiveReference       string
	EncryptionKeyReference string
	EncryptedDigest        [sha256.Size]byte
	PlaintextDigest        [sha256.Size]byte
	VerifiedAt             time.Time
}

// RetentionApply identifies auditable rows for one archive-gated apply.
type RetentionApply struct {
	ActionID       string
	ActionEntityID string
	Plan           RetentionPlan
	Receipt        ArchiveReceipt
}

// ApplyRetention deletes exactly the planned batch after verified archive proof.
func (r *Repository) ApplyRetention(ctx context.Context, apply RetentionApply) (int64, error) {
	encodedPlan, err := EncodeArchive(apply.Plan)
	if err != nil {
		return 0, err
	}
	planDigest := sha256.Sum256(encodedPlan)
	if len(apply.Plan.Records) == 0 || apply.Plan.Digest != planDigest ||
		apply.Receipt.VerifiedAt.IsZero() ||
		apply.Receipt.PlaintextDigest != planDigest ||
		apply.Receipt.BatchID == "" || apply.Receipt.EncryptionKeyReference == "" ||
		apply.Receipt.EncryptedDigest == ([sha256.Size]byte{}) ||
		apply.Receipt.ArchiveReference != "sha256:"+hex.EncodeToString(apply.Receipt.EncryptedDigest[:]) {
		return 0, errors.New("verified archive does not match retention plan")
	}
	ids := make([]string, len(apply.Plan.Records))
	for i, record := range apply.Plan.Records {
		ids[i] = record.EvidenceID
	}
	matchPayload, err := encodeArchiveMatchRecords(apply.Plan.Records)
	if err != nil {
		return 0, err
	}
	sessionIDs := archiveSessionIDs(apply.Plan.Records)
	var applied int64
	err = r.WithSerializable(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, retentionLockID(planDigest)); err != nil {
			return databaseError("lock retention action", err)
		}
		if err := tx.QueryRow(ctx, `SELECT applied_count FROM prompt_better.retention_actions
            WHERE action_key=$1 AND status='complete'`, apply.Plan.Digest[:]).Scan(&applied); err == nil {
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return databaseError("read retention action", err)
		}
		if err := validateArchivedPlan(ctx, tx, apply.Plan, matchPayload); err != nil {
			return err
		}
		taskIDs, err := retentionTaskIDs(ctx, tx, sessionIDs)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.retention_actions
            (retention_action_id, retention_policy_id, project_id, action_key,
             mode, cutoff_at, planned_count, applied_count, status, started_at)
            VALUES ($1,$2,$3,$4,'apply',$5,$6,0,'running',statement_timestamp())`,
			apply.ActionID, apply.Plan.PolicyID, apply.Plan.ProjectID, apply.Plan.Digest[:],
			apply.Plan.AsOf, len(ids)); err != nil {
			return databaseError("record retention action", err)
		}
		rangeStart, rangeEnd := apply.Plan.Records[0].ObservedAt, apply.Plan.Records[0].ObservedAt
		for _, record := range apply.Plan.Records[1:] {
			if record.ObservedAt.Before(rangeStart) {
				rangeStart = record.ObservedAt
			}
			if record.ObservedAt.After(rangeEnd) {
				rangeEnd = record.ObservedAt
			}
		}
		if !rangeEnd.After(rangeStart) {
			rangeEnd = rangeStart.Add(time.Microsecond)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.archive_batches
            (archive_batch_id, project_id, retention_action_id, archive_reference,
             encryption_key_reference, format_version, range_start, range_end,
             entity_count, plaintext_digest, encrypted_digest, status, created_at, verified_at)
            VALUES ($1,$2,$3,$4,$5,'2.0.0',$6,$7,$8,$9,$10,'verified',statement_timestamp(),$11)`,
			apply.Receipt.BatchID, apply.Plan.ProjectID, apply.ActionID,
			apply.Receipt.ArchiveReference, apply.Receipt.EncryptionKeyReference,
			rangeStart, rangeEnd, len(ids), apply.Receipt.PlaintextDigest[:],
			apply.Receipt.EncryptedDigest[:], apply.Receipt.VerifiedAt); err != nil {
			return databaseError("record archive batch", err)
		}
		if err := deleteExpiredAuditRevisions(ctx, tx, ids); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM prompt_better.evidence_artifacts
            WHERE evidence_artifact_id=ANY($1::uuid[])`, ids)
		if err != nil {
			return databaseError("delete retained evidence", err)
		}
		applied = tag.RowsAffected()
		if applied != int64(len(ids)) {
			return errors.New("retention apply count mismatch")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM prompt_better.project_aliases a
            WHERE a.project_id=$1 AND NOT EXISTS (
                SELECT 1 FROM prompt_better.evidence_artifacts e
                WHERE e.project_id=a.project_id AND e.deleted_at IS NULL)`,
			apply.Plan.ProjectID); err != nil {
			return databaseError("delete unreferenced keyed aliases", err)
		}
		if err := deleteExpiredExecutionLineage(ctx, tx, sessionIDs, taskIDs); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM prompt_better.audit_windows audit_window
			WHERE audit_window.project_id=$1 AND NOT EXISTS (
				SELECT 1 FROM prompt_better.audit_revisions revision
				WHERE revision.audit_window_id=audit_window.audit_window_id)`,
			apply.Plan.ProjectID); err != nil {
			return databaseError("delete empty audit windows", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.retention_action_entities
			(retention_action_entity_id,retention_action_id,archive_batch_id,entity_kind,
			 planned_count,archived_count,deleted_count,archive_digest,deletion_digest,recorded_at)
			VALUES ($1,$2,$3,'evidence_artifact',$4,$4,$5,$6,$6,statement_timestamp())`,
			apply.ActionEntityID, apply.ActionID, apply.Receipt.BatchID, len(ids),
			applied, apply.Plan.Digest[:]); err != nil {
			return databaseError("record retention action entities", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE prompt_better.retention_actions
            SET applied_count=$2, status='complete', completed_at=statement_timestamp()
            WHERE retention_action_id=$1`, apply.ActionID, applied); err != nil {
			return databaseError("complete retention action", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return applied, nil
}

func encodeArchiveMatchRecords(records []ArchiveRecord) ([]byte, error) {
	matched := make([]archiveMatchRecord, len(records))
	for i, record := range records {
		matched[i] = archiveMatchRecord{
			EvidenceID: record.EvidenceID, SourceID: record.SourceID, ProjectID: record.ProjectID,
			SessionID: record.SessionID, ExternalAliasID: record.ExternalAliasID,
			SchemaVersion: record.SchemaVersion, ContentHashHex: hex.EncodeToString(record.ContentHash),
			ContentLength: record.ContentLength, Classification: record.Classification,
			RedactionState: record.RedactionState, CoverageState: record.CoverageState,
			Provenance: record.Provenance, ProductSurface: record.ProductSurface,
			ObservedAt: record.ObservedAt, RetainedUntil: record.RetainedUntil,
		}
	}
	payload, err := json.Marshal(matched)
	if err != nil {
		return nil, errors.New("encode retention match records")
	}
	return payload, nil
}

func archiveSessionIDs(records []ArchiveRecord) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(records))
	for _, record := range records {
		if record.SessionID == nil {
			continue
		}
		if _, exists := seen[*record.SessionID]; exists {
			continue
		}
		seen[*record.SessionID] = struct{}{}
		result = append(result, *record.SessionID)
	}
	return result
}

func validateArchivedPlan(ctx context.Context, tx pgx.Tx, plan RetentionPlan, payload []byte) error {
	rows, err := tx.Query(ctx, `WITH archived AS (
		SELECT * FROM jsonb_to_recordset($1::jsonb) AS record(
			evidence_id uuid, source_id uuid, project_id uuid, session_id uuid,
			external_alias_id uuid, schema_version text, content_hash_hex text,
			content_length bigint, classification text, redaction_state text,
			coverage_state text, provenance text, product_surface text,
			observed_at timestamptz, retained_until timestamptz))
		SELECT evidence.evidence_artifact_id
		FROM archived
		JOIN prompt_better.evidence_artifacts evidence
		  ON evidence.evidence_artifact_id=archived.evidence_id
		 AND evidence.source_id=archived.source_id
		 AND evidence.project_id=archived.project_id
		 AND evidence.session_id IS NOT DISTINCT FROM archived.session_id
		 AND evidence.external_alias_id IS NOT DISTINCT FROM archived.external_alias_id
		 AND evidence.schema_version=archived.schema_version
		 AND evidence.content_hash=decode(archived.content_hash_hex,'hex')
		 AND evidence.content_length=archived.content_length
		 AND evidence.classification=archived.classification
		 AND evidence.redaction_state=archived.redaction_state
		 AND evidence.coverage_state=archived.coverage_state
		 AND evidence.provenance=archived.provenance
		 AND evidence.product_surface=archived.product_surface
		 AND evidence.observed_at=archived.observed_at
		 AND evidence.retained_until IS NOT DISTINCT FROM archived.retained_until
		JOIN prompt_better.retention_policies policy
		  ON policy.project_id=evidence.project_id
		 AND policy.classification=evidence.classification
		 AND policy.retired_at IS NULL
		WHERE policy.retention_policy_id=$2 AND evidence.project_id=$3
		  AND evidence.deleted_at IS NULL AND NOT policy.legal_hold
		  AND coalesce(evidence.retained_until,
		      evidence.observed_at + policy.retain_for_seconds * interval '1 second') <= $4
		FOR UPDATE OF evidence`, payload, plan.PolicyID, plan.ProjectID, plan.AsOf)
	if err != nil {
		return databaseError("recheck archived retention plan", err)
	}
	defer rows.Close()
	matched := 0
	for rows.Next() {
		var evidenceID string
		if err := rows.Scan(&evidenceID); err != nil {
			return databaseError("decode archived retention match", err)
		}
		matched++
	}
	if err := rows.Err(); err != nil {
		return databaseError("iterate archived retention plan", err)
	}
	if matched != len(plan.Records) {
		return errors.New("retention plan changed before apply")
	}
	return nil
}

func retentionTaskIDs(ctx context.Context, tx pgx.Tx, sessionIDs []string) ([]string, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT turn.task_id::text
		FROM prompt_better.turns turn
		JOIN prompt_better.trajectories trajectory USING (trajectory_id)
		WHERE trajectory.session_id=ANY($1::uuid[]) AND turn.task_id IS NOT NULL`, sessionIDs)
	if err != nil {
		return nil, databaseError("select expired task candidates", err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			return nil, databaseError("decode expired task candidate", err)
		}
		result = append(result, taskID)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate expired task candidates", err)
	}
	return result, nil
}

func deleteExpiredAuditRevisions(ctx context.Context, tx pgx.Tx, evidenceIDs []string) error {
	_, err := tx.Exec(ctx, `DELETE FROM prompt_better.audit_revisions revision
		WHERE revision.audit_revision_id IN (
			SELECT link.target_id FROM prompt_better.evidence_links link
			WHERE link.evidence_artifact_id=ANY($1::uuid[])
			  AND link.target_kind='audit_revision'
			UNION
			SELECT result.audit_revision_id
			FROM prompt_better.metric_results result
			JOIN prompt_better.evidence_links link
			  ON link.target_kind='metric_result' AND link.target_id=result.metric_result_id
			WHERE link.evidence_artifact_id=ANY($1::uuid[])
			UNION
			SELECT finding.audit_revision_id
			FROM prompt_better.findings finding
			JOIN prompt_better.evidence_links link
			  ON link.target_kind='finding' AND link.target_id=finding.finding_id
			WHERE link.evidence_artifact_id=ANY($1::uuid[])
			UNION
			SELECT observation.audit_revision_id
			FROM prompt_better.observations observation
			WHERE observation.audit_revision_id IS NOT NULL
			  AND observation.evidence_artifact_id=ANY($1::uuid[])
			UNION
			SELECT recommendation.audit_revision_id
			FROM prompt_better.recommendations recommendation
			JOIN prompt_better.recommendation_events event USING (recommendation_id)
			WHERE event.evidence_artifact_id=ANY($1::uuid[]))
		AND NOT EXISTS (
			SELECT 1 FROM prompt_better.evidence_links retained_link
			WHERE NOT (retained_link.evidence_artifact_id=ANY($1::uuid[])) AND (
				(retained_link.target_kind='audit_revision'
				 AND retained_link.target_id=revision.audit_revision_id)
				OR (retained_link.target_kind='metric_result' AND EXISTS (
					SELECT 1 FROM prompt_better.metric_results result
					WHERE result.metric_result_id=retained_link.target_id
					  AND result.audit_revision_id=revision.audit_revision_id))
				OR (retained_link.target_kind='finding' AND EXISTS (
					SELECT 1 FROM prompt_better.findings finding
					WHERE finding.finding_id=retained_link.target_id
					  AND finding.audit_revision_id=revision.audit_revision_id))))
		AND NOT EXISTS (
			SELECT 1 FROM prompt_better.recommendations recommendation
			JOIN prompt_better.recommendation_events event USING (recommendation_id)
			WHERE recommendation.audit_revision_id=revision.audit_revision_id
			  AND event.evidence_artifact_id IS NOT NULL
			  AND NOT (event.evidence_artifact_id=ANY($1::uuid[])))
		AND NOT EXISTS (
			SELECT 1 FROM prompt_better.observations observation
			WHERE observation.audit_revision_id=revision.audit_revision_id
			  AND observation.evidence_artifact_id IS NOT NULL
			  AND NOT (observation.evidence_artifact_id=ANY($1::uuid[])))`, evidenceIDs)
	if err != nil {
		return databaseError("delete expired audit revisions", err)
	}
	return nil
}

func deleteExpiredExecutionLineage(ctx context.Context, tx pgx.Tx, sessionIDs, taskIDs []string) error {
	if len(sessionIDs) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM prompt_better.sessions session
			WHERE session.session_id=ANY($1::uuid[]) AND NOT EXISTS (
				SELECT 1 FROM prompt_better.evidence_artifacts evidence
				WHERE evidence.session_id=session.session_id AND evidence.deleted_at IS NULL)`, sessionIDs); err != nil {
			return databaseError("delete expired execution sessions", err)
		}
	}
	if len(taskIDs) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM prompt_better.tasks task
			WHERE task.task_id=ANY($1::uuid[]) AND NOT EXISTS (
				SELECT 1 FROM prompt_better.turns turn WHERE turn.task_id=task.task_id)`, taskIDs); err != nil {
			return databaseError("delete expired task identities", err)
		}
	}
	return nil
}

func retentionLockID(digest [sha256.Size]byte) int64 {
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

// MaintainAfterRetention updates planner statistics without blocking a busy table.
func (r *Repository) MaintainAfterRetention(ctx context.Context) error {
	for _, table := range []string{"evidence_artifacts", "evidence_links", "observations"} {
		if _, err := r.pool.Exec(ctx, `VACUUM (ANALYZE, SKIP_LOCKED) prompt_better.`+table); err != nil {
			return errors.New("maintain retained evidence tables")
		}
	}
	return nil
}
