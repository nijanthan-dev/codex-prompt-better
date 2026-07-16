// Package recommendation applies deterministic preview-only governance policies.
package recommendation

import (
	"sort"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const Version = "recommendation-v1"

type Feedback struct {
	State            string
	CooldownUntil    time.Time
	EvidenceRevision string
}

type Context struct {
	AsOf                     time.Time
	ExistingRuleCoverage     map[string]bool
	Feedback                 map[string]Feedback
	MaterialEvidenceRevision string
	EvidenceRevisions        map[string]string
	QualityGuardrailsPass    bool
}

func Generate(findings []contracts.AuditFinding, context Context) []contracts.AuditRecommendation {
	result := []contracts.AuditRecommendation{}
	sorted := append([]contracts.AuditFinding{}, findings...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Code < sorted[j].Code })
	for _, finding := range sorted {
		if len(result) == 5 {
			break
		}
		if finding.Classification == "necessary" || finding.Classification == "uncertain" ||
			context.ExistingRuleCoverage[finding.Code] || suppressed(finding.Code, context) ||
			!context.QualityGuardrailsPass {
			continue
		}
		if recommendation, ok := candidate(finding); ok {
			result = append(result, recommendation)
		}
	}
	if len(result) == 0 {
		return []contracts.AuditRecommendation{noAction()}
	}
	return result
}

func suppressed(code string, context Context) bool {
	feedback, ok := context.Feedback[code]
	if !ok || feedback.State != "dismissed" {
		return false
	}
	revision := context.MaterialEvidenceRevision
	if context.EvidenceRevisions[code] != "" {
		revision = context.EvidenceRevisions[code]
	}
	newEvidence := revision != "" && revision != feedback.EvidenceRevision
	return context.AsOf.Before(feedback.CooldownUntil) && !newEvidence
}

func candidate(finding contracts.AuditFinding) (contracts.AuditRecommendation, bool) {
	switch finding.Code {
	case "scope_attribution_gap":
		return preview(finding, "restore_scope_attribution", "collector_mapping",
			"Restore explicit normalized task attribution at the adapter boundary.",
			"higher scope attribution coverage without identity disclosure",
			"Replay the same synthetic attribution fixtures and require observed keyed task aliases.",
			[]string{"privacy suppression may keep small cohorts unknown"}), true
	case "checkpoint_gap":
		return preview(finding, "complete_checkpoint_contract", "validation_cadence",
			"Record completion, next action, and remaining gate counts at required checkpoints.",
			"higher checkpoint completeness without extra checkpoint volume",
			"Replay matched checkpoint fixtures and require all structured fields.",
			[]string{"unnecessary checkpoints add governance overhead"}), true
	case "passive_polling":
		return preview(finding, "wait_on_state_change", "validation_cadence",
			"Replace unchanged polling with the configured wait or user-requested cadence.",
			"lower passive waits with unchanged completion and coverage",
			"Replay a matched window and require fewer unchanged polls with no missed state transition.",
			[]string{"external state may require polling"}), true
	case "repeated_unchanged_call":
		return preview(finding, "reuse_unchanged_evidence", "tool_output_budget",
			"Reuse the prior bounded result until relevant state changes.",
			"lower repeated calls with equal evidence coverage",
			"Replay a matched window and require fewer duplicate calls with equal evidence coverage.",
			[]string{"stale evidence after an unobserved mutation"}), true
	case "missing_validation":
		return preview(finding, "validate_after_mutation", "validation_cadence",
			"Run the smallest configured validation after each observable mutation.",
			"higher validation presence without weakening final gates",
			"Replay the next matched window and require complete mutation validation coverage.",
			[]string{"validation cost may rise for mutation-heavy work"}), true
	case "privacy_redaction_gap":
		return preview(finding, "restore_redaction_coverage", "project_instruction",
			"Restore configured redaction before accepting additional evidence.",
			"complete privacy redaction coverage",
			"Re-run redaction fixtures and require no accepted evidence with unknown redaction state.",
			[]string{"suppression may reduce metric coverage"}), true
	default:
		return contracts.AuditRecommendation{}, false
	}
}

func preview(finding contracts.AuditFinding, code, target, action, movement,
	verification string, risks []string,
) contracts.AuditRecommendation {
	return contracts.AuditRecommendation{
		Code: code, PolicyVersion: Version, LifecycleState: "proposed",
		TargetSurface: target, Action: action, ExpectedMovement: movement,
		ProtectedGuardrails: []string{
			"completion", "permissions", "privacy", "quality", "safety", "validation",
		},
		ApprovalRequired: true, Verification: verification, Risks: risks,
		EvidenceRefs: append([]string{}, finding.EvidenceRefs...),
	}
}

func noAction() contracts.AuditRecommendation {
	return contracts.AuditRecommendation{
		Code: "no_action", PolicyVersion: Version, LifecycleState: "proposed",
		TargetSurface: "no_action", Action: "Keep the current workflow.",
		ExpectedMovement:    "none",
		ProtectedGuardrails: []string{"all_configured_quality_and_safety_gates"},
		Verification:        "Re-audit after new comparable evidence is available.",
		Risks:               []string{}, EvidenceRefs: []string{},
	}
}
