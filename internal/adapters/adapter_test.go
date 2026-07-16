package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
)

var fixtureKey = []byte("synthetic-collector-key-32-bytes!!")

func TestSourceAdapters_Contract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file string
		new  func(SourceOptions) Adapter
		kind string
	}{
		{name: "configuration", file: "configuration.jsonl", new: Configuration, kind: "configuration"},
		{name: "git", file: "git.jsonl", new: Git, kind: "git"},
		{name: "github", file: "github.jsonl", new: GitHub, kind: "github"},
		{name: "codex jsonl", file: "codex-jsonl.jsonl", new: CodexJSONL, kind: "codex_jsonl"},
		{name: "codex state", file: "codex-state.jsonl", new: CodexState, kind: "codex_state_sqlite"},
		{name: "rollout", file: "rollout.jsonl", new: Rollout, kind: "rollout_summary"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			adapter := test.new(options(t, test.file))
			result, err := adapter.Collect(context.Background(), request())
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Records) != 1 || result.Records[0].SourceKind != test.kind {
				t.Fatalf("unexpected adapter result: %#v", result)
			}
			if result.NextCursor != 1 || result.Coverage != evidence.CoverageComplete {
				t.Fatalf("unexpected cursor/coverage: %#v", result)
			}
			if test.kind == "codex_jsonl" && (result.Records[0].Lineage.SessionID == "" || result.Records[0].Lineage.ResponseID == "") {
				t.Fatalf("normalized lineage missing: %#v", result.Records[0].Lineage)
			}
		})
	}
}

func TestJSONL_CoverageAndFailureIsolation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		adapter Adapter
		wantErr error
		want    evidence.Coverage
	}{
		{name: "disabled", adapter: NewJSONLWithIdentity("git", "git", strings.NewReader(""), fixtureKey, false, true), wantErr: ErrDisabled, want: evidence.CoverageMissing},
		{name: "unsupported", adapter: NewJSONLWithIdentity("git", "git", strings.NewReader(""), fixtureKey, true, false), wantErr: ErrUnsupported, want: evidence.CoverageUnknown},
		{name: "missing", adapter: NewJSONLWithIdentity("git", "git", nil, fixtureKey, true, true), want: evidence.CoverageMissing},
		{name: "malformed", adapter: Git(options(t, "malformed.jsonl")), wantErr: ErrMalformed, want: evidence.CoveragePartial},
		{name: "sensitive redacted", adapter: Git(options(t, "sensitive.jsonl")), want: evidence.CoverageComplete},
		{name: "truncated", adapter: NewJSONLWithIdentity("git", "git", strings.NewReader("{\"attributes\":{\"x\":\""+strings.Repeat("x", DefaultMaxRecordBytes)+"\"}}"), fixtureKey, true, true), wantErr: ErrTruncated, want: evidence.CoveragePartial},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := test.adapter.Collect(context.Background(), request())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("got error %v, want %v", err, test.wantErr)
			}
			if result.Coverage != test.want {
				t.Fatalf("got coverage %q, want %q", result.Coverage, test.want)
			}
			if test.name == "sensitive redacted" && len(result.Records[0].RedactedFields) != 2 {
				t.Fatalf("sensitive fields not redacted: %#v", result.Records[0])
			}
		})
	}
}

func TestGit_RejectsBlankRequiredAlias(t *testing.T) {
	t.Parallel()
	adapter := Git(SourceOptions{
		Reader: strings.NewReader(`{"version":"1","observed_at":"2026-01-01T00:00:00Z","attributes":{"repository_alias":""}}` + "\n"),
		Key:    fixtureKey, Enabled: true, Supported: true,
	})
	result, err := adapter.Collect(context.Background(), request())
	if !errors.Is(err, ErrMalformed) || result.Reason != "schema_drift" {
		t.Fatalf("blank alias accepted: %#v, %v", result, err)
	}
}

func TestJSONL_IncompleteLineageRemainsPartial(t *testing.T) {
	t.Parallel()
	adapter := NewJSONLWithIdentity("codex_jsonl", "synthetic", strings.NewReader(
		`{"version":"1","observed_at":"2026-01-01T00:00:00Z","attributes":{"response_lineage":"response-only"}}`+"\n",
	), fixtureKey, true, true)
	result, err := adapter.Collect(context.Background(), request())
	if err != nil || result.Coverage != evidence.CoveragePartial || result.Reason != "lineage_incomplete" {
		t.Fatalf("incomplete lineage result=%#v error=%v", result, err)
	}
	if result.Records[0].Lineage.ResponseID == "" || result.Records[0].Lineage.SessionID != "" {
		t.Fatalf("incomplete lineage was inferred: %#v", result.Records[0].Lineage)
	}
}

