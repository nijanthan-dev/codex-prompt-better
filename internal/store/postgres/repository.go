package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const latestSchemaVersion = 5

var (
	// ErrNotFound reports an absent normalized entity without exposing query data.
	ErrNotFound = errors.New("normalized entity not found")
	// ErrStaleCursor reports a duplicate or regressing sequence cursor.
	ErrStaleCursor = errors.New("cursor did not advance")
	// ErrSourceConflict reports a reused source identity with different content.
	ErrSourceConflict = errors.New("source identity conflicts with retained evidence")
	// ErrInvalidEffectiveTime reports a non-monotonic SCD2 effective time.
	ErrInvalidEffectiveTime = errors.New("dimension effective time must advance")
)

// PoolConfig bounds database resources for local operation.
type PoolConfig struct {
	MaxConnections int32
	MinConnections int32
	MaxLifetime    time.Duration
	MaxIdleTime    time.Duration
}

// DefaultPoolConfig returns conservative local connection bounds.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxConnections: 8,
		MinConnections: 1,
		MaxLifetime:    30 * time.Minute,
		MaxIdleTime:    5 * time.Minute,
	}
}

// Repository owns parameterized transactional persistence and bounded queries.
type Repository struct {
	pool *pgxpool.Pool
}

// OpenRepository opens and verifies a bounded PostgreSQL pool.
func OpenRepository(ctx context.Context, dsn string, limits PoolConfig) (*Repository, error) {
	if dsn == "" {
		return nil, errors.New("database reference is required")
	}
	if limits.MaxConnections < 1 || limits.MinConnections < 0 || limits.MinConnections > limits.MaxConnections {
		return nil, errors.New("invalid database pool bounds")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid database reference")
	}
	config.MaxConns = limits.MaxConnections
	config.MinConns = limits.MinConnections
	config.MaxConnLifetime = limits.MaxLifetime
	config.MaxConnIdleTime = limits.MaxIdleTime
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET TIME ZONE 'UTC'")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("open database pool")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("database is unavailable")
	}
	return &Repository{pool: pool}, nil
}

// Close releases all pool resources.
func (r *Repository) Close() { r.pool.Close() }

// DoctorResult is a sanitized storage-readiness report.
type DoctorResult struct {
	Ready         bool
	ServerVersion int
	SchemaVersion int64
	Role          string
	Problems      []string
}

// Doctor checks connectivity, PostgreSQL version, schema version, and schema use.
func (r *Repository) Doctor(ctx context.Context) DoctorResult {
	result := DoctorResult{Problems: []string{}}
	var canUse bool
	err := r.pool.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer,
        current_user, has_schema_privilege(current_user, 'prompt_better', 'USAGE')`).
		Scan(&result.ServerVersion, &result.Role, &canUse)
	if err != nil {
		result.Problems = append(result.Problems, "connectivity_unavailable")
		return result
	}
	if result.ServerVersion < 160000 {
		result.Problems = append(result.Problems, "postgres_version_unsupported")
	}
	if !canUse {
		result.Problems = append(result.Problems, "schema_access_denied")
	}
	if err := r.pool.QueryRow(ctx, `SELECT COALESCE(max(version_id), 0)
        FROM public.schema_migrations WHERE is_applied`).Scan(&result.SchemaVersion); err != nil {
		result.Problems = append(result.Problems, "schema_version_unavailable")
	} else if result.SchemaVersion != latestSchemaVersion {
		result.Problems = append(result.Problems, "schema_version_mismatch")
	}
	result.Ready = len(result.Problems) == 0
	return result
}

// Project is normalized project metadata with no raw identifier or display name.
type Project struct {
	ID             string
	VersionID      string
	CreatedAt      time.Time
	EffectiveAt    time.Time
	Classification string
	LifecycleState string
}

// PutProject inserts or updates normalized project state.
func (r *Repository) PutProject(ctx context.Context, project Project) error {
	hash := dimensionHash(project.Classification, project.LifecycleState)
	return r.WithSerializable(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.projects
            (project_id, created_at) VALUES ($1, $2)
            ON CONFLICT (project_id) DO NOTHING`, project.ID, project.CreatedAt); err != nil {
			return errors.New("persist project identity")
		}
		var currentID string
		var currentVersion int64
		var currentHash []byte
		var currentFrom time.Time
		err := tx.QueryRow(ctx, `SELECT project_version_id, version_number,
            version_hash, valid_from FROM prompt_better.project_versions
            WHERE project_id = $1 AND valid_to IS NULL FOR UPDATE`, project.ID).
			Scan(&currentID, &currentVersion, &currentHash, &currentFrom)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO prompt_better.project_versions
                (project_version_id, project_id, version_number, classification,
                 lifecycle_state, version_hash, valid_from)
                VALUES ($1,$2,1,$3,$4,$5,$6)`, project.VersionID, project.ID,
				project.Classification, project.LifecycleState, hash[:], project.EffectiveAt)
			if err != nil {
				return errors.New("persist project dimension")
			}
			return nil
		}
		if err != nil {
			return errors.New("read current project dimension")
		}
		if string(currentHash) == string(hash[:]) {
			return nil
		}
		if !project.EffectiveAt.After(currentFrom) {
			return ErrInvalidEffectiveTime
		}
		if _, err := tx.Exec(ctx, `UPDATE prompt_better.project_versions
            SET valid_to = $2 WHERE project_version_id = $1`, currentID,
			project.EffectiveAt); err != nil {
			return errors.New("close project dimension")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.project_versions
            (project_version_id, project_id, version_number, classification,
             lifecycle_state, version_hash, valid_from)
            VALUES ($1,$2,$3,$4,$5,$6,$7)`, project.VersionID, project.ID,
			currentVersion+1, project.Classification, project.LifecycleState,
			hash[:], project.EffectiveAt); err != nil {
			return errors.New("persist project dimension")
		}
		return nil
	})
}

