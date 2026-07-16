// Package collector coordinates atomic, bounded incremental collection.
package collector

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/adapters"
	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
)

var (
	ErrLeaseHeld        = errors.New("source lease held")
	ErrBatchConflict    = errors.New("batch identity conflict")
	ErrCursorRegression = errors.New("cursor regression")
)

type Commit struct {
	BatchID      string
	SourceKind   string
	Cursor       uint64
	Generation   string
	CursorDigest string
	Fence        string
	Records      []evidence.Record
}

type Store interface {
	Cursor(context.Context, string) (uint64, error)
	Commit(context.Context, Commit) (bool, error)
}

type LeaseStore interface {
	Acquire(context.Context, string, string, time.Time, time.Duration) (string, bool, error)
	Renew(context.Context, string, string, string, time.Time, time.Duration) (bool, error)
	Release(context.Context, string, string, string) error
}

type generationStore interface {
	Generation(context.Context, string) (string, error)
}

type digestStore interface {
	Digest(context.Context, string) (string, error)
}

type QuarantineEntry struct {
	SourceKind string
	Code       string
	Attempt    int
	At         time.Time
}

type Options struct {
	Owner         string
	BatchLimit    int
	MaxAttempts   int
	LeaseDuration time.Duration
	BaseBackoff   time.Duration
	Now           func() time.Time
	Wait          func(context.Context, time.Duration) error
}

type Collector struct {
	store      Store
	leases     LeaseStore
	options    Options
	quarantine []QuarantineEntry
	mu         sync.Mutex
}

func New(store Store, leases LeaseStore, options Options) (*Collector, error) {
	if store == nil || leases == nil || options.Owner == "" || options.BatchLimit < 1 ||
		options.MaxAttempts < 1 || options.LeaseDuration <= 0 || options.BaseBackoff < 0 {
		return nil, errors.New("invalid collector options")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Wait == nil {
		options.Wait = wait
	}
	return &Collector{store: store, leases: leases, options: options, quarantine: []QuarantineEntry{}}, nil
}

func (c *Collector) Run(ctx context.Context, adapter adapters.Adapter) (adapters.Result, error) {
	now := c.options.Now()
	fence, acquired, err := c.leases.Acquire(ctx, adapter.Kind(), c.options.Owner, now, c.options.LeaseDuration)
	if err != nil {
		return adapters.Result{}, errors.New("acquire source lease")
	}
	if !acquired {
		return adapters.Result{}, ErrLeaseHeld
	}
	defer func() { _ = c.leases.Release(context.Background(), adapter.Kind(), c.options.Owner, fence) }()
	cursor, err := c.store.Cursor(ctx, adapter.Kind())
	if err != nil {
		return adapters.Result{}, errors.New("read source cursor")
	}
	generation := ""
	digest := ""
	if generations, ok := c.store.(generationStore); ok {
		generation, err = generations.Generation(ctx, adapter.Kind())
		if err != nil {
			return adapters.Result{}, errors.New("read source generation")
		}
	}
	if digests, ok := c.store.(digestStore); ok {
		digest, err = digests.Digest(ctx, adapter.Kind())
		if err != nil {
			return adapters.Result{}, errors.New("read source cursor digest")
		}
	}
	request := adapters.Request{Cursor: cursor, CursorGeneration: generation, CursorDigest: digest, Limit: c.options.BatchLimit, ObservedAt: now, MonotonicNanos: now.UnixNano()}
	var result adapters.Result
	for attempt := 1; attempt <= c.options.MaxAttempts; attempt++ {
		result, err = adapter.Collect(ctx, request)
		if err == nil || errors.Is(err, adapters.ErrDisabled) || errors.Is(err, adapters.ErrUnsupported) {
			break
		}
		if permanent(err) {
			c.addQuarantine(QuarantineEntry{SourceKind: adapter.Kind(), Code: errorCode(err), Attempt: attempt, At: now})
			return result, errors.New("source collection failed")
		}
		if attempt == c.options.MaxAttempts {
			c.addQuarantine(QuarantineEntry{SourceKind: adapter.Kind(), Code: errorCode(err), Attempt: attempt, At: now})
			return result, errors.New("source collection failed")
		}
		if err := c.options.Wait(ctx, c.options.BaseBackoff*time.Duration(1<<(attempt-1))); err != nil {
			return result, err
		}
	}
	if len(result.Records) == 0 {
		return result, err
	}
	renewed, renewErr := c.leases.Renew(ctx, adapter.Kind(), c.options.Owner, fence, c.options.Now(), c.options.LeaseDuration)
	if renewErr != nil {
		return result, errors.New("renew source lease")
	}
	if !renewed {
		return result, ErrLeaseHeld
	}
	batchID, idErr := evidence.OpaqueID([]byte("collector-batch-identity-key-32!!"), adapter.Kind(), result.Generation, result.CursorDigest, strconv.FormatUint(cursor, 10), strconv.FormatUint(result.NextCursor, 10))
	if idErr != nil {
		return result, idErr
	}
	_, commitErr := c.store.Commit(ctx, Commit{BatchID: batchID, SourceKind: adapter.Kind(), Cursor: result.NextCursor, Generation: result.Generation, CursorDigest: result.CursorDigest, Fence: fence, Records: result.Records})
	if commitErr != nil {
		return result, commitErr
	}
	return result, err
}

func permanent(err error) bool {
	return errors.Is(err, adapters.ErrMalformed) || errors.Is(err, adapters.ErrTruncated) ||
		errors.Is(err, evidence.ErrSensitive)
}

func (c *Collector) Quarantine() []QuarantineEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]QuarantineEntry{}, c.quarantine...)
}

