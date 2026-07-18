package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// LatestSchemaVersion is the highest embedded migration version.
	LatestSchemaVersion       = 5
	maxTransactionAttempts    = 3
	transactionCleanupTimeout = 5 * time.Second
	maxLocalDatabaseBytes     = int64(1536 * 1024 * 1024)
	collectionBudgetLockID    = int64(0x50524f4d50544244)
	estimatedEvidenceBytes    = int64(64 * 1024)
	collectionBudgetReserve   = int64(16 * 1024 * 1024)
	maxDatabaseBytesEnv       = "PROMPT_BETTER_MAX_DATABASE_BYTES"
)

var (
	// ErrNotFound reports an absent normalized entity without exposing query data.
	ErrNotFound = errors.New("normalized entity not found")
	// ErrStaleCursor reports a duplicate or regressing sequence cursor.
	ErrStaleCursor = errors.New("cursor did not advance")
	// ErrSourceConflict reports a reused source identity with different content.
	ErrSourceConflict = errors.New("source identity conflicts with retained evidence")
	// ErrInvalidEffectiveTime reports a non-monotonic SCD2 effective time.
	ErrInvalidEffectiveTime = errors.New("dimension effective time must advance")
	// ErrStorageBudgetExceeded prevents local collection from growing without bound.
	ErrStorageBudgetExceeded = errors.New("local database storage budget reached; run retention or backup and reset")
)

// PoolConfig bounds database resources for local operation.
type PoolConfig struct {
	MaxConnections   int32
	MinConnections   int32
	MaxLifetime      time.Duration
	MaxIdleTime      time.Duration
	MaxDatabaseBytes int64
	ExpectedRole     DatabaseRole
}

// DatabaseRole is the least-privilege role required by an application path.
type DatabaseRole string

const (
	RoleRuntime   DatabaseRole = "prompt_better_runtime"
	RoleCollector DatabaseRole = "prompt_better_collector"
	RoleReporter  DatabaseRole = "prompt_better_reporter"
)

// OpenLocalRepository opens a repository with local defaults and an optional byte cap override.
func OpenLocalRepository(ctx context.Context, dsn string, expectedRole DatabaseRole) (*Repository, error) {
	if expectedRole != RoleRuntime && expectedRole != RoleCollector && expectedRole != RoleReporter {
		return nil, errors.New("invalid database application role")
	}
	limits := DefaultPoolConfig()
	limits.ExpectedRole = expectedRole
	if configured := os.Getenv(maxDatabaseBytesEnv); configured != "" {
		value, err := strconv.ParseInt(configured, 10, 64)
		if err != nil || value < 1 {
			return nil, errors.New("invalid local database byte budget")
		}
		limits.MaxDatabaseBytes = value
	}
	return OpenRepository(ctx, dsn, limits)
}

// DefaultPoolConfig returns conservative local connection bounds.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxConnections:   4,
		MinConnections:   0,
		MaxLifetime:      30 * time.Minute,
		MaxIdleTime:      5 * time.Minute,
		MaxDatabaseBytes: maxLocalDatabaseBytes,
	}
}

// Repository owns parameterized transactional persistence and bounded queries.
type Repository struct {
	pool             *pgxpool.Pool
	maxDatabaseBytes int64
}

type databaseOperationError struct {
	operation string
	cause     error
}

type transactionStarter interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

func (err databaseOperationError) Error() string { return err.operation }
func (err databaseOperationError) Unwrap() error { return err.cause }

func databaseError(operation string, cause error) error {
	return databaseOperationError{operation: operation, cause: cause}
}

func databaseCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), transactionCleanupTimeout)
}

