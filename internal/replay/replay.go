// Package replay compares immutable audit revisions without mutating evidence.
package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"

	"github.com/nijanthan-dev/codex-prompt-better/internal/metrics"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const Version = "replay-v1"

type Manifest struct {
	AuditRevisionHash string   `json:"audit_revision_hash"`
	InputHash         string   `json:"input_hash"`
	ProjectVersions   []string `json:"project_versions"`
	SourceVersions    []string `json:"source_versions"`
	MetricVersions    []string `json:"metric_versions"`
	PolicyVersions    []string `json:"policy_versions"`
	AsOf              string   `json:"as_of"`
}

func NewManifest(result contracts.AuditProjectResult, input any,
	projectVersions, sourceVersions, policyVersions []string,
) (Manifest, error) {
	if result.RevisionHash == "" || result.Window.AsOf.IsZero() {
		return Manifest{}, errors.New("replay revision provenance required")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return Manifest{}, errors.New("encode replay input")
	}
	digest := sha256.Sum256(encoded)
	metricVersions := []string{}
	for _, metric := range result.Metrics {
		metricVersions = append(metricVersions, metric.Name+":"+metric.Version)
	}
	sort.Strings(metricVersions)
	projectVersions = sortedCopy(projectVersions)
	sourceVersions = sortedCopy(sourceVersions)
	policyVersions = sortedCopy(policyVersions)
	return Manifest{
		AuditRevisionHash: result.RevisionHash,
		InputHash:         hex.EncodeToString(digest[:]),
		ProjectVersions:   projectVersions, SourceVersions: sourceVersions,
		MetricVersions: metricVersions, PolicyVersions: policyVersions,
		AsOf: result.Window.AsOf.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func VerifyImmutable(before, after Manifest) error {
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return errors.New("encode replay manifest")
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return errors.New("encode replay manifest")
	}
	if string(beforeJSON) != string(afterJSON) {
		return errors.New("replay source or version manifest changed")
	}
	return nil
}

func sortedCopy(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	return result
}

type Comparison struct {
	BaselineHash     string                            `json:"baseline_hash"`
	CandidateHash    string                            `json:"candidate_hash"`
	Statuses         map[string]contracts.MetricStatus `json:"statuses"`
	Outcome          string                            `json:"outcome"`
	QualityGateState string                            `json:"quality_gate_state"`
}

func Compare(baseline, candidate contracts.AuditProjectResult) (Comparison, error) {
	if baseline.RevisionHash == "" || candidate.RevisionHash == "" {
		return Comparison{}, errors.New("replay revisions required")
	}
	statuses := map[string]contracts.MetricStatus{}
	baselineMetrics := map[string]contracts.MetricResult{}
	for _, metric := range baseline.Metrics {
		if _, exists := baselineMetrics[metric.Name]; exists {
			return Comparison{}, errors.New("baseline metric names must be unique")
		}
		baselineMetrics[metric.Name] = metric
	}
	candidateNames := map[string]struct{}{}
	outcome := "unchanged"
	definitions := map[string]metrics.Definition{}
	for _, definition := range metrics.Definitions() {
		definitions[definition.Name] = definition
	}
	for _, current := range candidate.Metrics {
		if _, exists := candidateNames[current.Name]; exists {
			return Comparison{}, errors.New("candidate metric names must be unique")
		}
		candidateNames[current.Name] = struct{}{}
		prior, ok := baselineMetrics[current.Name]
		definition, defined := definitions[current.Name]
		status := compareMetric(prior, current, definition, ok && defined)
		statuses[current.Name] = status
		outcome = mergeOutcome(outcome, status)
		delete(baselineMetrics, current.Name)
	}
	for name := range baselineMetrics {
		statuses[name] = contracts.MetricStatusInsufficient
		outcome = mergeOutcome(outcome, contracts.MetricStatusInsufficient)
	}
	return Comparison{
		BaselineHash: baseline.RevisionHash, CandidateHash: candidate.RevisionHash,
		Statuses: statuses, Outcome: outcome,
		QualityGateState: qualityGateState(candidate.Guardrails),
	}, nil
}

func mergeOutcome(outcome string, status contracts.MetricStatus) string {
	switch status {
	case contracts.MetricStatusWorsened:
		return "regressed"
	case contracts.MetricStatusMixed, contracts.MetricStatusInsufficient:
		if outcome != "regressed" {
			return "mixed"
		}
	case contracts.MetricStatusImproved:
		if outcome == "unchanged" {
			return "improved"
		}
	}
	return outcome
}

func qualityGateState(guardrails []contracts.GuardrailResult) string {
	if len(guardrails) == 0 {
		return "unknown"
	}
	state := "pass"
	for _, guardrail := range guardrails {
		if guardrail.State == "fail" {
			return "fail"
		}
		if guardrail.State != "pass" {
			state = "unknown"
		}
	}
	return state
}

func compareMetric(prior, current contracts.MetricResult, definition metrics.Definition,
	found bool,
) contracts.MetricStatus {
	if !found || !finiteMetricValue(prior.NativeValue) || !finiteMetricValue(current.NativeValue) ||
		prior.Version != current.Version ||
		prior.Coverage != contracts.CoverageStateComplete ||
		current.Coverage != contracts.CoverageStateComplete {
		return contracts.MetricStatusInsufficient
	}
	if *prior.NativeValue == *current.NativeValue {
		return contracts.MetricStatusFlat
	}
	if definition.Polarity == metrics.PolarityContextual {
		return contracts.MetricStatusMixed
	}
	improved := *current.NativeValue < *prior.NativeValue
	if definition.Polarity == metrics.PolarityHigherBetter ||
		(definition.Polarity == metrics.PolarityGuardrail &&
			current.Name != "tool_error_rate") {
		improved = *current.NativeValue > *prior.NativeValue
	}
	if improved {
		return contracts.MetricStatusImproved
	}
	return contracts.MetricStatusWorsened
}

func finiteMetricValue(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0)
}
