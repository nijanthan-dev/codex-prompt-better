package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

type PersistentSource struct {
	SourceID string
	CursorID string
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
		items = append(items, postgres.Evidence{
			ID: id, SourceID: source.SourceID, ExternalAliasID: &alias,
			SchemaVersion: "1.0.0", ContentHash: hash, ContentLength: int64(len(record.Attributes)),
			Classification: record.Classification, RedactionState: redactionState(record.RedactedFields),
			CoverageState: string(record.Coverage), Provenance: "runtime_observed",
			ProductSurface: record.ProductSurface, ObservedAt: record.ObservedAt,
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
