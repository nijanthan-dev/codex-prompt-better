// Package adapters provides bounded, read-only evidence source adapters.
package adapters

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
)

const DefaultMaxRecordBytes = 256 * 1024

var (
	ErrDisabled    = errors.New("source disabled")
	ErrUnsupported = errors.New("source unsupported")
	ErrMalformed   = errors.New("source record malformed")
	ErrTruncated   = errors.New("source record truncated")
)

type Request struct {
	Cursor           uint64
	CursorGeneration string
	CursorDigest     string
	Limit            int
	SourceVersion    string
	ObservedAt       time.Time
	MonotonicNanos   int64
}

type Result struct {
	Records      []evidence.Record
	NextCursor   uint64
	Generation   string
	CursorDigest string
	Coverage     evidence.Coverage
	Reason       string
}

type Adapter interface {
	Kind() string
	Collect(context.Context, Request) (Result, error)
}

type JSONL struct {
	kind           string
	data           []byte
	key            []byte
	identity       string
	enabled        bool
	supported      bool
	maxRecordBytes int
}

type parsedRecord struct {
	version    string
	canonical  []byte
	observedAt time.Time
	eventAt    *time.Time
	attributes map[string]string
	redacted   []string
}

func NewJSONLWithIdentity(kind, identity string, reader io.Reader, key []byte, enabled, supported bool) *JSONL {
	data := []byte{}
	if reader != nil {
		data, _ = io.ReadAll(io.LimitReader(reader, 64*1024*1024+1))
	}
	return &JSONL{
		kind: kind, data: data, key: key, identity: identity, enabled: enabled, supported: supported,
		maxRecordBytes: DefaultMaxRecordBytes,
	}
}

func (a *JSONL) Kind() string { return a.kind }

func (a *JSONL) Collect(ctx context.Context, request Request) (Result, error) {
	if !a.enabled {
		return unavailable(evidence.CoverageMissing, "disabled"), ErrDisabled
	}
	if !a.supported {
		return unavailable(evidence.CoverageUnknown, "unsupported"), ErrUnsupported
	}
	if len(a.data) == 0 || request.Limit < 1 {
		return unavailable(evidence.CoverageMissing, "missing"), nil
	}
	result := Result{Records: []evidence.Record{}, NextCursor: request.Cursor, Coverage: evidence.CoverageComplete}
	parsed := []parsedRecord{}
	scanner := bufio.NewScanner(bytes.NewReader(a.data))
	scanner.Buffer(make([]byte, 4096), a.maxRecordBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var source struct {
			Version    string            `json:"version"`
			ObservedAt string            `json:"observed_at"`
			Attributes map[string]string `json:"attributes"`
		}
		line := append([]byte(nil), scanner.Bytes()...)
		if err := json.Unmarshal(line, &source); err != nil {
			return partial(result, "malformed"), ErrMalformed
		}
		observedAt := request.ObservedAt
		var eventAt *time.Time
		if source.ObservedAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, source.ObservedAt)
			if err != nil {
				return partial(result, "malformed_time"), ErrMalformed
			}
			observedAt = parsed
			eventAt = &parsed
		}
		clean, fields, err := evidence.Sanitize(a.kind, a.key, source.Attributes)
		if err != nil {
			return partial(result, "sensitive"), err
		}
		canonical, _ := json.Marshal(struct {
			Version    string            `json:"version"`
			ObservedAt string            `json:"observed_at"`
			Attributes map[string]string `json:"attributes"`
		}{Version: source.Version, ObservedAt: source.ObservedAt, Attributes: clean})
		parsed = append(parsed, parsedRecord{version: source.Version, canonical: canonical, observedAt: observedAt, eventAt: eventAt, attributes: clean, redacted: fields})
	}
	if err := scanner.Err(); err != nil {
		return partial(result, "truncated_or_oversized"), ErrTruncated
	}
	generationMAC := hmac.New(sha256.New, a.key)
	generationMAC.Write([]byte(a.kind))
	generationMAC.Write([]byte{0})
	generationMAC.Write([]byte(a.identity))
	baseGeneration := hex.EncodeToString(generationMAC.Sum(nil))
	result.Generation = baseGeneration
	prefixDigest := digestPrefix(a.key, parsed, request.Cursor)
	prefixMatches := request.CursorDigest == "" || hmac.Equal([]byte(request.CursorDigest), []byte(prefixDigest))
	rotated := !prefixMatches
	if request.CursorGeneration != "" && prefixMatches && request.Cursor <= uint64(len(parsed)) {
		result.Generation = request.CursorGeneration
	}
	if request.Cursor > uint64(len(parsed)) {
		rotated = true
	}
	if rotated {
		result.Coverage = evidence.CoveragePartial
		result.Reason = "source_replaced"
		request.Cursor = 0
		revisionMAC := hmac.New(sha256.New, a.key)
		revisionMAC.Write([]byte(baseGeneration))
		revisionMAC.Write([]byte{0})
		revisionMAC.Write([]byte(digestPrefix(a.key, parsed, uint64(len(parsed)))))
		result.Generation = hex.EncodeToString(revisionMAC.Sum(nil))
	}
	for index, item := range parsed {
		sequence := uint64(index + 1)
		if sequence <= request.Cursor {
			continue
		}
		digestMAC := hmac.New(sha256.New, a.key)
		digestMAC.Write(item.canonical)
		digest := digestMAC.Sum(nil)
		id, err := evidence.OpaqueID(a.key, a.kind, result.Generation, strconv.FormatUint(sequence, 10))
		if err != nil {
			return result, err
		}
		record := evidence.Record{
			ID: id, SourceKind: a.kind, SourceVersion: item.version,
			Sequence: sequence, EventAt: item.eventAt, IngestedAt: request.ObservedAt,
			ObservedAt: item.observedAt, MonotonicNanos: request.MonotonicNanos,
			Classification: "internal", Coverage: evidence.CoverageComplete,
			ProductSurface: surface(a.kind), ContentSHA256: hex.EncodeToString(digest),
			RedactedFields: item.redacted, Attributes: item.attributes, LateRevision: rotated,
			GovernanceOverhead: item.attributes["activity_class"] == "governance_overhead",
		}
		if record.GovernanceOverhead {
			record.Attributes["collection_trigger"] = "suppressed"
		}
		if err := evidence.Validate(record); err != nil {
			return partial(result, "invalid"), err
		}
		result.Records = append(result.Records, record)
		result.NextCursor = sequence
		if len(result.Records) == request.Limit {
			break
		}
	}
	result.CursorDigest = digestPrefix(a.key, parsed, result.NextCursor)
	return result, nil
}

func digestPrefix(key []byte, records []parsedRecord, cursor uint64) string {
	if cursor > uint64(len(records)) {
		cursor = uint64(len(records))
	}
	digest := hmac.New(sha256.New, key)
	for _, record := range records[:cursor] {
		digest.Write(record.canonical)
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func unavailable(coverage evidence.Coverage, reason string) Result {
	return Result{Records: []evidence.Record{}, Coverage: coverage, Reason: reason}
}

func partial(result Result, reason string) Result {
	result.Coverage = evidence.CoveragePartial
	result.Reason = reason
	return result
}

func surface(kind string) string {
	if kind == "github" {
		return "local"
	}
	if kind == "codex_jsonl" || kind == "codex_state_sqlite" || kind == "rollout_summary" {
		return "codex_subscription"
	}
	return "local"
}
