package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// KeyVersion stores only lifecycle metadata and a non-secret external reference.
type KeyVersion struct {
	ID          string
	Reference   string
	State       string
	CreatedAt   time.Time
	ActivatedAt *time.Time
}

// PutKeyVersion records keyed-HMAC lifecycle metadata without key material.
func (r *Repository) PutKeyVersion(ctx context.Context, version KeyVersion) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO prompt_better.key_versions
        (key_version_id,key_reference,algorithm,state,created_at,activated_at)
        VALUES ($1,$2,'hmac-sha256',$3,$4,$5)
        ON CONFLICT (key_version_id) DO UPDATE SET state=EXCLUDED.state,
            activated_at=EXCLUDED.activated_at`, version.ID, version.Reference,
		version.State, version.CreatedAt, version.ActivatedAt)
	if err != nil {
		return errors.New("persist key version metadata")
	}
	return nil
}

// ProjectAlias is a keyed digest; raw private identity is intentionally absent.
type ProjectAlias struct {
	ID           string
	ProjectID    string
	SourceID     string
	KeyVersionID string
	Kind         string
	Digest       []byte
	CreatedAt    time.Time
}

// PutProjectAlias persists a precomputed keyed alias.
func (r *Repository) PutProjectAlias(ctx context.Context, alias ProjectAlias) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO prompt_better.project_aliases
        (project_alias_id,project_id,source_id,key_version_id,alias_kind,
         alias_digest,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)
        ON CONFLICT (source_id,key_version_id,alias_kind,alias_digest)
        WHERE deleted_at IS NULL DO NOTHING`, alias.ID, alias.ProjectID,
		alias.SourceID, alias.KeyVersionID, alias.Kind, alias.Digest, alias.CreatedAt)
	if err != nil {
		return errors.New("persist keyed project alias")
	}
	return nil
}

// RekeyProjectAlias atomically replaces an old alias with a newly keyed digest.
func (r *Repository) RekeyProjectAlias(ctx context.Context, oldAliasID string, replacement ProjectAlias) error {
	return r.WithSerializable(ctx, func(tx pgx.Tx) error {
		var projectID, sourceID string
		if err := tx.QueryRow(ctx, `SELECT project_id,source_id FROM prompt_better.project_aliases
            WHERE project_alias_id=$1 FOR UPDATE`, oldAliasID).Scan(&projectID, &sourceID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return databaseError("read keyed project alias", err)
		}
		if projectID != replacement.ProjectID || sourceID != replacement.SourceID {
			return errors.New("rekey scope mismatch")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.project_aliases
            (project_alias_id,project_id,source_id,key_version_id,alias_kind,
             alias_digest,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			replacement.ID, replacement.ProjectID, replacement.SourceID,
			replacement.KeyVersionID, replacement.Kind, replacement.Digest,
			replacement.CreatedAt); err != nil {
			return databaseError("persist replacement project alias", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM prompt_better.project_aliases
            WHERE project_alias_id=$1`, oldAliasID); err != nil {
			return databaseError("delete replaced project alias", err)
		}
		return nil
	})
}

// MarkKeyLost makes loss explicit without changing or inventing aliases.
func (r *Repository) MarkKeyLost(ctx context.Context, keyVersionID string, at time.Time) error {
	tag, err := r.pool.Exec(ctx, `UPDATE prompt_better.key_versions
        SET state='lost',retired_at=$2 WHERE key_version_id=$1 AND state<>'deleted'`,
		keyVersionID, at)
	if err != nil {
		return errors.New("mark identity key lost")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteKeyVersion deletes aliases before lifecycle metadata; key material is
// deleted independently from the external encrypted key store.
func (r *Repository) DeleteKeyVersion(ctx context.Context, keyVersionID string, at time.Time) error {
	return r.WithSerializable(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM prompt_better.project_aliases
            WHERE key_version_id=$1`, keyVersionID); err != nil {
			return databaseError("delete keyed aliases", err)
		}
		tag, err := tx.Exec(ctx, `UPDATE prompt_better.key_versions
            SET state='deleted',deleted_at=$2 WHERE key_version_id=$1`, keyVersionID, at)
		if err != nil {
			return databaseError("delete key version metadata", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}