// OpenRepository opens and verifies a bounded PostgreSQL pool.
func OpenRepository(ctx context.Context, dsn string, limits PoolConfig) (*Repository, error) {
	if dsn == "" {
		return nil, errors.New("database reference is required")
	}
	if limits.MaxConnections < 1 || limits.MinConnections < 0 || limits.MinConnections > limits.MaxConnections ||
		limits.MaxDatabaseBytes < 1 {
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
		if _, err := conn.Exec(ctx, "SET TIME ZONE 'UTC'"); err != nil {
			return err
		}
		if limits.ExpectedRole == "" {
			return nil
		}
		var currentRole string
		var privilegedLogin bool
		if err := conn.QueryRow(ctx, `SELECT current_role,
			role.rolsuper OR role.rolcreaterole OR role.rolcreatedb OR
			role.rolreplication OR role.rolbypassrls OR
			pg_has_role(session_user,'prompt_better_migrator','MEMBER')
			FROM pg_roles role WHERE role.rolname=session_user`).
			Scan(&currentRole, &privilegedLogin); err != nil ||
			currentRole != string(limits.ExpectedRole) || privilegedLogin {
			return errors.New("database application role mismatch")
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("open database pool")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("database is unavailable")
	}
	return &Repository{pool: pool, maxDatabaseBytes: limits.MaxDatabaseBytes}, nil
}

// Close releases all pool resources.
func (r *Repository) Close() { r.pool.Close() }

// DoctorResult is a sanitized storage-readiness report.
type DoctorResult struct {
	Ready         bool
	ServerVersion int
	SchemaVersion int64
	Role          string
	DatabaseBytes int64
	Problems      []string
	Recovery      []string
}

// Doctor checks connectivity, PostgreSQL version, schema version, and schema use.
func (r *Repository) Doctor(ctx context.Context) DoctorResult {
	result := DoctorResult{Problems: []string{}, Recovery: []string{}}
	var canUse bool
	var autovacuum string
	err := r.pool.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer,
		current_role, has_schema_privilege(current_role, 'prompt_better', 'USAGE'),
		current_setting('autovacuum'), pg_database_size(current_database())`).
		Scan(&result.ServerVersion, &result.Role, &canUse, &autovacuum, &result.DatabaseBytes)
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
	if autovacuum != "on" {
		result.Problems = append(result.Problems, "autovacuum_disabled")
	}
	if result.DatabaseBytes >= r.maxDatabaseBytes {
		result.Problems = append(result.Problems, "storage_budget_exceeded")
	}
	if err := r.pool.QueryRow(ctx, `SELECT COALESCE(max(applied.version_id), 0)
		FROM public.schema_migrations applied
		WHERE applied.is_applied AND NOT EXISTS (
			SELECT 1 FROM public.schema_migrations later
			WHERE later.version_id=applied.version_id AND later.id>applied.id)`).
		Scan(&result.SchemaVersion); err != nil {
		result.Problems = append(result.Problems, "schema_version_unavailable")
	} else if result.SchemaVersion != LatestSchemaVersion {
		result.Problems = append(result.Problems, "schema_version_mismatch_backup_reset_required")
		result.Recovery = append(result.Recovery, "create an encrypted backup, reset the local database, then run migrate")
	}
	result.Ready = len(result.Problems) == 0
	return result
}

// ConfiguredCollectors reports whether every configured source kind has an enabled current dimension.
func (r *Repository) ConfiguredCollectors(ctx context.Context, sourceKinds []string) (bool, error) {
	if len(sourceKinds) == 0 {
		return false, nil
	}
	var configured int
	if err := r.pool.QueryRow(ctx, `SELECT count(DISTINCT s.source_kind)
        FROM prompt_better.sources s
        JOIN prompt_better.source_versions v
          ON v.source_id = s.source_id AND v.valid_to IS NULL
        WHERE v.enabled AND s.source_kind = ANY($1::text[])`, sourceKinds).Scan(&configured); err != nil {
		return false, errors.New("read configured collectors")
	}
	return configured == len(sourceKinds), nil
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

// ProjectVersion is one effective SCD2 project state.
type ProjectVersion struct {
	ProjectID      string
	VersionID      string
	VersionNumber  int64
	Classification string
	LifecycleState string
	ValidFrom      time.Time
	ValidTo        *time.Time
}

// CurrentProject returns the sole open project version.
func (r *Repository) CurrentProject(ctx context.Context, projectID string) (ProjectVersion, error) {
	return r.projectVersion(ctx, projectID, nil)
}

// ProjectAsOf returns the half-open version effective at event time.
func (r *Repository) ProjectAsOf(ctx context.Context, projectID string, eventAt time.Time) (ProjectVersion, error) {
	return r.projectVersion(ctx, projectID, &eventAt)
}

func (r *Repository) projectVersion(ctx context.Context, projectID string, eventAt *time.Time) (ProjectVersion, error) {
	query := `SELECT project_id, project_version_id, version_number,
        classification, lifecycle_state, valid_from, valid_to
        FROM prompt_better.project_versions
        WHERE project_id=$1 AND valid_to IS NULL`
	arguments := []any{projectID}
	if eventAt != nil {
		query = `SELECT project_id, project_version_id, version_number,
            classification, lifecycle_state, valid_from, valid_to
            FROM prompt_better.project_versions
            WHERE project_id=$1 AND valid_from <= $2
              AND (valid_to > $2 OR valid_to IS NULL)`
		arguments = append(arguments, *eventAt)
	}
	var version ProjectVersion
	err := r.pool.QueryRow(ctx, query, arguments...).Scan(
		&version.ProjectID, &version.VersionID, &version.VersionNumber,
		&version.Classification, &version.LifecycleState,
		&version.ValidFrom, &version.ValidTo,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectVersion{}, ErrNotFound
	}
	if err != nil {
		return ProjectVersion{}, errors.New("read project version")
	}
	return version, nil
}

// PutProject inserts or updates normalized project state.
func (r *Repository) PutProject(ctx context.Context, project Project) error {
	hash := dimensionHash(project.Classification, project.LifecycleState)
	return r.WithSerializable(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.projects
            (project_id, created_at) VALUES ($1, $2)
            ON CONFLICT (project_id) DO NOTHING`, project.ID, project.CreatedAt); err != nil {
			return databaseError("persist project identity", err)
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
				return databaseError("persist project dimension", err)
			}
			return nil
		}
		if err != nil {
			return databaseError("read current project dimension", err)
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
			return databaseError("close project dimension", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.project_versions
            (project_version_id, project_id, version_number, classification,
             lifecycle_state, version_hash, valid_from)
            VALUES ($1,$2,$3,$4,$5,$6,$7)`, project.VersionID, project.ID,
			currentVersion+1, project.Classification, project.LifecycleState,
			hash[:], project.EffectiveAt); err != nil {
			return databaseError("persist project dimension", err)
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

// SourceVersion is one effective SCD2 source state.
type SourceVersion struct {
	SourceID       string
	VersionID      string
	VersionNumber  int64
	AdapterVersion *string
	ProductSurface string
	CoverageState  string
	Enabled        bool
	ValidFrom      time.Time
	ValidTo        *time.Time
}

// CurrentSource returns the sole open source version.
func (r *Repository) CurrentSource(ctx context.Context, sourceID string) (SourceVersion, error) {
	return r.sourceVersion(ctx, sourceID, nil)
}

// SourceAsOf returns the half-open version effective at event time.
func (r *Repository) SourceAsOf(ctx context.Context, sourceID string, eventAt time.Time) (SourceVersion, error) {
	return r.sourceVersion(ctx, sourceID, &eventAt)
}

func (r *Repository) sourceVersion(ctx context.Context, sourceID string, eventAt *time.Time) (SourceVersion, error) {
	query := `SELECT source_id, source_version_id, version_number,
        adapter_version, product_surface, coverage_state, enabled, valid_from, valid_to
        FROM prompt_better.source_versions
        WHERE source_id=$1 AND valid_to IS NULL`
	arguments := []any{sourceID}
	if eventAt != nil {
		query = `SELECT source_id, source_version_id, version_number,
            adapter_version, product_surface, coverage_state, enabled, valid_from, valid_to
            FROM prompt_better.source_versions
            WHERE source_id=$1 AND valid_from <= $2
              AND (valid_to > $2 OR valid_to IS NULL)`
		arguments = append(arguments, *eventAt)
	}
	var version SourceVersion
	err := r.pool.QueryRow(ctx, query, arguments...).Scan(
		&version.SourceID, &version.VersionID, &version.VersionNumber,
		&version.AdapterVersion, &version.ProductSurface, &version.CoverageState,
		&version.Enabled, &version.ValidFrom, &version.ValidTo,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceVersion{}, ErrNotFound
	}
	if err != nil {
		return SourceVersion{}, errors.New("read source version")
	}
	return version, nil
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
			return databaseError("persist source identity", err)
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
				return databaseError("persist source dimension", err)
			}
			return nil
		}
		if err != nil {
			return databaseError("read current source dimension", err)
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
			return databaseError("close source dimension", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.source_versions
            (source_version_id, source_id, version_number, adapter_version,
             product_surface, coverage_state, enabled, version_hash, valid_from)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, source.VersionID, source.ID,
			currentVersion+1, source.AdapterVersion, source.ProductSurface,
			source.CoverageState, source.Enabled, hash[:], source.EffectiveAt); err != nil {
			return databaseError("persist source dimension", err)
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
	SourceVersion   string
	ContentHash     []byte
	ContentLength   int64
	Classification  string
	RedactionState  string
	CoverageState   string
	Provenance      string
	ProductSurface  string
	ObservedAt      time.Time
	RetainedUntil   *time.Time
	Lineage         EvidenceLineage
	Runtime         RuntimeObservation
}

// EvidenceLineage contains normalized opaque UUIDs derived before persistence.
type EvidenceLineage struct {
	SessionID          string
	TrajectoryID       string
	ParentTrajectoryID string
	TaskID             string
	TurnID             string
	TurnOrdinal        *int64
	ResponseID         string
	ParentResponseID   string
	ToolCallID         string
	CallerToolCallID   string
	ToolKind           string
	CallPath           string
}

// RuntimeObservation contains bounded typed fields parsed before persistence.
type RuntimeObservation struct {
	Phase                 string
	PhaseEvent            string
	ResponseEvent         string
	TaskAttribution       string
	ModelVariant          string
	ReasoningEffort       string
	ReasoningMode         string
	Verbosity             string
	ServiceMode           string
	SafeguardOutcome      string
	ToolOutcome           string
	ResultState           string
	CanonicalCallHash     []byte
	StateEpochID          string
	StateEpochHash        []byte
	MutationState         string
	OutputModality        string
	OutputSizeBytes       *int64
	WaitState             string
	UsageKind             string
	UsageValue            *float64
	UsageUnit             string
	AccountingRegime      string
	CacheKind             string
	CacheValue            *float64
	CacheMode             string
	CacheTTLSeconds       *int64
	CheckpointEvent       string
	BoundaryEvent         string
	DelegationEvent       string
	StopEvent             string
	CompactionEvent       string
	Plan                  *PlanSnapshot
	Budget                *BudgetSnapshot
	Policy                *PolicySnapshot
	HostCapability        *HostCapabilitySnapshot
	AttributionConfidence *float64
	AttributionValidTo    *time.Time
	GovernanceOverhead    bool
}

// PlanSnapshot is immutable, content-free execution-plan evidence.
type PlanSnapshot struct {
	Hash             []byte
	PhaseScope       string
	ApprovalBoundary string
}

// BudgetSnapshot is the bounded execution budget linked to a plan snapshot.
type BudgetSnapshot struct {
	Enforcement            string
	MaxToolLoops           *int64
	MaxRetries             *int64
	MaxRetrievalExpansions *int64
	DelegationPolicy       string
	MaxAgentDepth          *int64
	MaxConcurrency         *int64
	ContextMode            string
	ExhaustionOutcome      string
}

// PolicySnapshot pins the privacy policy used for later audit replay.
type PolicySnapshot struct {
	SchemaVersion       string
	Hash                []byte
	RawRetentionEnabled bool
	TelemetryEnabled    bool
}

// HostCapabilitySnapshot records one bounded host capability observation.
type HostCapabilitySnapshot struct {
	HostKind        string
	HostVersion     string
	CapabilityName  string
	CapabilityState string
}

// CollectionBatch atomically persists bounded evidence and its next cursor.
type CollectionBatch struct {
	BatchID      string
	CursorID     string
	SourceID     string
	Next         uint64
	Generation   string
	CursorDigest string
	Fence        string
	ObservedAt   time.Time
	Evidence     []Evidence
}

// CollectionCursor returns zero when a source has no committed sequence cursor.
func (r *Repository) CollectionCursor(ctx context.Context, sourceID string) (uint64, error) {
	var value string
	err := r.pool.QueryRow(ctx, `SELECT cursor_value FROM prompt_better.collection_cursors
        WHERE source_id=$1 AND cursor_kind='sequence'`, sourceID).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, errors.New("read collection cursor")
	}
	_, _, next, err := decodeCollectionCursor(value)
	if err != nil {
		return 0, errors.New("invalid collection cursor")
	}
	return next, nil
}

// CommitCollection commits evidence before advancing the cursor in one transaction.
func (r *Repository) CommitCollection(ctx context.Context, batch CollectionBatch) (bool, error) {
	if batch.CursorID == "" || batch.SourceID == "" || batch.Next == 0 || batch.Fence == "" ||
		len(batch.Evidence) == 0 || len(batch.Evidence) > maxUsageBatch {
		return false, errors.New("collection batch is outside bounds")
	}
	for _, item := range batch.Evidence {
		if err := validateCollectionEvidence(item); err != nil {
			return false, err
		}
	}
	batchDigest, err := normalizedBatchDigest(batch.Evidence)
	if err != nil {
		return false, err
	}
	batchVersion := batch.CursorDigest + ":" + batchDigest
	committed := false
	err = r.WithSerializable(ctx, func(tx pgx.Tx) error {
		committed = false
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, collectionBudgetLockID); err != nil {
			return databaseError("lock local storage budget", err)
		}
		var databaseBytes int64
		if err := tx.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&databaseBytes); err != nil {
			return databaseError("check local storage budget", err)
		}
		estimatedBytes := estimatedCollectionBytes(batch.Evidence)
		if !storageBudgetAvailable(databaseBytes, estimatedBytes, r.maxDatabaseBytes) {
			return ErrStorageBudgetExceeded
		}
		var current uint64
		var storedGeneration, storedDigest string
		var cursorValue, cursorState string
		var leaseExpires *time.Time
		err := tx.QueryRow(ctx, `SELECT cursor_value, cursor_state, lease_expires_at
			FROM prompt_better.collection_cursors
			WHERE source_id=$1 AND cursor_kind='sequence' FOR UPDATE`, batch.SourceID).
			Scan(&cursorValue, &cursorState, &leaseExpires)
		if err == nil {
			var parsedCurrent uint64
			var parseErr error
			storedGeneration, storedDigest, parsedCurrent, parseErr = decodeCollectionCursor(cursorValue)
			current = parsedCurrent
			err = parseErr
			if err != nil {
				return errors.New("invalid collection cursor")
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return databaseError("lock collection cursor", err)
		}
		if cursorState != "leased:"+batch.Fence || leaseExpires == nil {
			return errors.New("collection lease fence rejected")
		}
		var leaseCurrent bool
		if err := tx.QueryRow(ctx, `SELECT $1 > statement_timestamp()`, *leaseExpires).Scan(&leaseCurrent); err != nil {
			return databaseError("validate collection lease fence", err)
		}
		if !leaseCurrent {
			return errors.New("collection lease fence rejected")
		}
		for _, item := range batch.Evidence {
			if item.SourceID != batch.SourceID {
				return errors.New("collection source mismatch")
			}
		}
		generationChanged := storedGeneration != "" && storedGeneration != batch.Generation
		if !generationChanged && batch.Next < current {
			return ErrStaleCursor
		}
		if !generationChanged && batch.Next == current {
			if !hmac.Equal([]byte(storedDigest), []byte(batch.CursorDigest)) {
				return ErrSourceConflict
			}
			var storedDigestValue, storedCount string
			err := tx.QueryRow(ctx, `SELECT source_version, state_value
				FROM prompt_better.observations WHERE observation_id=$1
				AND source_id=$2 AND observation_kind='source_assertion'
				AND metric_kind='collection_batch'`, collectionBatchUUID(batch.BatchID), batch.SourceID).
				Scan(&storedDigestValue, &storedCount)
			legacyVersion := storedDigestValue == batch.CursorDigest
			if err != nil || (!legacyVersion && storedDigestValue != batchVersion) ||
				storedCount != fmt.Sprintf("count:%d", len(batch.Evidence)) {
				return ErrSourceConflict
			}
			for _, item := range batch.Evidence {
				var hash []byte
				err := tx.QueryRow(ctx, `SELECT content_hash FROM prompt_better.evidence_artifacts
					WHERE source_id=$1 AND external_alias_id=$2`, item.SourceID, item.ExternalAliasID).Scan(&hash)
				if err != nil || !hmac.Equal(hash, item.ContentHash) {
					return ErrSourceConflict
				}
			}
			return nil
		}
		for _, item := range batch.Evidence {
			if err := persistEvidenceLineage(ctx, tx, item); err != nil {
				return err
			}
		}
		// A second bounded pass resolves explicit parents that appeared later in
		// the same batch without inventing placeholder lineage.
		for _, item := range batch.Evidence {
			if err := persistEvidenceLineage(ctx, tx, item); err != nil {
				return err
			}
		}
		for _, item := range batch.Evidence {
			tag, err := tx.Exec(ctx, `INSERT INTO prompt_better.evidence_artifacts
                (evidence_artifact_id, source_id, project_id, session_id,
                 external_alias_id, schema_version, content_hash, content_length,
                 classification, redaction_state, coverage_state, provenance,
                 product_surface, observed_at, retained_until)
                VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
                ON CONFLICT (source_id, external_alias_id)
                WHERE external_alias_id IS NOT NULL DO UPDATE SET
                    observed_at=EXCLUDED.observed_at,
                    retained_until=EXCLUDED.retained_until
                WHERE prompt_better.evidence_artifacts.content_hash=EXCLUDED.content_hash`,
				item.ID, item.SourceID, item.ProjectID, item.SessionID,
				item.ExternalAliasID, item.SchemaVersion, item.ContentHash,
				item.ContentLength, item.Classification, item.RedactionState,
				item.CoverageState, item.Provenance, item.ProductSurface,
				item.ObservedAt, item.RetainedUntil)
			if err != nil {
				return databaseError("persist collection evidence", err)
			}
			if tag.RowsAffected() == 0 {
				return ErrSourceConflict
			}
			if item.Lineage.TrajectoryID != "" {
				if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.evidence_links
					(evidence_link_id,evidence_artifact_id,target_kind,target_id,link_kind,created_at)
					VALUES ($1,$2,'trajectory',$3,'observed_in',$4)
					ON CONFLICT (evidence_link_id) DO NOTHING`,
					collectionBatchUUID(item.ID+":trajectory-evidence"), item.ID,
					item.Lineage.TrajectoryID, item.ObservedAt); err != nil {
					return databaseError("persist trajectory evidence link", err)
				}
			}
			if err := persistRuntimeObservations(ctx, tx, item); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.observations
			(observation_id,observation_kind,schema_version,source_id,metric_kind,
			 state_value,provenance,source_version,confidence,valid_from,
			 observed_at,knowledge_state)
			VALUES ($1,'source_assertion','1',$2,'collection_batch',$3,
			 'runtime_observed',$5,1,$4,$4,'observed')`,
			collectionBatchUUID(batch.BatchID), batch.SourceID,
			fmt.Sprintf("count:%d", len(batch.Evidence)), batch.ObservedAt, batchVersion); err != nil {
			return databaseError("persist collection batch identity", err)
		}
		if err := verifyCommittedStorageBudget(ctx, tx, r.maxDatabaseBytes); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO prompt_better.collection_cursors
            (collection_cursor_id, source_id, cursor_kind, cursor_value,
             cursor_state, observed_at, updated_at)
			VALUES ($1,$2,'sequence',$3,$5,$4,$4)
            ON CONFLICT (source_id, cursor_kind) DO UPDATE SET
                cursor_value=EXCLUDED.cursor_value,
                cursor_state=EXCLUDED.cursor_state,
                observed_at=EXCLUDED.observed_at,
				updated_at=EXCLUDED.updated_at
			WHERE prompt_better.collection_cursors.cursor_state=$5
			  AND prompt_better.collection_cursors.lease_expires_at > statement_timestamp()`,
			batch.CursorID, batch.SourceID, encodeCollectionCursor(batch.Generation, batch.CursorDigest, batch.Next), batch.ObservedAt, "leased:"+batch.Fence)
		if err != nil {
			return databaseError("advance collection cursor", err)
		}
		if tag.RowsAffected() != 1 {
			return errors.New("collection lease fence rejected")
		}
		committed = true
		return nil
	})
	return committed, err
}