// Source is a configured source without private identity material.
type Source struct {
	ID             string
	VersionID      string
	Kind           string
	AdapterVersion *string
	ProductSurface string
	CoverageState  string
	Enabled        bool
	CreatedAt      time.Time
	EffectiveAt    time.Time
}

// PutSource inserts or updates configured source state.
func (r *Repository) PutSource(ctx context.Context, source Source) error {
	adapter := ""
	if source.AdapterVersion != nil {
		adapter = *source.AdapterVersion
	}
	hash := dimensionHash(adapter, source.ProductSurface, source.CoverageState,
		fmt.Sprintf("%t", source.Enabled))
	return r.WithSerializable(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.sources
            (source_id, source_kind, created_at) VALUES ($1, $2, $3)
            ON CONFLICT (source_id) DO NOTHING`, source.ID, source.Kind,
			source.CreatedAt); err != nil {
			return errors.New("persist source identity")
		}
		var currentID string
		var currentVersion int64
		var currentHash []byte
		var currentFrom time.Time
		err := tx.QueryRow(ctx, `SELECT source_version_id, version_number,
            version_hash, valid_from FROM prompt_better.source_versions
            WHERE source_id = $1 AND valid_to IS NULL FOR UPDATE`, source.ID).
			Scan(&currentID, &currentVersion, &currentHash, &currentFrom)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO prompt_better.source_versions
                (source_version_id, source_id, version_number, adapter_version,
                 product_surface, coverage_state, enabled, version_hash, valid_from)
                VALUES ($1,$2,1,$3,$4,$5,$6,$7,$8)`, source.VersionID,
				source.ID, source.AdapterVersion, source.ProductSurface,
				source.CoverageState, source.Enabled, hash[:], source.EffectiveAt)
			if err != nil {
				return errors.New("persist source dimension")
			}
			return nil
		}
		if err != nil {
			return errors.New("read current source dimension")
		}
		if string(currentHash) == string(hash[:]) {
			return nil
		}
		if !source.EffectiveAt.After(currentFrom) {
			return ErrInvalidEffectiveTime
		}
		if _, err := tx.Exec(ctx, `UPDATE prompt_better.source_versions
            SET valid_to = $2 WHERE source_version_id = $1`, currentID,
			source.EffectiveAt); err != nil {
			return errors.New("close source dimension")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.source_versions
            (source_version_id, source_id, version_number, adapter_version,
             product_surface, coverage_state, enabled, version_hash, valid_from)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, source.VersionID, source.ID,
			currentVersion+1, source.AdapterVersion, source.ProductSurface,
			source.CoverageState, source.Enabled, hash[:], source.EffectiveAt); err != nil {
			return errors.New("persist source dimension")
		}
		return nil
	})
}

// AdvanceSequenceCursor atomically advances a monotonic per-source sequence.
func (r *Repository) AdvanceSequenceCursor(ctx context.Context, cursorID, sourceID string, next uint64, observedAt time.Time) error {
	tag, err := r.pool.Exec(ctx, `INSERT INTO prompt_better.collection_cursors
        (collection_cursor_id, source_id, cursor_kind, cursor_value,
         cursor_state, observed_at, updated_at)
        VALUES ($1, $2, 'sequence', $3, 'active', $4, $4)
        ON CONFLICT (source_id, cursor_kind) DO UPDATE SET
            cursor_value = EXCLUDED.cursor_value,
            cursor_state = EXCLUDED.cursor_state,
            observed_at = EXCLUDED.observed_at,
            updated_at = EXCLUDED.updated_at
        WHERE prompt_better.collection_cursors.cursor_value::numeric
              < EXCLUDED.cursor_value::numeric`, cursorID, sourceID,
		fmt.Sprintf("%d", next), observedAt)
	if err != nil {
		return errors.New("advance collection cursor")
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleCursor
	}
	return nil
}

// Evidence is normalized metadata; raw source payload is intentionally absent.
type Evidence struct {
	ID              string
	SourceID        string
	ProjectID       *string
	SessionID       *string
	ExternalAliasID *string
	SchemaVersion   string
	ContentHash     []byte
	ContentLength   int64
	Classification  string
	RedactionState  string
	CoverageState   string
	Provenance      string
	ProductSurface  string
	ObservedAt      time.Time
	RetainedUntil   *time.Time
}