func TestJSONL_RotationAndReplay(t *testing.T) {
	t.Parallel()
	adapter := Git(options(t, "git.jsonl"))
	first, err := adapter.Collect(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Collect(context.Background(), Request{Cursor: 1, CursorGeneration: first.Generation, Limit: 10, ObservedAt: time.Now()})
	if err != nil || len(result.Records) != 0 || result.NextCursor != 1 {
		t.Fatalf("replay changed evidence: %#v, %v", result, err)
	}
	rotated := Git(SourceOptions{Reader: strings.NewReader(sourceLines(1)), Key: fixtureKey, Enabled: true, Supported: true})
	result, err = rotated.Collect(context.Background(), Request{Cursor: 2, CursorGeneration: first.Generation, Limit: 10, ObservedAt: time.Now()})
	if err != nil || len(result.Records) != 1 || result.NextCursor != 1 || result.Coverage != evidence.CoveragePartial || !result.Records[0].LateRevision {
		t.Fatalf("rotation cursor handling changed: %#v, %v", result, err)
	}
}

func TestJSONL_ReusedAdapterDoesNotSkipBoundedBatches(t *testing.T) {
	t.Parallel()
	data := sourceLines(5)
	adapter := NewJSONLWithIdentity("git", "git", strings.NewReader(data), fixtureKey, true, true)
	first, err := adapter.Collect(context.Background(), Request{Limit: 2, ObservedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.Collect(context.Background(), Request{Cursor: first.NextCursor, CursorGeneration: first.Generation, CursorDigest: first.CursorDigest, Limit: 2, ObservedAt: time.Now()})
	if err != nil || len(second.Records) != 2 || second.Records[0].Sequence != 3 || second.Records[1].Sequence != 4 {
		t.Fatalf("bounded continuation skipped records: %#v, %v", second, err)
	}
}

func TestJSONL_AppendAfterReopenEmitsOnlyNewRecords(t *testing.T) {
	t.Parallel()
	firstAdapter := NewJSONLWithIdentity("git", "source-one", strings.NewReader(sourceLines(2)), fixtureKey, true, true)
	first, err := firstAdapter.Collect(context.Background(), Request{Limit: 10, ObservedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	appended := NewJSONLWithIdentity("git", "source-one", strings.NewReader(sourceLines(3)), fixtureKey, true, true)
	second, err := appended.Collect(context.Background(), Request{
		Cursor: first.NextCursor, CursorGeneration: first.Generation,
		CursorDigest: first.CursorDigest, Limit: 10, ObservedAt: time.Now(),
	})
	if err != nil || second.Coverage != evidence.CoverageComplete || len(second.Records) != 1 || second.Records[0].Sequence != 3 {
		t.Fatalf("append replayed or skipped evidence: %#v, %v", second, err)
	}
}

func TestProcess_RequiresOptInPurpose(t *testing.T) {
	t.Parallel()
	if _, err := Process(SourceOptions{Enabled: true}); !errors.Is(err, ErrPurposeRequired) {
		t.Fatalf("got %v, want purpose error", err)
	}
	adapter, err := Process(SourceOptions{Reader: openFixture(t, "process.jsonl"), Key: fixtureKey, Enabled: true, Supported: true, Purpose: "collector-health"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Collect(context.Background(), request())
	if err != nil || !result.Records[0].GovernanceOverhead || result.Records[0].Attributes["collection_trigger"] != "suppressed" {
		t.Fatalf("process boundary failed: %#v, %v", result, err)
	}
}

func TestSyntheticFixtures_ContainNoLocalOrSecretData(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(filepath.Join("..", "..", "testdata", "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "evidence", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(data))
		for _, forbidden := range []string{"/users/", "c:\\users\\", "ghp_", "sk-", "op://", "@gmail.com"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("fixture %s contains forbidden local/secret pattern", entry.Name())
			}
		}
	}
}

func FuzzJSONL(f *testing.F) {
	f.Add([]byte("{\"version\":\"1\",\"observed_at\":\"2026-01-01T00:00:00Z\",\"attributes\":{}}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		adapter := NewJSONLWithIdentity("git", "git", strings.NewReader(string(data)), fixtureKey, true, true)
		_, _ = adapter.Collect(context.Background(), request())
	})
}

func options(t *testing.T, name string) SourceOptions {
	t.Helper()
	return SourceOptions{Reader: openFixture(t, name), Key: fixtureKey, Enabled: true, Supported: true}
}

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "testdata", "evidence", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func request() Request {
	return Request{Limit: 10, SourceVersion: "1", ObservedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func sourceLines(count int) string {
	var builder strings.Builder
	for index := range count {
		builder.WriteString(`{"version":"1","observed_at":"2026-01-01T00:00:00Z","attributes":{"repository_alias":"project-`)
		builder.WriteString(strconv.Itoa(index))
		builder.WriteString(`"}}` + "\n")
	}
	return builder.String()
}
