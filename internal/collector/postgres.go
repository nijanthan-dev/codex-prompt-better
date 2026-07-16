package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
	"github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

type PersistentSource struct {
	SourceID  string
	CursorID  string
	ProjectID string
}

// PostgresBackend implements durable cursor/evidence commits and fenced leases.
type PostgresBackend struct {
	repository *postgres.Repository
	sources    map[string]PersistentSource
}

func NewPostgresBackend(repository *postgres.Repository, sources map[string]PersistentSource) (*PostgresBackend, error) {
	if repository == nil || len(sources) == 0 {
		return nil, errors.New("persistent collector sources are required")
	}
	return &PostgresBackend{repository: repository, sources: sources}, nil
}

func (b *PostgresBackend) Cursor(ctx context.Context, kind string) (uint64, error) {
	source, ok := b.sources[kind]
	if !ok {
		return 0, errors.New("source mapping unavailable")
	}
	return b.repository.CollectionCursor(ctx, source.SourceID)
}

func (b *PostgresBackend) Generation(ctx context.Context, kind string) (string, error) {
	source, ok := b.sources[kind]
	if !ok {
		return "", errors.New("source mapping unavailable")
	}
	return b.repository.CollectionGeneration(ctx, source.SourceID)
}

func (b *PostgresBackend) Digest(ctx context.Context, kind string) (string, error) {
	source, ok := b.sources[kind]
	if !ok {
		return "", errors.New("source mapping unavailable")
	}
	return b.repository.CollectionDigest(ctx, source.SourceID)
}

func (b *PostgresBackend) Commit(ctx context.Context, commit Commit) (bool, error) {
	source, ok := b.sources[commit.SourceKind]
	if !ok {
		return false, errors.New("source mapping unavailable")
	}
	items := make([]postgres.Evidence, 0, len(commit.Records))
	for _, record := range commit.Records {
		hash, err := hex.DecodeString(record.ContentSHA256)
		if err != nil {
			return false, errors.New("invalid evidence digest")
		}
		id := deterministicUUID(record.ID)
		alias := id
		lineage := postgres.EvidenceLineage{
			SessionID:          lineageUUID(record.Lineage.SessionID),
			TrajectoryID:       lineageUUID(record.Lineage.TrajectoryID),
			ParentTrajectoryID: lineageUUID(record.Lineage.ParentTrajectoryID),
			TaskID:             lineageUUID(record.Lineage.TaskID),
			TurnID:             lineageUUID(record.Lineage.TurnID),
			TurnOrdinal:        record.Lineage.TurnOrdinal,
			ResponseID:         lineageUUID(record.Lineage.ResponseID),
			ParentResponseID:   lineageUUID(record.Lineage.ParentResponseID),
			ToolCallID:         lineageUUID(record.Lineage.ToolCallID),
			CallerToolCallID:   lineageUUID(record.Lineage.CallerToolCallID),
			ToolKind:           record.Lineage.ToolKind,
			CallPath:           record.Lineage.CallPath,
		}
		runtime, err := runtimeObservation(record)
		if err != nil {
			return false, err
		}
		var sessionID *string
		if lineage.SessionID != "" {
			sessionID = &lineage.SessionID
		}
		var projectID *string
		if source.ProjectID != "" {
			projectID = &source.ProjectID
		}
		items = append(items, postgres.Evidence{
			ID: id, SourceID: source.SourceID, ProjectID: projectID,
			SessionID: sessionID, ExternalAliasID: &alias,
			SchemaVersion: evidence.SchemaVersion, ContentHash: hash, ContentLength: int64(len(record.Attributes)),
			Classification: record.Classification, RedactionState: redactionState(record.RedactedFields),
			CoverageState: string(record.Coverage), Provenance: "runtime_observed",
			ProductSurface: record.ProductSurface, ObservedAt: record.ObservedAt,
			Lineage: lineage, Runtime: runtime,
		})
	}
	observedAt := time.Now().UTC()
	if len(commit.Records) > 0 {
		observedAt = commit.Records[0].IngestedAt
	}
	return b.repository.CommitCollection(ctx, postgres.CollectionBatch{
		BatchID: commit.BatchID, CursorID: source.CursorID, SourceID: source.SourceID, Next: commit.Cursor,
		Generation: commit.Generation, CursorDigest: commit.CursorDigest,
		Fence: commit.Fence, ObservedAt: observedAt,
		Evidence: items,
	})
}