func estimatedCollectionBytes(evidence []Evidence) int64 {
	if len(evidence) == 0 {
		return 0
	}
	return collectionBudgetReserve + int64(len(evidence))*estimatedEvidenceBytes
}

func storageBudgetAvailable(current, estimated, maximum int64) bool {
	return current >= 0 && estimated >= 0 && current < maximum && estimated < maximum-current
}

func verifyCommittedStorageBudget(ctx context.Context, tx pgx.Tx, maximum int64) error {
	var databaseBytes int64
	if err := tx.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&databaseBytes); err != nil {
		return databaseError("verify local storage budget", err)
	}
	if databaseBytes >= maximum {
		return ErrStorageBudgetExceeded
	}
	return nil
}

func validateCollectionEvidence(item Evidence) error {
	if len(item.ContentHash) != sha256.Size || item.SchemaVersion == "" || item.SourceID == "" ||
		item.ObservedAt.IsZero() {
		return errors.New("collection evidence is incomplete")
	}
	if item.Runtime.Plan != nil && (item.ProjectID == nil || len(item.Runtime.Plan.Hash) != sha256.Size ||
		item.Runtime.Plan.PhaseScope == "" || item.Runtime.Plan.ApprovalBoundary == "") {
		return errors.New("plan snapshot is incomplete")
	}
	if item.Runtime.Budget != nil && (item.Runtime.Plan == nil || item.Runtime.Budget.Enforcement == "" ||
		item.Runtime.Budget.DelegationPolicy == "" || item.Runtime.Budget.ExhaustionOutcome == "" ||
		!validOptionalNonnegative(item.Runtime.Budget.MaxToolLoops) ||
		!validOptionalNonnegative(item.Runtime.Budget.MaxRetries) ||
		!validOptionalNonnegative(item.Runtime.Budget.MaxRetrievalExpansions) ||
		!validOptionalNonnegative(item.Runtime.Budget.MaxAgentDepth) ||
		item.Runtime.Budget.MaxConcurrency != nil && *item.Runtime.Budget.MaxConcurrency < 1) {
		return errors.New("budget snapshot is incomplete")
	}
	if item.Runtime.Policy != nil && (item.ProjectID == nil || len(item.Runtime.Policy.Hash) != sha256.Size ||
		item.Runtime.Policy.SchemaVersion == "") {
		return errors.New("policy snapshot is incomplete")
	}
	if item.Runtime.HostCapability != nil && (item.Lineage.SessionID == "" ||
		item.Runtime.HostCapability.CapabilityName == "" || item.Runtime.HostCapability.CapabilityState == "") {
		return errors.New("host capability snapshot is incomplete")
	}
	if item.Runtime.AttributionConfidence != nil &&
		(*item.Runtime.AttributionConfidence < 0 || *item.Runtime.AttributionConfidence > 1 ||
			math.IsNaN(*item.Runtime.AttributionConfidence) || math.IsInf(*item.Runtime.AttributionConfidence, 0)) {
		return errors.New("project attribution is invalid")
	}
	if item.Runtime.AttributionValidTo != nil && !item.Runtime.AttributionValidTo.After(item.ObservedAt) {
		return errors.New("project attribution is invalid")
	}
	return nil
}

