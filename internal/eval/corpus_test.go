package eval

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/internal/diagnosis"
	"github.com/nijanthan-dev/codex-prompt-better/internal/replay"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestDecodeCorpusCoversRequiredProductionCases(t *testing.T) {
	t.Parallel()
	file, err := os.Open(filepath.Join("..", "..", "testdata", "evaluation", "governance-corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	corpus, err := DecodeCorpus(file)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"good": false, "bad": false, "ambiguous": false, "missing": false,
		"drifted": false, "portfolio_mix": false, "sparse_late": false,
		"nested_parallel_subagent": false, "binary_media": false,
		"governance_overhead": false, "privacy_suppression": false,
		"required_release_validation": false,
	}
	for _, item := range corpus.Cases {
		for _, tag := range item.Tags {
			if _, ok := required[tag]; ok {
				required[tag] = true
			}
		}
		coverage := contracts.CoverageState(item.Coverage)
		baselineValue, candidateValue := item.BaselineValue, item.CandidateValue
		baseline := contracts.AuditProjectResult{
			RevisionHash: "baseline",
			Metrics: []contracts.MetricResult{{
				Name: item.MetricName, Version: "metric-v1",
				NativeValue: &baselineValue, Coverage: contracts.CoverageStateComplete,
			}},
		}
		candidateMetric := contracts.MetricResult{
			Name: item.MetricName, Version: "metric-v1",
			NativeValue: &candidateValue, Coverage: coverage,
		}
		if coverage != contracts.CoverageStateComplete {
			candidateMetric.NativeValue = nil
		}
		comparison, err := replay.Compare(baseline, contracts.AuditProjectResult{
			RevisionHash: "candidate", Metrics: []contracts.MetricResult{candidateMetric},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := comparison.Statuses[item.MetricName]; string(got) != item.ExpectedStatus {
			t.Fatalf("%s status=%s want=%s", item.ID, got, item.ExpectedStatus)
		}
		if item.ExpectedFinding != "" {
			findings := diagnosis.Evaluate([]diagnosis.Observation{{
				Code: item.ExpectedFinding, Cause: "synthetic regression",
				ExceptionCheck: "synthetic exception", Classification: "avoidable",
				Coverage:      contracts.CoverageStateComplete,
				PositiveCount: 1, Denominator: 1, MinimumSample: 1,
				ConfounderMatched: true,
			}})
			if len(findings) != 1 || findings[0].Code != item.ExpectedFinding {
				t.Fatalf("%s finding not reproduced: %#v", item.ID, findings)
			}
		}
	}
	for tag, covered := range required {
		if !covered {
			t.Fatalf("required corpus tag %q missing", tag)
		}
	}
}

func TestEvaluationJSONRejectsTrailingDocuments(t *testing.T) {
	t.Parallel()
	corpus := `{"version":"1","cases":[{"id":"one","label":"one","tags":["good"],"metric_name":"validation_presence","coverage":"complete","expected_status":"flat","quality_gate":"pass"}]}`
	if _, err := DecodeCorpus(bytes.NewBufferString(corpus + `{}`)); err == nil {
		t.Fatal("trailing corpus document accepted")
	}
	path := filepath.Join(t.TempDir(), "trace.json")
	trace := `{"version":"1","coverage":"complete"} {}`
	if err := os.WriteFile(path, []byte(trace), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRedactedTrace(path, true); err == nil {
		t.Fatal("trailing trace document accepted")
	}
}

func TestLoadRedactedTraceIsOptInStrictAndContentFree(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "trace.json")
	valid := `{"version":"1","metric_inputs":{"turns":2},"coverage":"complete","workload_class":"validation","confounders":["model:same"],"evidence_hashes":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRedactedTrace(path, false); err == nil {
		t.Fatal("trace loaded without explicit opt-in")
	}
	if _, err := LoadRedactedTrace(path, true); err != nil {
		t.Fatal(err)
	}
	private := strings.Replace(valid, `"coverage"`, `"raw_prompt":"secret","coverage"`, 1)
	if err := os.WriteFile(path, []byte(private), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRedactedTrace(path, true); err == nil {
		t.Fatal("raw/private trace field accepted")
	}
}
