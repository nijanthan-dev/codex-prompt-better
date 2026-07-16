package evidence

import (
	"errors"
	"testing"
	"time"
)

func TestOpaqueID_IsKeyedAndStable(t *testing.T) {
	t.Parallel()
	key := []byte("synthetic-key-material-32-bytes!!")
	first, err := OpaqueID(key, "project", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := OpaqueID(key, "project", "alpha")
	other, _ := OpaqueID([]byte("different-key-material-32-bytes!"), "project", "alpha")
	if first != second || first == other || first == "alpha" {
		t.Fatal("keyed identity invariant failed")
	}
}

func TestValidate_RejectsSecretLikeValue(t *testing.T) {
	t.Parallel()
	now := time.Now()
	record := Record{ID: "id", SourceKind: "git", IngestedAt: now, ObservedAt: now, RedactedFields: []string{}, Attributes: map[string]string{"note": "ghp_synthetic"}}
	if err := Validate(record); !errors.Is(err, ErrSensitive) {
		t.Fatalf("got %v, want sensitive rejection", err)
	}
}

func TestSanitize_CorrelatesCompatibleLineageDomains(t *testing.T) {
	t.Parallel()
	key := []byte("synthetic-key-material-32-bytes!!")
	state, _, err := Sanitize("codex_state_sqlite", key, map[string]string{"thread_alias": "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	jsonl, _, err := Sanitize("codex_jsonl", key, map[string]string{"session_alias": "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	if state["thread_alias"] != jsonl["session_alias"] {
		t.Fatal("compatible session aliases did not correlate")
	}
	trajectory, _, err := Sanitize("rollout_summary", key, map[string]string{"trajectory_alias": "session-one"})
	if err != nil {
		t.Fatal(err)
	}
	if trajectory["trajectory_alias"] == jsonl["session_alias"] {
		t.Fatal("incompatible alias domains correlated")
	}
}

func TestSanitize_KeysCanonicalCallAndStateEpoch(t *testing.T) {
	t.Parallel()
	key := []byte("0123456789abcdef0123456789abcdef")
	clean, _, err := Sanitize("codex_jsonl", key, map[string]string{
		"canonical_call": "tool:query:synthetic",
		"state_epoch":    "unchanged-state",
	})
	if err != nil {
		t.Fatal(err)
	}
	if clean["canonical_call"] == "tool:query:synthetic" ||
		clean["state_epoch"] == "unchanged-state" ||
		len(clean["canonical_call"]) != 64 ||
		len(clean["state_epoch"]) != 64 {
		t.Fatalf("keyed runtime identity missing: %#v", clean)
	}
}

func TestParseLineage_PreservesMissingAndInvalidValues(t *testing.T) {
	t.Parallel()
	lineage := ParseLineage(map[string]string{
		"session_alias": "opaque-session",
		"turn_ordinal":  "invalid",
		"call_path":     "direct",
	})
	if lineage.SessionID != "opaque-session" || lineage.TurnOrdinal != nil || lineage.TrajectoryID != "" {
		t.Fatalf("unexpected lineage: %+v", lineage)
	}
}
