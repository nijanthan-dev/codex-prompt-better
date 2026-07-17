package collector

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/adapters"
	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
)

var testKey = []byte("synthetic-collector-key-32-bytes!!")

func TestCollector_CrashReplayIsAtomicAndDuplicateFree(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	store.failNextCommit()
	collector := newCollector(t, store, newMemoryLeases(), "owner-one")
	line := sourceLine("one")
	_, err := collector.Run(context.Background(), adapter(line))
	if err == nil {
		t.Fatal("commit failure not returned")
	}
	cursor, _ := store.Cursor(context.Background(), "git")
	if cursor != 0 || store.recordCount() != 0 {
		t.Fatalf("failed transaction advanced: cursor=%d records=%d", cursor, store.recordCount())
	}
	if _, err := collector.Run(context.Background(), adapter(line)); err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Run(context.Background(), adapter(line)); err != nil {
		t.Fatal(err)
	}
	cursor, _ = store.Cursor(context.Background(), "git")
	if cursor != 1 || store.recordCount() != 1 {
		t.Fatalf("replay duplicated: cursor=%d records=%d", cursor, store.recordCount())
	}
}

func TestCollector_LeasePreventsOverlap(t *testing.T) {
	t.Parallel()
	leases := newMemoryLeases()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, ok, err := leases.Acquire(context.Background(), "git", "other", now, time.Minute); err != nil || !ok {
		t.Fatal("fixture lease failed")
	}
	collector := newCollectorAt(t, newMemoryStore(), leases, "owner-one", now)
	if _, err := collector.Run(context.Background(), adapter(sourceLine("one"))); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("got %v, want lease held", err)
	}
}

func TestMemoryLeases_ExpiredFenceCannotRenewOrReleaseNewOwner(t *testing.T) {
	t.Parallel()
	leases := newMemoryLeases()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	oldFence, ok, err := leases.Acquire(context.Background(), "git", "old", now, time.Second)
	if err != nil || !ok {
		t.Fatal("old lease unavailable")
	}
	newFence, ok, err := leases.Acquire(context.Background(), "git", "new", now.Add(2*time.Second), time.Minute)
	if err != nil || !ok {
		t.Fatal("expired lease not replaceable")
	}
	if renewed, err := leases.Renew(context.Background(), "git", "old", oldFence, now.Add(2*time.Second), time.Minute); err != nil || renewed {
		t.Fatal("expired fence renewed")
	}
	if err := leases.Release(context.Background(), "git", "old", oldFence); err != nil {
		t.Fatal(err)
	}
	if renewed, err := leases.Renew(context.Background(), "git", "new", newFence, now.Add(3*time.Second), time.Minute); err != nil || !renewed {
		t.Fatal("stale release removed current owner")
	}
}

func TestCollector_BoundedRetryAndSanitizedQuarantine(t *testing.T) {
	t.Parallel()
	collector := newCollector(t, newMemoryStore(), newMemoryLeases(), "owner-one")
	_, err := collector.Run(context.Background(), adapter("{"))
	if err == nil || strings.Contains(err.Error(), "{") {
		t.Fatalf("unsanitized or absent error: %v", err)
	}
	entries := collector.Quarantine()
	if len(entries) != 1 || entries[0].Code != "malformed" || entries[0].Attempt != 1 {
		t.Fatalf("unexpected quarantine: %#v", entries)
	}
}

func TestMemoryStore_ConcurrentIdempotency(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	record := evidence.Record{ID: "id", ContentSHA256: "hash", Attributes: map[string]string{}, RedactedFields: []string{}}
	commit := Commit{BatchID: "batch", SourceKind: "git", Cursor: 1, Generation: "generation", Records: []evidence.Record{record}}
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := store.Commit(context.Background(), commit); err != nil {
				t.Errorf("commit: %v", err)
			}
		}()
	}
	group.Wait()
	if store.recordCount() != 1 {
		t.Fatalf("got %d records, want 1", store.recordCount())
	}
}

func TestCollector_DisabledSourceIsCoverageNotFailure(t *testing.T) {
	t.Parallel()
	collector := newCollector(t, newMemoryStore(), newMemoryLeases(), "owner-one")
	disabled := adapters.NewJSONLWithIdentity("git", "git", strings.NewReader(""), testKey, false, true)
	result, err := collector.Run(context.Background(), disabled)
	if !errors.Is(err, adapters.ErrDisabled) || result.Coverage != evidence.CoverageMissing {
		t.Fatalf("unexpected disabled result: %#v, %v", result, err)
	}
}

func TestCollector_LargeSyntheticIncrementalRunIsBounded(t *testing.T) {
	const records = 10_000
	var source bytes.Buffer
	for index := range records {
		source.WriteString(sourceLine(strconv.Itoa(index + 1)))
	}
	data := source.String()
	store := newMemoryStore()
	collector, err := New(store, newMemoryLeases(), Options{
		Owner: "performance", BatchLimit: 1_000, MaxAttempts: 1,
		LeaseDuration: time.Minute, BaseBackoff: 0,
		Now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for store.recordCount() < records {
		if _, err := collector.Run(context.Background(), adapter(data)); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(started); !raceEnabled && elapsed > 5*time.Second {
		t.Fatalf("large synthetic collection took %v, budget 5s", elapsed)
	}
	cursor, _ := store.Cursor(context.Background(), "git")
	if cursor != records || store.recordCount() != records {
		t.Fatalf("incomplete run: cursor=%d records=%d", cursor, store.recordCount())
	}
}

func newCollector(t *testing.T, store Store, leases LeaseStore, owner string) *Collector {
	t.Helper()
	return newCollectorAt(t, store, leases, owner, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
}

func newCollectorAt(t *testing.T, store Store, leases LeaseStore, owner string, now time.Time) *Collector {
	t.Helper()
	collector, err := New(store, leases, Options{
		Owner: owner, BatchLimit: 100, MaxAttempts: 3, LeaseDuration: time.Minute,
		BaseBackoff: time.Millisecond, Now: func() time.Time { return now },
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return collector
}

func adapter(data string) adapters.Adapter {
	return adapters.NewJSONLWithIdentity("git", "git", strings.NewReader(data), testKey, true, true)
}

func sourceLine(id string) string {
	return `{"version":"1","observed_at":"2026-01-01T00:00:00Z","attributes":{"event_id":"` + id + `"}}` + "\n"
}