func validOptionalNonnegative(value *int64) bool {
	return value == nil || *value >= 0
}

func normalizedBatchDigest(evidence []Evidence) (string, error) {
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return "", errors.New("encode normalized collection batch")
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func persistEvidenceLineage(ctx context.Context, tx pgx.Tx, item Evidence) error {
	lineage := item.Lineage
	if lineage.SessionID == "" {
		return nil
	}
	if item.SessionID == nil || *item.SessionID != lineage.SessionID {
		return errors.New("collection lineage mismatch")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.sessions
		(session_id, project_id, source_id, started_at, coverage_state, knowledge_state)
		VALUES ($1,$2,$3,$4,$5,'observed')
		ON CONFLICT (session_id) DO UPDATE SET
			coverage_state = CASE
				WHEN prompt_better.sessions.coverage_state='complete' AND EXCLUDED.coverage_state<>'complete'
				THEN EXCLUDED.coverage_state
				ELSE prompt_better.sessions.coverage_state
			END`, lineage.SessionID, item.ProjectID, item.SourceID, item.ObservedAt, item.CoverageState); err != nil {
		return databaseError("persist collection session", err)
	}

	parentTrajectoryID, err := existingLineageID(ctx, tx,
		`SELECT trajectory_id FROM prompt_better.trajectories WHERE trajectory_id=$1`,
		lineage.ParentTrajectoryID)
	if err != nil {
		return err
	}
	if lineage.TrajectoryID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.trajectories
			(trajectory_id, session_id, source_id, external_alias_id,
			 parent_trajectory_id, started_at, knowledge_state)
			VALUES ($1,$2,$3,$1,$4,$5,'observed')
			ON CONFLICT (trajectory_id) DO UPDATE SET
				parent_trajectory_id=COALESCE(prompt_better.trajectories.parent_trajectory_id,
				EXCLUDED.parent_trajectory_id)`, lineage.TrajectoryID, lineage.SessionID,
			item.SourceID, parentTrajectoryID, item.ObservedAt); err != nil {
			return databaseError("persist collection trajectory", err)
		}
	}

	if lineage.TaskID != "" {
		attributionState := normalizedAttributionState(item.Runtime.TaskAttribution)
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.tasks
			(task_id,project_id,source_id,external_alias_id,task_kind,
			 attribution_state,confidence,algorithm_version,observed_at,knowledge_state)
			VALUES ($1,$2,$3,$1,'codex_task',$4,
			 CASE WHEN $4='attributed' THEN 1 ELSE NULL END,
			 'attribution-v1',$5,'observed')
			ON CONFLICT (task_id) DO UPDATE SET
				project_id=COALESCE(prompt_better.tasks.project_id,EXCLUDED.project_id),
				attribution_state=EXCLUDED.attribution_state,
				confidence=EXCLUDED.confidence`,
			lineage.TaskID, item.ProjectID, item.SourceID,
			attributionState, item.ObservedAt); err != nil {
			return databaseError("persist collection task", err)
		}
	}

	if lineage.TurnID != "" && lineage.TrajectoryID != "" && lineage.TurnOrdinal != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.turns
			(turn_id, trajectory_id, source_id, external_alias_id, task_id,
			 ordinal, observed_at, knowledge_state)
			VALUES ($1,$2,$3,$1,$4,$5,$6,'observed')
			ON CONFLICT (turn_id) DO UPDATE SET
				task_id=COALESCE(prompt_better.turns.task_id,EXCLUDED.task_id)`,
			lineage.TurnID, lineage.TrajectoryID, item.SourceID,
			nullableString(lineage.TaskID), *lineage.TurnOrdinal, item.ObservedAt); err != nil {
			return databaseError("persist collection turn", err)
		}
	}

	parentResponseID, err := existingLineageID(ctx, tx,
		`SELECT response_id FROM prompt_better.responses WHERE response_id=$1`,
		lineage.ParentResponseID)
	if err != nil {
		return err
	}
	if lineage.ResponseID != "" && lineage.TurnID != "" {
		completedAt := (*time.Time)(nil)
		if item.Runtime.ResponseEvent == "completed" {
			value := item.ObservedAt
			completedAt = &value
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.responses
			(response_id, turn_id, source_id, external_alias_id, parent_response_id,
			 model_variant,reasoning_effort,reasoning_mode,verbosity,service_mode,
			 safeguard_outcome,started_at,completed_at,knowledge_state)
			VALUES ($1,$2,$3,$1,$4,$5,$6,$7,$8,$9,$10,$11,$12,'observed')
			ON CONFLICT (response_id) DO UPDATE SET
				parent_response_id=COALESCE(prompt_better.responses.parent_response_id,
				EXCLUDED.parent_response_id),
				model_variant=COALESCE(EXCLUDED.model_variant,prompt_better.responses.model_variant),
				reasoning_effort=COALESCE(EXCLUDED.reasoning_effort,prompt_better.responses.reasoning_effort),
				reasoning_mode=COALESCE(EXCLUDED.reasoning_mode,prompt_better.responses.reasoning_mode),
				verbosity=COALESCE(EXCLUDED.verbosity,prompt_better.responses.verbosity),
				service_mode=COALESCE(EXCLUDED.service_mode,prompt_better.responses.service_mode),
				safeguard_outcome=COALESCE(EXCLUDED.safeguard_outcome,prompt_better.responses.safeguard_outcome),
				completed_at=COALESCE(EXCLUDED.completed_at,prompt_better.responses.completed_at)`,
			lineage.ResponseID, lineage.TurnID, item.SourceID, parentResponseID,
			nullableString(item.Runtime.ModelVariant), nullableString(item.Runtime.ReasoningEffort),
			nullableString(item.Runtime.ReasoningMode), nullableString(item.Runtime.Verbosity),
			nullableString(item.Runtime.ServiceMode), nullableString(item.Runtime.SafeguardOutcome),
			item.ObservedAt, completedAt); err != nil {
			return databaseError("persist collection response", err)
		}
	}

	runtimePhaseID := ""
	if item.Runtime.Phase != "" && lineage.TrajectoryID != "" {
		runtimePhaseID = collectionBatchUUID(item.ID + ":phase:" + item.Runtime.Phase)
		endedAt := (*time.Time)(nil)
		if item.Runtime.PhaseEvent == "completed" {
			value := item.ObservedAt
			endedAt = &value
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.phases
			(phase_id,trajectory_id,response_id,phase_kind,ordinal,started_at,ended_at,knowledge_state)
			VALUES ($1,$2,$3,$4,$5,$6,$7,'observed')
			ON CONFLICT (phase_id) DO UPDATE SET
				ended_at=COALESCE(EXCLUDED.ended_at,prompt_better.phases.ended_at)`,
			runtimePhaseID, lineage.TrajectoryID, nullableString(lineage.ResponseID),
			item.Runtime.Phase, normalizedOrdinal(item.ID+":phase:"+item.Runtime.Phase),
			item.ObservedAt, endedAt); err != nil {
			return databaseError("persist collection phase", err)
		}
	}
	toolPhaseID := ""
	if lineage.ToolCallID != "" && lineage.ResponseID != "" && lineage.TrajectoryID != "" {
		toolPhaseID = collectionBatchUUID(lineage.ResponseID + ":tool-phase")
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.phases
			(phase_id,trajectory_id,response_id,phase_kind,ordinal,started_at,ended_at,knowledge_state)
			VALUES ($1,$2,$3,'tool',$4,$5,$5,'observed')
			ON CONFLICT (phase_id) DO UPDATE SET ended_at=GREATEST(
				prompt_better.phases.ended_at,EXCLUDED.ended_at)`,
			toolPhaseID, lineage.TrajectoryID, lineage.ResponseID,
			normalizedOrdinal(lineage.ResponseID+":tool-phase"), item.ObservedAt); err != nil {
			return databaseError("persist collection tool phase", err)
		}
	}

	if lineage.ResponseID != "" && item.Runtime.OutputModality != "" {
		itemID := collectionBatchUUID(item.ID + ":item")
		itemKind := "assistant_output"
		if item.Runtime.ResultState != "" {
			itemKind = "tool_result"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.items
			(item_id,response_id,source_id,external_alias_id,item_kind,
			 assistant_phase,ordinal,image_detail,content_hash,content_length,
			 observed_at,knowledge_state)
			VALUES ($1,$2,$3,$1,$4,$5,$6,$7,$8,$9,$10,'observed')
			ON CONFLICT (item_id) DO UPDATE SET
				content_length=COALESCE(EXCLUDED.content_length,prompt_better.items.content_length)`,
			itemID, lineage.ResponseID, item.SourceID, itemKind,
			nullableString(item.Runtime.Phase), normalizedOrdinal(item.ID+":item"),
			nullableString(item.Runtime.OutputModality), item.ContentHash,
			item.Runtime.OutputSizeBytes, item.ObservedAt); err != nil {
			return databaseError("persist collection item", err)
		}
	}

	if item.Runtime.StateEpochID != "" && lineage.TrajectoryID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.state_epochs
			(state_epoch_id,trajectory_id,source_id,state_hash,mutation_state,
			 started_at,knowledge_state)
			VALUES ($1,$2,$3,$4,$5,$6,'observed')
			ON CONFLICT (state_epoch_id) DO UPDATE SET
				mutation_state=EXCLUDED.mutation_state`,
			item.Runtime.StateEpochID, lineage.TrajectoryID, item.SourceID,
			item.Runtime.StateEpochHash, defaultString(item.Runtime.MutationState, "unknown"),
			item.ObservedAt); err != nil {
			return databaseError("persist collection state epoch", err)
		}
	}

	if lineage.ToolCallID != "" && lineage.ResponseID != "" {
		callPath := lineage.CallPath
		if callPath != "direct" && callPath != "programmatic" {
			callPath = "unknown"
		}
		toolKind := lineage.ToolKind
		if toolKind == "" {
			toolKind = "unknown"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.tool_calls
			(tool_call_id, response_id, phase_id, source_id, external_alias_id, caller_alias_id,
			 call_path, tool_kind, started_at, outcome, knowledge_state,
			 state_epoch_id,canonical_call_hash,result_state,output_modality,
			 output_size_bytes,wait_state)
			VALUES ($1,$2,$3,$4,$1,$5,$6,$7,$8,$9,'observed',$10,$11,$12,$13,$14,$15)
			ON CONFLICT (tool_call_id) DO UPDATE SET
				outcome=COALESCE(EXCLUDED.outcome,prompt_better.tool_calls.outcome),
				phase_id=COALESCE(EXCLUDED.phase_id,prompt_better.tool_calls.phase_id),
				state_epoch_id=COALESCE(EXCLUDED.state_epoch_id,prompt_better.tool_calls.state_epoch_id),
				canonical_call_hash=COALESCE(EXCLUDED.canonical_call_hash,prompt_better.tool_calls.canonical_call_hash),
				result_state=COALESCE(EXCLUDED.result_state,prompt_better.tool_calls.result_state),
				output_modality=COALESCE(EXCLUDED.output_modality,prompt_better.tool_calls.output_modality),
				output_size_bytes=COALESCE(EXCLUDED.output_size_bytes,prompt_better.tool_calls.output_size_bytes),
				wait_state=COALESCE(EXCLUDED.wait_state,prompt_better.tool_calls.wait_state)`,
			lineage.ToolCallID, lineage.ResponseID, nullableString(toolPhaseID),
			item.SourceID, nullableString(lineage.CallerToolCallID), callPath,
			toolKind, item.ObservedAt, nullableString(item.Runtime.ToolOutcome),
			nullableString(item.Runtime.StateEpochID), item.Runtime.CanonicalCallHash,
			nullableString(item.Runtime.ResultState), nullableString(item.Runtime.OutputModality),
			item.Runtime.OutputSizeBytes, nullableString(item.Runtime.WaitState)); err != nil {
			return databaseError("persist collection tool call", err)
		}
	}
	if item.Runtime.DelegationEvent != "" && lineage.TrajectoryID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,trajectory_id,
			 parent_trajectory_id,source_id,external_alias_id,observed_at,knowledge_state)
			VALUES ($1,'delegation',$5,$7,$2,$3,$4,$1,$6,'observed')
			ON CONFLICT (execution_event_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":delegation"), lineage.TrajectoryID,
			nullableString(lineage.ParentTrajectoryID), item.SourceID,
			item.Runtime.DelegationEvent, item.ObservedAt, item.SchemaVersion); err != nil {
			return databaseError("persist delegation event", err)
		}
	}
	if item.Runtime.CompactionEvent != "" && lineage.TrajectoryID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,trajectory_id,
			 response_id,source_id,external_alias_id,observed_at,knowledge_state)
			VALUES ($1,'compaction',$5,$7,$2,$3,$4,$1,$6,'observed')
			ON CONFLICT (execution_event_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":compaction"), lineage.TrajectoryID,
			nullableString(lineage.ResponseID), item.SourceID,
			item.Runtime.CompactionEvent, item.ObservedAt, item.SchemaVersion); err != nil {
			return databaseError("persist compaction event", err)
		}
	}
	if item.Runtime.StopEvent != "" && lineage.TrajectoryID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,trajectory_id,
			 phase_id,outcome,observed_at,knowledge_state)
			VALUES ($1,'stop',$3,$6,$2,$4,'observed',$5,'observed')
			ON CONFLICT (execution_event_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":stop"), lineage.TrajectoryID,
			item.Runtime.StopEvent, nullableString(defaultString(toolPhaseID, runtimePhaseID)),
			item.ObservedAt, item.SchemaVersion); err != nil {
			return databaseError("persist stop event", err)
		}
	}
	if item.Runtime.CheckpointEvent != "" && lineage.SessionID != "" {
		hash := sha256.Sum256([]byte(item.Runtime.CheckpointEvent))
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,session_id,
			 content_hash,observed_at,knowledge_state)
			VALUES ($1,'checkpoint',$5,$6,$2,$3,$4,'observed')
			ON CONFLICT (execution_event_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":checkpoint"), lineage.SessionID,
			hash[:], item.ObservedAt, item.Runtime.CheckpointEvent, item.SchemaVersion); err != nil {
			return databaseError("persist checkpoint event", err)
		}
	}
	if item.Runtime.BoundaryEvent != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,project_id,
			 session_id,outcome,observed_at,knowledge_state)
			VALUES ($1,'boundary','runtime',$6,$2,$3,$4,$5,'observed')
			ON CONFLICT (execution_event_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":boundary"), item.ProjectID,
			nullableString(lineage.SessionID), item.Runtime.BoundaryEvent,
			item.ObservedAt, item.SchemaVersion); err != nil {
			return databaseError("persist boundary event", err)
		}
	}
	if err := persistReplaySnapshots(ctx, tx, item); err != nil {
		return err
	}
	return nil
}

