package status

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
)

func TestBuild_ExplicitCoverageAndSanitizedBoundedErrors(t *testing.T) {
	t.Parallel()
	errors := make([]string, MaxErrors+5)
	for index := range errors {
		errors[index] = "/private/user/token-value"
	}
	report := Build(time.Date(2026, 1, 1, 1, 0, 0, 0, time.FixedZone("synthetic", 3600)), []Source{
		{Kind: "git", Enabled: false, Supported: true, Coverage: evidence.CoverageComplete, Errors: errors},
		{Kind: "github", Enabled: true, Supported: false, Coverage: evidence.CoverageComplete, Errors: []string{"offline"}},
	}, 2, 3)
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") || len(report.Sources[0].Errors) != MaxErrors {
		t.Fatalf("status leaked or exceeded bound: %s", data)
	}
	if report.Sources[0].Coverage != evidence.CoverageMissing || report.Sources[1].Coverage != evidence.CoverageUnknown || report.AsOf.Location() != time.UTC {
		t.Fatalf("coverage/watermark wrong: %#v", report)
	}
}