const maxUsageBatch = 5000

// UsageObservation preserves one native-unit measurement without conversion.
type UsageObservation struct {
	ID               string
	TrajectoryID     *string
	ResponseID       *string
	EvidenceID       *string
	MetricKind       string
	Value            *float64
	UsageUnit        string
	ProductSurface   string
	AccountingRegime string
	Provenance       string
	SourceAdapter    string
	SourceVersion    *string
	ObservedAt       time.Time
	KnowledgeState   string
}

// PutUsageBatch inserts a bounded batch through PostgreSQL COPY.
func (r *Repository) PutUsageBatch(ctx context.Context, observations []UsageObservation) error {
	if len(observations) == 0 || len(observations) > maxUsageBatch {
		return errors.New("usage batch size is outside bounds")
	}
	rows := make([][]any, len(observations))
	for i, observation := range observations {
		rows[i] = []any{observation.ID, observation.TrajectoryID,
			observation.ResponseID, observation.EvidenceID, observation.MetricKind,
			observation.Value, observation.UsageUnit, observation.ProductSurface,
			observation.AccountingRegime, observation.Provenance,
			observation.SourceAdapter, observation.SourceVersion,
			observation.ObservedAt, observation.KnowledgeState}
	}
	count, err := r.pool.CopyFrom(ctx, pgx.Identifier{"prompt_better", "usage_observations"},
		[]string{"usage_observation_id", "trajectory_id", "response_id",
			"evidence_artifact_id", "metric_kind", "value_numeric", "usage_unit",
			"product_surface", "accounting_regime", "provenance", "source_adapter",
			"source_version", "observed_at", "knowledge_state"},
		pgx.CopyFromRows(rows))
	if err != nil || count != int64(len(observations)) {
		return errors.New("persist usage batch")
	}
	return nil
}

// PutEvidence idempotently persists one source-scoped evidence envelope.
func (r *Repository) PutEvidence(ctx context.Context, evidence Evidence) error {
	tag, err := r.pool.Exec(ctx, `INSERT INTO prompt_better.evidence_artifacts
        (evidence_artifact_id, source_id, project_id, session_id,
         external_alias_id, schema_version, content_hash, content_length,
         classification, redaction_state, coverage_state, provenance,
         product_surface, observed_at, retained_until)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
        ON CONFLICT (source_id, external_alias_id)
        WHERE external_alias_id IS NOT NULL
        DO UPDATE SET observed_at = EXCLUDED.observed_at,
                      retained_until = EXCLUDED.retained_until
        WHERE prompt_better.evidence_artifacts.content_hash = EXCLUDED.content_hash`,
		evidence.ID, evidence.SourceID, evidence.ProjectID, evidence.SessionID,
		evidence.ExternalAliasID, evidence.SchemaVersion, evidence.ContentHash,
		evidence.ContentLength, evidence.Classification, evidence.RedactionState,
		evidence.CoverageState, evidence.Provenance, evidence.ProductSurface,
		evidence.ObservedAt, evidence.RetainedUntil)
	if err != nil {
		return errors.New("persist evidence")
	}
	if tag.RowsAffected() == 0 {
		return ErrSourceConflict
	}
	return nil
}

// ProjectCoverage is the documented coverage-view result.
type ProjectCoverage struct {
	ProjectID             string
	SessionCount          int64
	EvidenceCount         int64
	CompleteEvidenceCount int64
	CoverageRatio         *float64
	DenominatorState      string
}

// GetProjectCoverage returns one project's missing-aware coverage.
func (r *Repository) GetProjectCoverage(ctx context.Context, projectID string) (ProjectCoverage, error) {
	var coverage ProjectCoverage
	err := r.pool.QueryRow(ctx, `SELECT project_id, session_count,
        evidence_count, complete_evidence_count, coverage_ratio,
        denominator_state FROM prompt_better.project_coverage
        WHERE project_id = $1`, projectID).Scan(&coverage.ProjectID,
		&coverage.SessionCount, &coverage.EvidenceCount,
		&coverage.CompleteEvidenceCount, &coverage.CoverageRatio,
		&coverage.DenominatorState)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectCoverage{}, ErrNotFound
	}
	if err != nil {
		return ProjectCoverage{}, errors.New("query project coverage")
	}
	return coverage, nil
}

// WithSerializable runs a multi-statement operation atomically and rolls back on
// callback, cancellation, serialization, or commit failure.
func (r *Repository) WithSerializable(ctx context.Context, fn func(pgx.Tx) error) error {
	if fn == nil {
		return errors.New("transaction callback is required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return errors.New("begin database transaction")
	}
	defer tx.Rollback(context.Background())
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit database transaction")
	}
	return nil
}

func dimensionHash(parts ...string) [32]byte {
	hash := sha256.New()
	var size [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		hash.Write(size[:])
		hash.Write([]byte(part))
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}
