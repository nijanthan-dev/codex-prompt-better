package eval

import (
	"bytes"
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
	var corpus Corpus
	if err := decodeStrictJSON(reader, &corpus); err != nil {
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
	var trace RedactedTrace
	if err := decodeStrictJSON(file, &trace); err != nil {
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

func decodeStrictJSON(reader io.Reader, destination any) error {
	const maxBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return errors.New("JSON input exceeds bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("JSON input has trailing data")
	}
	return nil
}
