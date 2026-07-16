package eval

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

type Corpus struct {
	Version string       `json:"version"`
	Cases   []CorpusCase `json:"cases"`
}

type CorpusCase struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Tags            []string `json:"tags"`
	MetricName      string   `json:"metric_name"`
	BaselineValue   float64  `json:"baseline_value"`
	CandidateValue  float64  `json:"candidate_value"`
	Coverage        string   `json:"coverage"`
	ExpectedStatus  string   `json:"expected_status"`
	ExpectedFinding string   `json:"expected_finding"`
	QualityGate     string   `json:"quality_gate"`
}

func DecodeCorpus(reader io.Reader) (Corpus, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var corpus Corpus
	if err := decoder.Decode(&corpus); err != nil {
		return Corpus{}, errors.New("decode evaluation corpus")
	}
	if corpus.Version == "" || len(corpus.Cases) == 0 {
		return Corpus{}, errors.New("evaluation corpus incomplete")
	}
	seen := map[string]bool{}
	for _, item := range corpus.Cases {
		if item.ID == "" || seen[item.ID] || item.Label == "" || len(item.Tags) == 0 ||
			item.MetricName == "" || item.Coverage == "" ||
			item.ExpectedStatus == "" || item.QualityGate == "" {
			return Corpus{}, errors.New("invalid evaluation corpus case")
		}
		seen[item.ID] = true
	}
	return corpus, nil
}

type RedactedTrace struct {
	Version        string             `json:"version"`
	MetricInputs   map[string]float64 `json:"metric_inputs"`
	Coverage       string             `json:"coverage"`
	WorkloadClass  string             `json:"workload_class"`
	Confounders    []string           `json:"confounders"`
	EvidenceHashes []string           `json:"evidence_hashes"`
}

func LoadRedactedTrace(path string, enabled bool) (RedactedTrace, error) {
	if !enabled {
		return RedactedTrace{}, errors.New("redacted trace loading is disabled")
	}
	file, err := os.Open(path)
	if err != nil {
		return RedactedTrace{}, errors.New("redacted trace unavailable")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var trace RedactedTrace
	if err := decoder.Decode(&trace); err != nil {
		return RedactedTrace{}, errors.New("invalid redacted trace")
	}
	if trace.Version == "" || trace.Coverage == "" {
		return RedactedTrace{}, errors.New("redacted trace incomplete")
	}
	for _, hash := range trace.EvidenceHashes {
		if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
			return RedactedTrace{}, errors.New("invalid redacted evidence hash")
		}
	}
	return trace, nil
}
