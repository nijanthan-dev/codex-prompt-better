// Package status builds bounded, sanitized collector status.
package status

import (
	"sort"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
)

const MaxErrors = 20

type Source struct {
	Kind              string                       `json:"kind"`
	Enabled           bool                         `json:"enabled"`
	Supported         bool                         `json:"supported"`
	Cursor            string                       `json:"cursor"`
	FreshAt           *time.Time                   `json:"fresh_at"`
	EvidenceCount     int                          `json:"evidence_count"`
	Coverage          evidence.Coverage            `json:"coverage"`
	CoverageReason    string                       `json:"coverage_reason,omitempty"`
	ConflictCount     int                          `json:"conflict_count"`
	OrderingUncertain bool                         `json:"ordering_uncertain"`
	LateRevisionCount int                          `json:"late_revision_count"`
	GovernanceCount   int                          `json:"governance_overhead_count"`
	FieldCoverage     map[string]evidence.Coverage `json:"field_coverage"`
	Errors            []string                     `json:"errors"`
}

type Report struct {
	AsOf      time.Time `json:"as_of"`
	Sources   []Source  `json:"sources"`
	HostCalls int       `json:"host_calls"`
	LeafCalls int       `json:"leaf_calls"`
}

func Build(asOf time.Time, sources []Source, hostCalls, leafCalls int) Report {
	copySources := append([]Source{}, sources...)
	for index := range copySources {
		copySources[index].FieldCoverage = normalizeFieldCoverage(copySources[index].FieldCoverage)
		copySources[index].Errors = sanitize(copySources[index].Errors)
		if !copySources[index].Enabled {
			copySources[index].Coverage = evidence.CoverageMissing
			copySources[index].CoverageReason = "disabled"
		} else if !copySources[index].Supported {
			copySources[index].Coverage = evidence.CoverageUnknown
			copySources[index].CoverageReason = "unsupported"
		}
	}
	sort.Slice(copySources, func(i, j int) bool { return copySources[i].Kind < copySources[j].Kind })
	return Report{AsOf: asOf.UTC(), Sources: copySources, HostCalls: hostCalls, LeafCalls: leafCalls}
}

func normalizeFieldCoverage(values map[string]evidence.Coverage) map[string]evidence.Coverage {
	result := map[string]evidence.Coverage{}
	for _, family := range []string{
		"identity", "response", "items_phases", "tool_state", "lifecycle",
		"usage_cache", "governance_overhead",
	} {
		coverage := evidence.CoverageUnknown
		if value := values[family]; value == evidence.CoverageComplete ||
			value == evidence.CoveragePartial || value == evidence.CoverageMissing ||
			value == evidence.CoverageUnknown {
			coverage = value
		}
		result[family] = coverage
	}
	return result
}

func sanitize(errors []string) []string {
	if len(errors) > MaxErrors {
		errors = errors[:MaxErrors]
	}
	result := make([]string, 0, len(errors))
	allowed := map[string]bool{"malformed": true, "truncated": true, "sensitive": true, "adapter_failure": true, "offline": true}
	for _, code := range errors {
		if allowed[code] {
			result = append(result, code)
		} else {
			result = append(result, "adapter_failure")
		}
	}
	return result
}