func (c *Collector) addQuarantine(entry QuarantineEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.quarantine = append(c.quarantine, entry)
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, adapters.ErrMalformed):
		return "malformed"
	case errors.Is(err, adapters.ErrTruncated):
		return "truncated"
	case errors.Is(err, evidence.ErrSensitive):
		return "sensitive"
	default:
		return "adapter_failure"
	}
}

type MemoryStore struct {
	mu          sync.Mutex
	cursors     map[string]uint64
	batches     map[string]Commit
	records     map[string]evidence.Record
	generations map[string]string
	digests     map[string]string
	fail        bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{cursors: map[string]uint64{}, batches: map[string]Commit{}, records: map[string]evidence.Record{}, generations: map[string]string{}, digests: map[string]string{}}
}

func (s *MemoryStore) FailNextCommit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = true
}

func (s *MemoryStore) Cursor(_ context.Context, source string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursors[source], nil
}

func (s *MemoryStore) Generation(_ context.Context, source string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generations[source], nil
}

func (s *MemoryStore) Digest(_ context.Context, source string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.digests[source], nil
}

func (s *MemoryStore) Commit(_ context.Context, commit Commit) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		s.fail = false
		return false, errors.New("commit unavailable")
	}
	if existing, ok := s.batches[commit.BatchID]; ok {
		if existing.SourceKind != commit.SourceKind || existing.Cursor != commit.Cursor ||
			existing.Generation != commit.Generation || existing.CursorDigest != commit.CursorDigest ||
			!sameRecords(existing.Records, commit.Records) {
			return false, ErrBatchConflict
		}
		return false, nil
	}
	storedGeneration := s.generations[commit.SourceKind]
	if commit.Cursor < s.cursors[commit.SourceKind] && storedGeneration == commit.Generation {
		return false, ErrCursorRegression
	}
	if commit.Cursor == s.cursors[commit.SourceKind] && storedGeneration == commit.Generation &&
		s.digests[commit.SourceKind] != commit.CursorDigest {
		return false, ErrBatchConflict
	}
	for _, record := range commit.Records {
		if existing, ok := s.records[record.ID]; ok && existing.ContentSHA256 != record.ContentSHA256 {
			return false, ErrBatchConflict
		}
	}
	for _, record := range commit.Records {
		s.records[record.ID] = record
	}
	s.cursors[commit.SourceKind] = commit.Cursor
	s.generations[commit.SourceKind] = commit.Generation
	s.digests[commit.SourceKind] = commit.CursorDigest
	s.batches[commit.BatchID] = commit
	return true, nil
}

func sameRecords(left, right []evidence.Record) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].ContentSHA256 != right[index].ContentSHA256 {
			return false
		}
	}
	return true
}

func (s *MemoryStore) RecordCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

type MemoryLeases struct {
	mu     sync.Mutex
	leases map[string]lease
}

type lease struct {
	owner   string
	fence   string
	expires time.Time
}

func NewMemoryLeases() *MemoryLeases { return &MemoryLeases{leases: map[string]lease{}} }

func (l *MemoryLeases) Acquire(_ context.Context, source, owner string, now time.Time, duration time.Duration) (string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	current, held := l.leases[source]
	if held && current.owner != owner && current.expires.After(now) {
		return "", false, nil
	}
	fence := strconv.FormatInt(now.UnixNano(), 10) + ":" + owner
	l.leases[source] = lease{owner: owner, fence: fence, expires: now.Add(duration)}
	return fence, true, nil
}

func (l *MemoryLeases) Renew(_ context.Context, source, owner, fence string, now time.Time, duration time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	current, ok := l.leases[source]
	if !ok || current.owner != owner || current.fence != fence || !current.expires.After(now) {
		return false, nil
	}
	current.expires = now.Add(duration)
	l.leases[source] = current
	return true, nil
}

func (l *MemoryLeases) Release(_ context.Context, source, owner, fence string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if current, ok := l.leases[source]; ok && current.owner == owner && current.fence == fence {
		delete(l.leases, source)
	}
	return nil
}
