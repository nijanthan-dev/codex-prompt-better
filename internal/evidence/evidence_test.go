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
