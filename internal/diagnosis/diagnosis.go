// Package diagnosis evaluates deterministic governance detectors.
package diagnosis

import (
	"sort"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const Version = "detector-v1"

type Observation struct {
	Code              string
	Cause             string
	ExceptionCheck    string
	Classification    string
	Coverage          contracts.CoverageState
	PositiveCount     int
	Denominator       int
	MinimumSample     int
	MinimumRate       float64
	EvidenceRefs      []string
	Counterevidence   []string
	RequiredWork      bool
	ConfounderMatched bool
}

func Evaluate(observations []Observation) []contracts.AuditFinding {
	findings := []contracts.AuditFinding{}
	for _, observation := range observations {
		finding, ok := evaluate(observation)
		if ok {
			findings = append(findings, finding)
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Code < findings[j].Code })
	return findings
}

func evaluate(observation Observation) (contracts.AuditFinding, bool) {
	if observation.PositiveCount <= 0 {
		return contracts.AuditFinding{}, false
	}
	if observation.Denominator > 0 && observation.MinimumRate > 0 &&
		float64(observation.PositiveCount)/float64(observation.Denominator) <= observation.MinimumRate {
		return contracts.AuditFinding{}, false
	}
	classification := observation.Classification
	confidence := "high"
	if observation.RequiredWork {
		classification = "necessary"
		confidence = "high"
	} else if observation.Coverage != contracts.CoverageStateComplete ||
		observation.Denominator < observation.MinimumSample {
		classification = "uncertain"
		confidence = "unknown"
	} else if !observation.ConfounderMatched || len(observation.Counterevidence) > 0 {
		classification = "mixed"
		confidence = "medium"
	}
	if classification == "" {
		classification = "uncertain"
	}
	return contracts.AuditFinding{
		Code: observation.Code, Detector: observation.Code,
		DetectorVersion: Version, Cause: observation.Cause,
		ExceptionCheck: observation.ExceptionCheck,
		Classification: classification, Confidence: confidence,
		EvidenceRefs:    bounded(observation.EvidenceRefs, 100),
		Counterevidence: bounded(observation.Counterevidence, 20),
	}, true
}

func bounded(values []string, limit int) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}
