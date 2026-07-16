package collector

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
)

type memoryStore struct {
	mu          sync.Mutex
	cursors     map[string]uint64
	batches     map[string]Commit
	records     map[string]evidence.Record
	generations map[string]string
	digests     map[string]string
	fail        bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		cursors:     map[string]uint64{},
		batches:     map[string]Commit{},
		records:     map[string]evidence.Record{},
		generations: map[string]string{},
		digests:     map[string]string{},
	}
}

func (s *memoryStore) failNextCommit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = true
}

func (s *memoryStore) Cursor(_ context.Context, source string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursors[source], nil
}

func (s *memoryStore) Generation(_ context.Context, source string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generations[source], nil
}

func (s *memoryStore) Digest(_ context.Context, source string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.digests[source], nil
}

func (s *memoryStore) Commit(_ context.Context, commit Commit) (bool, error) {
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

func (s *memoryStore) recordCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

type memoryLeases struct {
	mu     sync.Mutex
	leases map[string]lease
}

type lease struct {
	owner   string
	fence   string
	expires time.Time
}

func newMemoryLeases() *memoryLeases {
	return &memoryLeases{leases: map[string]lease{}}
}

func (l *memoryLeases) Acquire(_ context.Context, source, owner string, now time.Time, duration time.Duration) (string, bool, error) {
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

func (l *memoryLeases) Renew(_ context.Context, source, owner, fence string, now time.Time, duration time.Duration) (bool, error) {
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

func (l *memoryLeases) Release(_ context.Context, source, owner, fence string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if current, ok := l.leases[source]; ok && current.owner == owner && current.fence == fence {
		delete(l.leases, source)
	}
	return nil
}
