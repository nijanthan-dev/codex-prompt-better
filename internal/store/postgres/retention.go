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
	EvidenceID     string    `json:"evidence_id"`
	SourceID       string    `json:"source_id"`
	ProjectID      string    `json:"project_id"`
	ContentHash    []byte    `json:"content_hash"`
	ContentLength  int64     `json:"content_length"`
	Classification string    `json:"classification"`
	CoverageState  string    `json:"coverage_state"`
	Provenance     string    `json:"provenance"`
	ProductSurface string    `json:"product_surface"`
	ObservedAt     time.Time `json:"observed_at"`
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
        e.project_id, e.content_hash, e.content_length, e.classification,
        e.coverage_state, e.provenance, e.product_surface, e.observed_at
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
			&record.ContentHash, &record.ContentLength, &record.Classification,
			&record.CoverageState, &record.Provenance, &record.ProductSurface,
			&record.ObservedAt); err != nil {
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
	}{Format: "prompt-better-retention-v1", PolicyID: plan.PolicyID, ProjectID: plan.ProjectID,
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
		var eligible int64
		if err := tx.QueryRow(ctx, `SELECT count(*)
            FROM prompt_better.evidence_artifacts e
            JOIN prompt_better.retention_policies p
              ON p.project_id=e.project_id AND p.classification=e.classification
             AND p.retired_at IS NULL
            WHERE p.retention_policy_id=$1 AND e.project_id=$2
              AND e.evidence_artifact_id=ANY($3::uuid[])
              AND e.deleted_at IS NULL AND NOT p.legal_hold
              AND COALESCE(e.retained_until,
                  e.observed_at + p.retain_for_seconds * interval '1 second') <= $4`,
			apply.Plan.PolicyID, apply.Plan.ProjectID, ids, apply.Plan.AsOf).Scan(&eligible); err != nil {
			return databaseError("recheck retention plan", err)
		}
		if eligible != int64(len(ids)) {
			return errors.New("retention plan changed before apply")
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
            VALUES ($1,$2,$3,$4,$5,'1.0.0',$6,$7,$8,$9,$10,'verified',statement_timestamp(),$11)`,
			apply.Receipt.BatchID, apply.Plan.ProjectID, apply.ActionID,
			apply.Receipt.ArchiveReference, apply.Receipt.EncryptionKeyReference,
			rangeStart, rangeEnd, len(ids), apply.Receipt.PlaintextDigest[:],
			apply.Receipt.EncryptedDigest[:], apply.Receipt.VerifiedAt); err != nil {
			return databaseError("record archive batch", err)
		}
		for _, target := range []struct{ table, kind string }{
			{"audit_revisions", "audit_revision"},
			{"metric_results", "metric_result"},
			{"findings", "finding"},
		} {
			query := `DELETE FROM prompt_better.` + target.table + ` WHERE ` +
				target.kind + `_id IN (SELECT target_id FROM prompt_better.evidence_links
                    WHERE evidence_artifact_id=ANY($1::uuid[]) AND target_kind=$2)`
			if _, err := tx.Exec(ctx, query, ids, target.kind); err != nil {
				return databaseError("delete derived retention data", err)
			}
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
		if _, err := tx.Exec(ctx, `DELETE FROM prompt_better.tasks task
			WHERE task.project_id=$1 AND NOT EXISTS (
				SELECT 1 FROM prompt_better.evidence_artifacts evidence
				WHERE evidence.project_id=task.project_id AND evidence.deleted_at IS NULL)`,
			apply.Plan.ProjectID); err != nil {
			return databaseError("delete retained task identities", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM prompt_better.state_epochs epoch
			USING prompt_better.trajectories trajectory,
				prompt_better.sessions session
			WHERE epoch.trajectory_id=trajectory.trajectory_id
			  AND trajectory.session_id=session.session_id
			  AND session.project_id=$1
			  AND NOT EXISTS (
				SELECT 1 FROM prompt_better.evidence_artifacts evidence
				WHERE evidence.project_id=session.project_id
				  AND evidence.deleted_at IS NULL)`,
			apply.Plan.ProjectID); err != nil {
			return databaseError("delete retained state epochs", err)
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