func persistReplaySnapshots(ctx context.Context, tx pgx.Tx, item Evidence) error {
	runtime := item.Runtime
	planID := ""
	if runtime.Plan != nil {
		planID = collectionBatchUUID(item.ID + ":plan")
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,project_id,session_id,
			 source_id,outcome,content_hash,observed_at,knowledge_state)
			VALUES ($1,'plan_snapshot',$2,$3,$4,$5,$6,$7,$8,$9,'observed')
			ON CONFLICT (execution_event_id) DO NOTHING`,
			planID, runtime.Plan.PhaseScope, item.SchemaVersion, item.ProjectID,
			nullableString(item.Lineage.SessionID), item.SourceID,
			runtime.Plan.ApprovalBoundary, runtime.Plan.Hash, item.ObservedAt); err != nil {
			return databaseError("persist plan snapshot", err)
		}
	}
	if runtime.Budget != nil {
		budget := runtime.Budget
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,project_id,session_id,
			 source_id,related_event_id,state_value,outcome,attributes,observed_at,knowledge_state)
			VALUES ($1,'budget_snapshot',$2,$3,$4,$5,$6,$7,$8,$9,
			 jsonb_strip_nulls(jsonb_build_object(
			 'max_tool_loops',$10::bigint,'max_retries',$11::bigint,
			 'max_retrieval_expansions',$12::bigint,'max_agent_depth',$13::bigint,
			 'max_concurrency',$14::bigint,'context_mode',$15::text)),$16,'observed')
			ON CONFLICT (execution_event_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":budget"), budget.Enforcement, item.SchemaVersion,
			item.ProjectID, nullableString(item.Lineage.SessionID), item.SourceID, planID,
			budget.DelegationPolicy, budget.ExhaustionOutcome, budget.MaxToolLoops,
			budget.MaxRetries, budget.MaxRetrievalExpansions, budget.MaxAgentDepth,
			budget.MaxConcurrency, nullableString(budget.ContextMode), item.ObservedAt); err != nil {
			return databaseError("persist budget snapshot", err)
		}
	}
	if runtime.Policy != nil {
		policy := runtime.Policy
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,project_id,session_id,
			 source_id,content_hash,attributes,observed_at,knowledge_state)
			VALUES ($1,'policy_snapshot',$2,$3,$4,$5,$6,$7,
			 jsonb_build_object('raw_retention_enabled',$8::boolean,
			 'telemetry_enabled',$9::boolean),$10,'observed')
			ON CONFLICT (execution_event_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":policy"), policy.SchemaVersion, item.SchemaVersion,
			item.ProjectID, nullableString(item.Lineage.SessionID), item.SourceID, policy.Hash,
			policy.RawRetentionEnabled, policy.TelemetryEnabled, item.ObservedAt); err != nil {
			return databaseError("persist policy snapshot", err)
		}
	}
	if runtime.HostCapability != nil {
		host := runtime.HostCapability
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.execution_events
			(execution_event_id,event_kind,event_name,schema_version,project_id,session_id,
			 source_id,state_value,outcome,attributes,observed_at,knowledge_state)
			VALUES ($1,'host_capability',$2,$3,$4,$5,$6,$7,$8,
			 jsonb_strip_nulls(jsonb_build_object('host_version',$9::text)),$10,'observed')
			ON CONFLICT (execution_event_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":host-capability:"+host.CapabilityName),
			host.CapabilityName, item.SchemaVersion, item.ProjectID, item.Lineage.SessionID,
			item.SourceID, host.CapabilityState, nullableString(host.HostKind),
			nullableString(host.HostVersion), item.ObservedAt); err != nil {
			return databaseError("persist host capability snapshot", err)
		}
	}
	return nil
}

func persistRuntimeObservations(ctx context.Context, tx pgx.Tx, item Evidence) error {
	runtime := item.Runtime
	sourceVersion := defaultString(item.SourceVersion, item.SchemaVersion)
	if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.observations
		(observation_id,observation_kind,schema_version,project_id,source_id,
		 evidence_artifact_id,metric_kind,state_value,provenance,source_version,
		 confidence,valid_from,observed_at,knowledge_state)
		VALUES ($1,'source_assertion',$2,$3,$4,$5,'evidence_record',$6,$7,$8,
		 1,$9,$9,'observed')
		ON CONFLICT (observation_id) DO NOTHING`,
		collectionBatchUUID(item.ID+":source-assertion"), item.SchemaVersion,
		item.ProjectID, item.SourceID, item.ID, item.CoverageState, item.Provenance,
		sourceVersion, item.ObservedAt); err != nil {
		return databaseError("persist evidence source assertion", err)
	}
	if item.ProjectID != nil && runtime.TaskAttribution != "" {
		state := normalizedAttributionState(runtime.TaskAttribution)
		confidence := runtime.AttributionConfidence
		if confidence == nil && state == "attributed" {
			value := 1.0
			confidence = &value
		}
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.observations
			(observation_id,observation_kind,schema_version,project_id,source_id,
			 evidence_artifact_id,metric_kind,state_value,provenance,source_version,
			 confidence,valid_from,valid_to,observed_at,knowledge_state)
			VALUES ($1,'project_attribution',$2,$3,$4,$5,'project',$6,$7,$8,
			 $9,$10,$11,$10,'observed')
			ON CONFLICT (observation_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":project-attribution"), item.SchemaVersion,
			item.ProjectID, item.SourceID, item.ID, state, item.Provenance,
			sourceVersion, confidence, item.ObservedAt, runtime.AttributionValidTo); err != nil {
			return databaseError("persist project attribution", err)
		}
	}
	if runtime.UsageKind != "" && runtime.UsageValue != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.observations
			(observation_id,observation_kind,schema_version,project_id,source_id,
			 trajectory_id,response_id,evidence_artifact_id,metric_kind,value_numeric,
			 native_unit,product_surface,accounting_regime,provenance,source_adapter,source_version,
			 observed_at,knowledge_state)
			VALUES ($1,'usage',$12,$13,$14,$2,$3,$4,$5,$6,$7,$8,$9,$10,
			 'collector',$15,$11,'observed')
			ON CONFLICT (observation_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":usage:"+runtime.UsageKind),
			nullableString(item.Lineage.TrajectoryID), nullableString(item.Lineage.ResponseID),
			item.ID, runtime.UsageKind, *runtime.UsageValue,
			defaultString(runtime.UsageUnit, "unknown"), item.ProductSurface,
			defaultString(runtime.AccountingRegime, "unknown"), item.Provenance,
			item.ObservedAt, item.SchemaVersion, item.ProjectID, item.SourceID, sourceVersion); err != nil {
			return databaseError("persist collection usage", err)
		}
	}
	if runtime.CacheKind != "" && runtime.CacheValue != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.observations
			(observation_id,observation_kind,schema_version,project_id,source_id,
			 trajectory_id,response_id,evidence_artifact_id,metric_kind,value_numeric,
			 native_unit,provenance,source_adapter,source_version,attributes,observed_at,knowledge_state)
			VALUES ($1,'cache',$12,$13,$14,$2,$3,$4,$5,$6,$7,$10,'collector',$15,
			 jsonb_build_object('cache_mode',$8::text,'cache_ttl_seconds',$9::bigint),$11,'observed')
			ON CONFLICT (observation_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":cache:"+runtime.CacheKind),
			nullableString(item.Lineage.TrajectoryID), nullableString(item.Lineage.ResponseID),
			item.ID, runtime.CacheKind, *runtime.CacheValue,
			defaultString(runtime.UsageUnit, "unknown"),
			nullableString(runtime.CacheMode), runtime.CacheTTLSeconds,
			item.Provenance, item.ObservedAt, item.SchemaVersion, item.ProjectID, item.SourceID, sourceVersion); err != nil {
			return databaseError("persist collection cache", err)
		}
	}
	if runtime.GovernanceOverhead && item.Lineage.TrajectoryID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO prompt_better.observations
			(observation_id,observation_kind,schema_version,project_id,source_id,
			 trajectory_id,audit_revision_id,metric_kind,state_value,native_unit,
			 provenance,observed_at,knowledge_state)
			SELECT $1,'governance_overhead',$5,$4,$6,$2,ar.audit_revision_id,
			 'collector','necessary','events',$7,$3,'observed'
			FROM prompt_better.audit_revisions ar
			JOIN prompt_better.audit_windows aw ON aw.audit_window_id=ar.audit_window_id
			WHERE aw.project_id=$4 ORDER BY ar.created_at DESC LIMIT 1
			ON CONFLICT (observation_id) DO NOTHING`,
			collectionBatchUUID(item.ID+":governance"), item.Lineage.TrajectoryID,
			item.ObservedAt, item.ProjectID, item.SchemaVersion, item.SourceID, item.Provenance); err != nil {
			return databaseError("persist governance overhead", err)
		}
	}
	return nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func normalizedAttributionState(value string) string {
	switch value {
	case "attributed", "ambiguous", "multi_project", "unattributed":
		return value
	default:
		return "unknown"
	}
}

func existingLineageID(ctx context.Context, tx pgx.Tx, query, id string) (*string, error) {
	if id == "" {
		return nil, nil
	}
	var existing string
	if err := tx.QueryRow(ctx, query, id).Scan(&existing); errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, databaseError("resolve collection parent lineage", err)
	}
	return &existing, nil
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// CollectionGeneration returns the committed source generation.
func (r *Repository) CollectionGeneration(ctx context.Context, sourceID string) (string, error) {
	var value string
	err := r.pool.QueryRow(ctx, `SELECT cursor_value FROM prompt_better.collection_cursors
        WHERE source_id=$1 AND cursor_kind='sequence'`, sourceID).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("read collection generation")
	}
	generation, _, _, err := decodeCollectionCursor(value)
	if err != nil {
		return "", errors.New("invalid collection cursor")
	}
	return generation, nil
}

// CollectionDigest returns the keyed prefix checkpoint for the committed cursor.
func (r *Repository) CollectionDigest(ctx context.Context, sourceID string) (string, error) {
	var value string
	err := r.pool.QueryRow(ctx, `SELECT cursor_value FROM prompt_better.collection_cursors
        WHERE source_id=$1 AND cursor_kind='sequence'`, sourceID).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("read collection digest")
	}
	_, digest, _, err := decodeCollectionCursor(value)
	if err != nil {
		return "", errors.New("invalid collection cursor")
	}
	return digest, nil
}

// AcquireCollectionLease acquires or replaces an expired fenced source lease.
func (r *Repository) AcquireCollectionLease(ctx context.Context, cursorID, sourceID, owner string, now time.Time, duration time.Duration) (string, bool, error) {
	digest := sha256.Sum256([]byte(owner + now.UTC().Format(time.RFC3339Nano)))
	fence := hex.EncodeToString(digest[:16])
	tag, err := r.pool.Exec(ctx, `INSERT INTO prompt_better.collection_cursors
        (collection_cursor_id, source_id, cursor_kind, cursor_value, cursor_state,
         observed_at, lease_expires_at, updated_at)
		VALUES ($1,$2,'sequence','0',$3,statement_timestamp(),statement_timestamp()+$4::interval,statement_timestamp())
        ON CONFLICT (source_id, cursor_kind) DO UPDATE SET
          cursor_state=EXCLUDED.cursor_state,
          lease_expires_at=EXCLUDED.lease_expires_at,
          updated_at=EXCLUDED.updated_at
		WHERE prompt_better.collection_cursors.lease_expires_at IS NULL
		   OR prompt_better.collection_cursors.lease_expires_at <= statement_timestamp()`,
		cursorID, sourceID, "leased:"+fence, duration.String())
	if err != nil {
		return "", false, errors.New("acquire collection lease")
	}
	return fence, tag.RowsAffected() == 1, nil
}

func (r *Repository) RenewCollectionLease(ctx context.Context, sourceID, fence string, now time.Time, duration time.Duration) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE prompt_better.collection_cursors
		SET lease_expires_at=statement_timestamp()+$3::interval, updated_at=statement_timestamp()
		WHERE source_id=$1 AND cursor_kind='sequence' AND cursor_state=$2
		  AND lease_expires_at > statement_timestamp()`, sourceID, "leased:"+fence, duration.String())
	if err != nil {
		return false, errors.New("renew collection lease")
	}
	return tag.RowsAffected() == 1, nil
}

