// Package evidence defines content-free normalized collector records.
package evidence

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const SchemaVersion = "1.0.0"

var ErrSensitive = errors.New("sensitive evidence rejected")

type Coverage string

const (
	CoverageComplete Coverage = "complete"
	CoveragePartial  Coverage = "partial"
	CoverageMissing  Coverage = "missing"
	CoverageUnknown  Coverage = "unknown"
)

type Record struct {
	ID                 string
	SourceKind         string
	SourceVersion      string
	Sequence           uint64
	EventAt            *time.Time
	IngestedAt         time.Time
	ObservedAt         time.Time
	MonotonicNanos     int64
	Classification     string
	Coverage           Coverage
	CoverageReason     string
	ProductSurface     string
	ContentSHA256      string
	RedactedFields     []string
	Attributes         map[string]string
	LateRevision       bool
	OrderingUncertain  bool
	GovernanceOverhead bool
}

// OpaqueID creates a local keyed identifier after classification/redaction.
func OpaqueID(key []byte, parts ...string) (string, error) {
	if len(key) < 32 {
		return "", errors.New("identity key must be at least 32 bytes")
	}
	hash := hmac.New(sha256.New, key)
	for _, part := range parts {
		hash.Write([]byte{0})
		hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func Validate(record Record) error {
	if record.ID == "" || record.SourceKind == "" || record.IngestedAt.IsZero() || record.ObservedAt.IsZero() {
		return errors.New("incomplete evidence metadata")
	}
	if record.Attributes == nil || record.RedactedFields == nil {
		return errors.New("evidence collections must be initialized")
	}
	for key, value := range record.Attributes {
		if sensitiveKey(key) || secretLike(value) {
			return ErrSensitive
		}
	}
	return nil
}

var allowedAttributes = map[string]map[string]bool{
	"configuration":      {"execution_policy": true, "host_permission": true, "max_input_bytes": true, "timeout": true, "format": true, "activity_class": true},
	"git":                {"repository_alias": true, "worktree_alias": true, "branch_alias": true, "head_alias": true, "activity_class": true},
	"github":             {"event_kind": true, "state": true, "repository_alias": true, "pull_request_alias": true, "check_alias": true, "activity_class": true},
	"codex_jsonl":        {"phase": true, "response_lineage": true, "model_variant": true, "reasoning_effort": true, "reasoning_mode": true, "effective_context": true, "cache_mode": true, "cache_ttl": true, "image_detail": true, "safeguard_outcome": true, "safety_identifier_presence": true, "activity_class": true},
	"codex_state_sqlite": {"thread_alias": true, "state": true, "model_variant": true, "reasoning_effort": true, "activity_class": true},
	"rollout_summary":    {"trajectory_alias": true, "compaction_state": true, "delegation_parent": true, "delegation_depth": true, "context_mode": true, "activity_class": true},
	"process":            {"purpose": true, "state": true, "activity_class": true},
}

// Sanitize applies a strict source schema; unknown/private fields are omitted.
func Sanitize(sourceKind string, identityKey []byte, attributes map[string]string) (map[string]string, []string, error) {
	allowed := allowedAttributes[sourceKind]
	clean := make(map[string]string, len(attributes))
	redacted := []string{}
	for field, value := range attributes {
		if !allowed[field] || sensitiveKey(field) {
			redacted = append(redacted, field)
			continue
		}
		if secretLike(value) {
			return nil, nil, ErrSensitive
		}
		if strings.HasSuffix(field, "_alias") || strings.Contains(field, "lineage") || strings.HasSuffix(field, "_parent") {
			if strings.TrimSpace(value) == "" {
				clean[field] = ""
				continue
			}
			opaque, err := OpaqueID(identityKey, sourceKind, field, value)
			if err != nil {
				return nil, nil, err
			}
			clean[field] = opaque
			continue
		}
		clean[field] = value
	}
	return clean, redacted, nil
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, fragment := range []string{
		"prompt", "response_body", "reasoning", "payload", "program_body",
		"database_row", "token_value", "secret", "username", "host_path",
		"safety_identifier",
		"authorization", "cookie", "cwd", "path", "content", "text", "user",
	} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func secretLike(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "-----begin private key-----") ||
		strings.HasPrefix(lower, "ghp_") || strings.HasPrefix(lower, "sk-") ||
		strings.Contains(lower, "op://") || strings.HasPrefix(lower, "github_pat_") ||
		strings.HasPrefix(lower, "eyj")
}
