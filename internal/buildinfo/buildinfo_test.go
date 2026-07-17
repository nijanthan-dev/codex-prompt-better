package buildinfo

import (
	"runtime"
	"testing"
)

func TestCurrentUsesEmbeddedAndRuntimeMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, Commit, BuildDate
	Version, Commit, BuildDate = "1.2.3", "0123456789abcdef", "2026-07-17T00:00:00Z"
	t.Cleanup(func() { Version, Commit, BuildDate = oldVersion, oldCommit, oldDate })

	got := Current()
	if got.Version != Version || got.Commit != Commit || got.BuildTimestamp != BuildDate {
		t.Fatalf("embedded metadata mismatch: %+v", got)
	}
	if got.GoVersion != runtime.Version() || got.OS != runtime.GOOS || got.Architecture != runtime.GOARCH {
		t.Fatalf("runtime metadata mismatch: %+v", got)
	}
}