func runtimeObservation(record evidence.Record) (postgres.RuntimeObservation, error) {
	attributes := record.Attributes
	runtime := postgres.RuntimeObservation{
		Phase: attributes["phase"], PhaseEvent: attributes["phase_event"],
		ResponseEvent:   attributes["response_event"],
		TaskAttribution: attributes["task_attribution_state"],
		ModelVariant:    attributes["model_variant"],
		ReasoningEffort: attributes["reasoning_effort"], ReasoningMode: attributes["reasoning_mode"],
		Verbosity: attributes["verbosity"], ServiceMode: attributes["service_mode"],
		SafeguardOutcome: attributes["safeguard_outcome"], ToolOutcome: attributes["tool_outcome"],
		ResultState: attributes["result_state"], MutationState: attributes["mutation_state"],
		OutputModality: attributes["output_modality"], WaitState: attributes["wait_state"],
		UsageKind: attributes["usage_kind"], UsageUnit: attributes["usage_unit"],
		AccountingRegime: attributes["accounting_regime"], CacheKind: attributes["cache_kind"],
		CacheMode: attributes["cache_mode"], CheckpointEvent: attributes["checkpoint_event"],
		BoundaryEvent: attributes["boundary_event"], DelegationEvent: attributes["delegation_event"],
		StopEvent: attributes["stop_event"], CompactionEvent: attributes["compaction_event"],
		GovernanceOverhead: record.GovernanceOverhead,
	}
	if value := attributes["canonical_call"]; value != "" {
		hash := sha256.Sum256([]byte(value))
		runtime.CanonicalCallHash = hash[:]
	}
	if value := attributes["state_epoch"]; value != "" {
		runtime.StateEpochID = deterministicUUID(value)
		hash := sha256.Sum256([]byte(value))
		runtime.StateEpochHash = hash[:]
	}
	var err error
	if runtime.OutputSizeBytes, err = parseOptionalInt(attributes["output_size_bytes"]); err != nil {
		return postgres.RuntimeObservation{}, errors.New("invalid output size")
	}
	if runtime.CacheTTLSeconds, err = parseOptionalInt(attributes["cache_ttl"]); err != nil {
		return postgres.RuntimeObservation{}, errors.New("invalid cache ttl")
	}
	if runtime.UsageValue, err = parseOptionalFloat(attributes["usage_value"]); err != nil {
		return postgres.RuntimeObservation{}, errors.New("invalid usage value")
	}
	if runtime.CacheValue, err = parseOptionalFloat(attributes["cache_value"]); err != nil {
		return postgres.RuntimeObservation{}, errors.New("invalid cache value")
	}
	return runtime, nil
}

func parseOptionalInt(value string) (*int64, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return nil, errors.New("invalid nonnegative integer")
	}
	return &parsed, nil
}

func parseOptionalFloat(value string) (*float64, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil, errors.New("invalid nonnegative number")
	}
	return &parsed, nil
}

func lineageUUID(value string) string {
	if value == "" {
		return ""
	}
	return deterministicUUID(value)
}

func (b *PostgresBackend) Acquire(ctx context.Context, kind, owner string, now time.Time, duration time.Duration) (string, bool, error) {
	source, ok := b.sources[kind]
	if !ok {
		return "", false, errors.New("source mapping unavailable")
	}
	return b.repository.AcquireCollectionLease(ctx, source.CursorID, source.SourceID, owner, now, duration)
}

func (b *PostgresBackend) Renew(ctx context.Context, kind, _ string, fence string, now time.Time, duration time.Duration) (bool, error) {
	source, ok := b.sources[kind]
	if !ok {
		return false, errors.New("source mapping unavailable")
	}
	return b.repository.RenewCollectionLease(ctx, source.SourceID, fence, now, duration)
}

func (b *PostgresBackend) Release(ctx context.Context, kind, _ string, fence string) error {
	source, ok := b.sources[kind]
	if !ok {
		return errors.New("source mapping unavailable")
	}
	return b.repository.ReleaseCollectionLease(ctx, source.SourceID, fence)
}

func deterministicUUID(value string) string {
	digest := sha256.Sum256([]byte(value))
	digest[6] = (digest[6] & 0x0f) | 0x40
	digest[8] = (digest[8] & 0x3f) | 0x80
	text := hex.EncodeToString(digest[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", text[:8], text[8:12], text[12:16], text[16:20], text[20:])
}

func redactionState(fields []string) string {
	if len(fields) > 0 {
		return "applied"
	}
	return "not_needed"
}
