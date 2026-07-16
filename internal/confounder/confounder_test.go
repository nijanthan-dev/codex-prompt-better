package confounder

import "testing"

func TestNormalize_DoesNotInfer(t *testing.T) {
	t.Parallel()
	if got := Normalize("model", "synthetic", "observed"); got.Value != "synthetic" || got.Provenance != "observed" {
		t.Fatalf("observed value lost: %#v", got)
	}
	if got := Normalize("model", "synthetic", "inferred"); got.Value != "" || got.Provenance != "unknown" {
		t.Fatalf("inferred value retained: %#v", got)
	}
	if got := Normalize("price", "1.00", "observed"); got.Value != "" || got.Provenance != "unknown" {
		t.Fatalf("unsupported confounder retained: %#v", got)
	}
}