func collectionBatchUUID(batchID string) string {
	digest := sha256.Sum256([]byte(batchID))
	digest[6] = (digest[6] & 0x0f) | 0x40
	digest[8] = (digest[8] & 0x3f) | 0x80
	text := hex.EncodeToString(digest[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", text[:8], text[8:12], text[12:16], text[16:20], text[20:])
}

func normalizedOrdinal(identity string) int64 {
	digest := sha256.Sum256([]byte(identity))
	return int64(binary.BigEndian.Uint64(digest[:8]) & uint64(^uint64(0)>>1))
}

func (r *Repository) ReleaseCollectionLease(ctx context.Context, sourceID, fence string) error {
	_, err := r.pool.Exec(ctx, `UPDATE prompt_better.collection_cursors
        SET cursor_state='active', lease_expires_at=NULL, updated_at=statement_timestamp()
        WHERE source_id=$1 AND cursor_kind='sequence' AND cursor_state=$2`, sourceID, "leased:"+fence)
	if err != nil {
		return errors.New("release collection lease")
	}
	return nil
}

func encodeCollectionCursor(generation, digest string, sequence uint64) string {
	if generation == "" {
		return fmt.Sprintf("%d", sequence)
	}
	return generation + ":" + digest + ":" + fmt.Sprintf("%d", sequence)
}

func decodeCollectionCursor(value string) (string, string, uint64, error) {
	parts := strings.Split(value, ":")
	generation, digest, sequence := "", "", value
	if len(parts) == 3 {
		generation, digest, sequence = parts[0], parts[1], parts[2]
	} else if len(parts) == 2 {
		generation, sequence = parts[0], parts[1]
	}
	var next uint64
	if _, err := fmt.Sscanf(sequence, "%d", &next); err != nil {
		return "", "", 0, err
	}
	return generation, digest, next, nil
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
		if observation.ID == "" || observation.MetricKind == "" || observation.Value == nil ||
			*observation.Value < 0 || math.IsNaN(*observation.Value) || math.IsInf(*observation.Value, 0) ||
			observation.UsageUnit == "" || observation.Provenance == "" || observation.SourceAdapter == "" ||
			observation.SourceVersion == nil || *observation.SourceVersion == "" || observation.ObservedAt.IsZero() ||
			(observation.TrajectoryID == nil && observation.ResponseID == nil && observation.EvidenceID == nil) {
			return errors.New("usage observation is incomplete")
		}
		rows[i] = []any{observation.ID, "usage", "1", observation.TrajectoryID,
			observation.ResponseID, observation.EvidenceID, observation.MetricKind,
			observation.Value, observation.UsageUnit, observation.ProductSurface,
			observation.AccountingRegime, observation.Provenance,
			observation.SourceAdapter, observation.SourceVersion,
			observation.ObservedAt, observation.KnowledgeState}
	}
	count, err := r.pool.CopyFrom(ctx, pgx.Identifier{"prompt_better", "observations"},
		[]string{"observation_id", "observation_kind", "schema_version", "trajectory_id", "response_id",
			"evidence_artifact_id", "metric_kind", "value_numeric", "native_unit",
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
	return withSerializable(ctx, r.pool, fn)
}

func withSerializable(ctx context.Context, starter transactionStarter, fn func(pgx.Tx) error) error {
	if fn == nil {
		return errors.New("transaction callback is required")
	}
	for attempt := 1; attempt <= maxTransactionAttempts; attempt++ {
		tx, err := starter.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return databaseError("begin database transaction", err)
		}
		err = fn(tx)
		if err == nil {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				err = databaseError("commit database transaction", commitErr)
			}
		}
		if err == nil {
			return nil
		}
		rollbackCtx, cancel := databaseCleanupContext(ctx)
		_ = tx.Rollback(rollbackCtx)
		cancel()
		if !retryableTransactionFailure(err) || attempt == maxTransactionAttempts {
			return err
		}
	}
	return errors.New("database transaction retry exhausted")
}

func retryableTransactionFailure(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	return postgresError.Code == "40001" || postgresError.Code == "40P01"
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
