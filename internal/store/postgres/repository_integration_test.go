//go:build integration

package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nijanthan-dev/codex-prompt-better/internal/identity"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prepareDatabase(t, ctx, dsn)
	repo, err := OpenRepository(ctx, dsn, DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	t.Run("doctor reports sanitized readiness", func(t *testing.T) {
		result := repo.Doctor(ctx)
		if !result.Ready || result.SchemaVersion != latestSchemaVersion || result.ServerVersion < 160000 {
			t.Fatalf("unexpected doctor result: %+v", result)
		}
		if len(result.Problems) != 0 || result.Role == "" {
			t.Fatalf("unexpected doctor diagnostics: %+v", result)
		}
	})

	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	project := Project{
		ID: "10000000-0000-0000-0000-000000000001", VersionID: "10000000-0000-0000-0000-000000000011",
		CreatedAt: base, EffectiveAt: base, Classification: "internal", LifecycleState: "active",
	}
	t.Run("project SCD2 is idempotent and historical", func(t *testing.T) {
		if err := repo.PutProject(ctx, project); err != nil {
			t.Fatal(err)
		}
		identical := project
		identical.VersionID = "10000000-0000-0000-0000-000000000012"
		identical.EffectiveAt = base.Add(time.Hour)
		if err := repo.PutProject(ctx, identical); err != nil {
			t.Fatal(err)
		}
		changed := project
		changed.VersionID = "10000000-0000-0000-0000-000000000013"
		changed.Classification = "restricted"
		changed.EffectiveAt = base.Add(2 * time.Hour)
		if err := repo.PutProject(ctx, changed); err != nil {
			t.Fatal(err)
		}
		var versionCount int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.project_versions
            WHERE project_id = $1`, project.ID).Scan(&versionCount); err != nil {
			t.Fatal(err)
		}
		if versionCount != 2 {
			t.Fatalf("project versions = %d, want 2", versionCount)
		}
		var historical, current string
		if err := repo.pool.QueryRow(ctx, `SELECT classification FROM prompt_better.project_versions
            WHERE project_id=$1 AND valid_from <= $2 AND (valid_to > $2 OR valid_to IS NULL)`,
			project.ID, base.Add(time.Hour)).Scan(&historical); err != nil {
			t.Fatal(err)
		}
		if err := repo.pool.QueryRow(ctx, `SELECT classification FROM prompt_better.current_project_dimensions
            WHERE project_id=$1`, project.ID).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if historical != "internal" || current != "restricted" {
			t.Fatalf("historical=%q current=%q", historical, current)
		}
		asOf, err := repo.ProjectAsOf(ctx, project.ID, base.Add(time.Hour))
		if err != nil || asOf.Classification != "internal" || asOf.ValidTo == nil {
			t.Fatalf("historical query=%+v error=%v", asOf, err)
		}
		currentVersion, err := repo.CurrentProject(ctx, project.ID)
		if err != nil || currentVersion.Classification != "restricted" || currentVersion.ValidTo != nil {
			t.Fatalf("current query=%+v error=%v", currentVersion, err)
		}
		late := changed
		late.VersionID = "10000000-0000-0000-0000-000000000014"
		late.Classification = "confidential"
		late.EffectiveAt = base.Add(90 * time.Minute)
		if err := repo.PutProject(ctx, late); !errors.Is(err, ErrInvalidEffectiveTime) {
			t.Fatalf("late observation error=%v", err)
		}
		var currentCount, overlapCount, zeroLengthCount int
		if err := repo.pool.QueryRow(ctx, `SELECT
            count(*) FILTER (WHERE valid_to IS NULL),
            count(*) FILTER (WHERE valid_to IS NOT NULL AND valid_to <= valid_from)
            FROM prompt_better.project_versions WHERE project_id=$1`, project.ID).
			Scan(&currentCount, &zeroLengthCount); err != nil {
			t.Fatal(err)
		}
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.project_versions a
            JOIN prompt_better.project_versions b ON a.project_id=b.project_id
             AND a.project_version_id < b.project_version_id
             AND tstzrange(a.valid_from, a.valid_to, '[)') && tstzrange(b.valid_from, b.valid_to, '[)')
            WHERE a.project_id=$1`, project.ID).Scan(&overlapCount); err != nil {
			t.Fatal(err)
		}
		if currentCount != 1 || overlapCount != 0 || zeroLengthCount != 0 {
			t.Fatalf("SCD2 invariant current=%d overlap=%d zero=%d", currentCount, overlapCount, zeroLengthCount)
		}
	})

	t.Run("concurrent project changes preserve one current version", func(t *testing.T) {
		concurrent := Project{
			ID: "11000000-0000-0000-0000-000000000001", VersionID: "11000000-0000-0000-0000-000000000011",
			CreatedAt: base, EffectiveAt: base, Classification: "internal", LifecycleState: "active",
		}
		if err := repo.PutProject(ctx, concurrent); err != nil {
			t.Fatal(err)
		}
		changes := []Project{concurrent, concurrent}
		changes[0].VersionID = "11000000-0000-0000-0000-000000000012"
		changes[0].Classification = "confidential"
		changes[1].VersionID = "11000000-0000-0000-0000-000000000013"
		changes[1].Classification = "restricted"
		for index := range changes {
			changes[index].EffectiveAt = base.Add(time.Hour)
		}
		errorsSeen := make(chan error, len(changes))
		var group sync.WaitGroup
		for _, change := range changes {
			group.Add(1)
			go func() {
				defer group.Done()
				errorsSeen <- repo.PutProject(ctx, change)
			}()
		}
		group.Wait()
		close(errorsSeen)
		successes := 0
		for err := range errorsSeen {
			if err == nil {
				successes++
				continue
			}
			if !errors.Is(err, ErrInvalidEffectiveTime) {
				t.Fatalf("unexpected concurrent error: %v", err)
			}
		}
		var currentCount, overlapCount int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE valid_to IS NULL)
            FROM prompt_better.project_versions WHERE project_id=$1`, concurrent.ID).Scan(&currentCount); err != nil {
			t.Fatal(err)
		}
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.project_versions a
            JOIN prompt_better.project_versions b ON a.project_id=b.project_id
             AND a.project_version_id < b.project_version_id
             AND tstzrange(a.valid_from, a.valid_to, '[)') && tstzrange(b.valid_from, b.valid_to, '[)')
            WHERE a.project_id=$1`, concurrent.ID).Scan(&overlapCount); err != nil {
			t.Fatal(err)
		}
		if successes != 1 || currentCount != 1 || overlapCount != 0 {
			t.Fatalf("concurrent SCD2 successes=%d current=%d overlap=%d", successes, currentCount, overlapCount)
		}
	})

	source := Source{
		ID: "20000000-0000-0000-0000-000000000001", VersionID: "20000000-0000-0000-0000-000000000011",
		Kind: "synthetic_jsonl", ProductSurface: "local", CoverageState: "complete",
		Enabled: true, CreatedAt: base, EffectiveAt: base,
	}
	t.Run("source, cursor, evidence, and coverage", func(t *testing.T) {
		if err := repo.PutSource(ctx, source); err != nil {
			t.Fatal(err)
		}
		identical := source
		identical.VersionID = "20000000-0000-0000-0000-000000000012"
		identical.EffectiveAt = base.Add(time.Hour)
		if err := repo.PutSource(ctx, identical); err != nil {
			t.Fatal(err)
		}
		changed := source
		changed.VersionID = "20000000-0000-0000-0000-000000000013"
		changed.CoverageState = "partial"
		changed.EffectiveAt = base.Add(2 * time.Hour)
		if err := repo.PutSource(ctx, changed); err != nil {
			t.Fatal(err)
		}
		historicalSource, err := repo.SourceAsOf(ctx, source.ID, base.Add(time.Hour))
		if err != nil || historicalSource.CoverageState != "complete" {
			t.Fatalf("historical source=%+v error=%v", historicalSource, err)
		}
		currentSource, err := repo.CurrentSource(ctx, source.ID)
		if err != nil || currentSource.CoverageState != "partial" || currentSource.ValidTo != nil {
			t.Fatalf("current source=%+v error=%v", currentSource, err)
		}
		lateSource := changed
		lateSource.VersionID = "20000000-0000-0000-0000-000000000014"
		lateSource.CoverageState = "unknown"
		lateSource.EffectiveAt = base.Add(90 * time.Minute)
		if err := repo.PutSource(ctx, lateSource); !errors.Is(err, ErrInvalidEffectiveTime) {
			t.Fatalf("late source error=%v", err)
		}
		var sourceCurrent, sourceOverlap, sourceZero int
		if err := repo.pool.QueryRow(ctx, `SELECT
            count(*) FILTER (WHERE valid_to IS NULL),
            count(*) FILTER (WHERE valid_to IS NOT NULL AND valid_to <= valid_from)
            FROM prompt_better.source_versions WHERE source_id=$1`, source.ID).
			Scan(&sourceCurrent, &sourceZero); err != nil {
			t.Fatal(err)
		}
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.source_versions a
            JOIN prompt_better.source_versions b ON a.source_id=b.source_id
             AND a.source_version_id < b.source_version_id
             AND tstzrange(a.valid_from, a.valid_to, '[)') && tstzrange(b.valid_from, b.valid_to, '[)')
            WHERE a.source_id=$1`, source.ID).Scan(&sourceOverlap); err != nil {
			t.Fatal(err)
		}
		if sourceCurrent != 1 || sourceOverlap != 0 || sourceZero != 0 {
			t.Fatalf("source SCD2 current=%d overlap=%d zero=%d", sourceCurrent, sourceOverlap, sourceZero)
		}
		if err := repo.AdvanceSequenceCursor(ctx, "30000000-0000-0000-0000-000000000001", source.ID, 1, base); err != nil {
			t.Fatal(err)
		}
		if err := repo.AdvanceSequenceCursor(ctx, "30000000-0000-0000-0000-000000000002", source.ID, 1, base); !errors.Is(err, ErrStaleCursor) {
			t.Fatalf("duplicate cursor error = %v", err)
		}
		coverage, err := repo.GetProjectCoverage(ctx, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if coverage.CoverageRatio != nil || coverage.DenominatorState != "missing" {
			t.Fatalf("unexpected empty coverage: %+v", coverage)
		}
		hash := sha256.Sum256([]byte("synthetic evidence"))
		alias := "30000000-0000-0000-0000-000000000011"
		evidence := Evidence{
			ID: "30000000-0000-0000-0000-000000000021", SourceID: source.ID,
			ProjectID: &project.ID, ExternalAliasID: &alias, SchemaVersion: "1.0.0",
			ContentHash: hash[:], ContentLength: 18, Classification: "internal",
			RedactionState: "not_needed", CoverageState: "complete",
			Provenance: "runtime_observed", ProductSurface: "local", ObservedAt: base,
		}
		if err := repo.PutEvidence(ctx, evidence); err != nil {
			t.Fatal(err)
		}
		if err := repo.PutEvidence(ctx, evidence); err != nil {
			t.Fatal(err)
		}
		conflict := evidence
		other := sha256.Sum256([]byte("different synthetic evidence"))
		conflict.ContentHash = other[:]
		if err := repo.PutEvidence(ctx, conflict); !errors.Is(err, ErrSourceConflict) {
			t.Fatalf("conflict error = %v", err)
		}
		coverage, err = repo.GetProjectCoverage(ctx, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if coverage.CoverageRatio == nil || *coverage.CoverageRatio != 1 {
			t.Fatalf("unexpected observed coverage: %+v", coverage)
		}
	})

	t.Run("concurrent source changes preserve one current version", func(t *testing.T) {
		concurrent := Source{
			ID: "22000000-0000-0000-0000-000000000001", VersionID: "22000000-0000-0000-0000-000000000011",
			Kind: "synthetic_jsonl", ProductSurface: "local", CoverageState: "complete",
			Enabled: true, CreatedAt: base, EffectiveAt: base,
		}
		if err := repo.PutSource(ctx, concurrent); err != nil {
			t.Fatal(err)
		}
		changes := []Source{concurrent, concurrent}
		changes[0].VersionID = "22000000-0000-0000-0000-000000000012"
		changes[0].CoverageState = "partial"
		changes[1].VersionID = "22000000-0000-0000-0000-000000000013"
		changes[1].CoverageState = "unknown"
		for index := range changes {
			changes[index].EffectiveAt = base.Add(time.Hour)
		}
		errorsSeen := make(chan error, len(changes))
		var group sync.WaitGroup
		for _, change := range changes {
			group.Add(1)
			go func() {
				defer group.Done()
				errorsSeen <- repo.PutSource(ctx, change)
			}()
		}
		group.Wait()
		close(errorsSeen)
		successes := 0
		for err := range errorsSeen {
			if err == nil {
				successes++
				continue
			}
			if !errors.Is(err, ErrInvalidEffectiveTime) {
				t.Fatalf("unexpected concurrent source error: %v", err)
			}
		}
		var currentCount, overlapCount int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE valid_to IS NULL)
            FROM prompt_better.source_versions WHERE source_id=$1`, concurrent.ID).Scan(&currentCount); err != nil {
			t.Fatal(err)
		}
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.source_versions a
            JOIN prompt_better.source_versions b ON a.source_id=b.source_id
             AND a.source_version_id < b.source_version_id
             AND tstzrange(a.valid_from, a.valid_to, '[)') && tstzrange(b.valid_from, b.valid_to, '[)')
            WHERE a.source_id=$1`, concurrent.ID).Scan(&overlapCount); err != nil {
			t.Fatal(err)
		}
		if successes != 1 || currentCount != 1 || overlapCount != 0 {
			t.Fatalf("concurrent source successes=%d current=%d overlap=%d", successes, currentCount, overlapCount)
		}
	})

	t.Run("collection evidence and cursor commit atomically", func(t *testing.T) {
		atomicSource := Source{
			ID: "21000000-0000-0000-0000-000000000001", VersionID: "21000000-0000-0000-0000-000000000011",
			Kind: "synthetic_jsonl", ProductSurface: "local", CoverageState: "complete",
			Enabled: true, CreatedAt: base, EffectiveAt: base,
		}
		if err := repo.PutSource(ctx, atomicSource); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte("atomic synthetic evidence"))
		alias := "31000000-0000-0000-0000-000000000011"
		sessionID := "31000000-0000-0000-0000-000000000031"
		turnOrdinal := int64(0)
		outputSize := int64(2048)
		usageValue := 100.0
		cacheValue := 20.0
		cacheTTL := int64(300)
		stateHash := sha256.Sum256([]byte("synthetic-state-epoch"))
		callHash := sha256.Sum256([]byte("synthetic-redacted-call"))
		item := Evidence{
			ID: "31000000-0000-0000-0000-000000000021", SourceID: atomicSource.ID,
			ProjectID: &project.ID, SessionID: &sessionID, ExternalAliasID: &alias, SchemaVersion: "1.0.0", ContentHash: hash[:],
			ContentLength: 25, Classification: "internal", RedactionState: "not_needed",
			CoverageState: "complete", Provenance: "runtime_observed",
			ProductSurface: "local", ObservedAt: base,
			Lineage: EvidenceLineage{
				SessionID: sessionID, TrajectoryID: "31000000-0000-0000-0000-000000000032",
				TaskID: "31000000-0000-0000-0000-000000000036",
				TurnID: "31000000-0000-0000-0000-000000000033", TurnOrdinal: &turnOrdinal,
				ResponseID: "31000000-0000-0000-0000-000000000034",
				ToolCallID: "31000000-0000-0000-0000-000000000035", ToolKind: "synthetic", CallPath: "direct",
			},
			Runtime: RuntimeObservation{
				Phase: "commentary", PhaseEvent: "completed", ResponseEvent: "completed",
				ModelVariant: "synthetic", ReasoningEffort: "medium",
				ToolOutcome: "success", ResultState: "complete",
				CanonicalCallHash: callHash[:],
				StateEpochID:      "31000000-0000-0000-0000-000000000037",
				StateEpochHash:    stateHash[:], MutationState: "unchanged",
				OutputModality: "text", OutputSizeBytes: &outputSize,
				UsageKind: "total_tokens", UsageValue: &usageValue, UsageUnit: "tokens",
				AccountingRegime: "native", CacheKind: "cached_input",
				CacheValue: &cacheValue, CacheMode: "ephemeral", CacheTTLSeconds: &cacheTTL,
				CheckpointEvent: "checkpointed", BoundaryEvent: "allowed",
				DelegationEvent: "none", StopEvent: "completed",
				CompactionEvent: "none",
			},
		}
		batch := CollectionBatch{
			BatchID: "synthetic-batch-one", CursorID: "31000000-0000-0000-0000-000000000001", SourceID: atomicSource.ID,
			Next: 1, Generation: "synthetic-generation", CursorDigest: "synthetic-digest", ObservedAt: base, Evidence: []Evidence{item},
		}
		fence, acquired, err := repo.AcquireCollectionLease(ctx, batch.CursorID, batch.SourceID, "integration", base, time.Hour)
		if err != nil || !acquired {
			t.Fatalf("lease acquired=%t error=%v", acquired, err)
		}
		batch.Fence = fence
		committed, err := repo.CommitCollection(ctx, batch)
		if err != nil || !committed {
			t.Fatalf("first commit=%t error=%v", committed, err)
		}
		var normalizedCount int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*)
			FROM prompt_better.evidence_artifacts e
			JOIN prompt_better.sessions s ON s.session_id=e.session_id
			JOIN prompt_better.trajectories tr ON tr.session_id=s.session_id
			JOIN prompt_better.turns t ON t.trajectory_id=tr.trajectory_id
			JOIN prompt_better.responses r ON r.turn_id=t.turn_id
			JOIN prompt_better.tool_calls c ON c.response_id=r.response_id
			JOIN prompt_better.tasks task ON task.task_id=t.task_id
			JOIN prompt_better.state_epochs epoch ON epoch.state_epoch_id=c.state_epoch_id
			JOIN prompt_better.items i ON i.response_id=r.response_id
			JOIN prompt_better.phases p ON p.response_id=r.response_id
			JOIN prompt_better.usage_observations u ON u.evidence_artifact_id=e.evidence_artifact_id
			JOIN prompt_better.cache_observations cache ON cache.evidence_artifact_id=e.evidence_artifact_id
			JOIN prompt_better.checkpoints checkpoint ON checkpoint.session_id=s.session_id
			JOIN prompt_better.boundaries boundary ON boundary.session_id=s.session_id
			JOIN prompt_better.delegation_events delegation ON delegation.trajectory_id=tr.trajectory_id
			JOIN prompt_better.compaction_events compaction ON compaction.trajectory_id=tr.trajectory_id
			JOIN prompt_better.stop_events stop ON stop.trajectory_id=tr.trajectory_id
			WHERE e.evidence_artifact_id=$1`, item.ID).Scan(&normalizedCount); err != nil || normalizedCount != 1 {
			t.Fatalf("normalized governance handoff count=%d error=%v", normalizedCount, err)
		}
		currentAudit, provenance, err := repo.AuditSession(ctx, sessionID, []string{atomicSource.Kind}, nil)
		if err != nil || currentAudit.Coverage != contracts.CoverageStateComplete || len(currentAudit.EvidenceRefs) != 1 || len(provenance) != 1 {
			t.Fatalf("current audit=%+v provenance=%v error=%v", currentAudit, provenance, err)
		}
		asOf := base.Add(time.Hour)
		historicalAudit, _, err := repo.AuditSession(ctx, sessionID, []string{atomicSource.Kind}, &asOf)
		if err != nil || historicalAudit.Coverage != contracts.CoverageStateComplete {
			t.Fatalf("historical audit=%+v error=%v", historicalAudit, err)
		}
		before := base.Add(-time.Nanosecond)
		boundaryAudit, _, err := repo.AuditSession(ctx, sessionID, []string{atomicSource.Kind}, &before)
		if err != nil || boundaryAudit.Coverage != contracts.CoverageStatePartial {
			t.Fatalf("boundary audit=%+v error=%v", boundaryAudit, err)
		}
		if _, _, err := repo.AuditSession(ctx, sessionID, []string{"other"}, nil); err == nil {
			t.Fatal("unconfigured source accepted")
		}
		committed, err = repo.CommitCollection(ctx, batch)
		if err != nil || committed {
			t.Fatalf("replay commit=%t error=%v", committed, err)
		}
		sameCursorConflict := batch
		otherAtSameCursor := sha256.Sum256([]byte("same cursor conflict"))
		sameCursorConflict.Evidence = append([]Evidence{}, item)
		sameCursorConflict.Evidence[0].ContentHash = otherAtSameCursor[:]
		if _, err := repo.CommitCollection(ctx, sameCursorConflict); !errors.Is(err, ErrSourceConflict) {
			t.Fatalf("same-cursor conflict error=%v", err)
		}
		cursor, err := repo.CollectionCursor(ctx, atomicSource.ID)
		if err != nil || cursor != 1 {
			t.Fatalf("cursor=%d error=%v", cursor, err)
		}
		conflict := batch
		conflict.Next = 2
		other := sha256.Sum256([]byte("conflicting atomic evidence"))
		conflict.Evidence = append([]Evidence{}, item)
		conflict.Evidence[0].ContentHash = other[:]
		if _, err := repo.CommitCollection(ctx, conflict); !errors.Is(err, ErrSourceConflict) {
			t.Fatalf("conflict error=%v", err)
		}
		cursor, err = repo.CollectionCursor(ctx, atomicSource.ID)
		if err != nil || cursor != 1 {
			t.Fatalf("failed commit advanced cursor=%d error=%v", cursor, err)
		}

		missingHash := sha256.Sum256([]byte("unlinked synthetic evidence"))
		missingAlias := "31000000-0000-0000-0000-000000000041"
		missing := item
		missing.ID = "31000000-0000-0000-0000-000000000042"
		missing.ExternalAliasID = &missingAlias
		missing.SessionID = nil
		missing.ContentHash = missingHash[:]
		missing.CoverageState = "partial"
		missing.Lineage = EvidenceLineage{}
		unlinked := batch
		unlinked.BatchID = "synthetic-batch-unlinked"
		unlinked.Next = 2
		unlinked.CursorDigest = "synthetic-digest-two"
		unlinked.Evidence = []Evidence{missing}
		if committed, err := repo.CommitCollection(ctx, unlinked); err != nil || !committed {
			t.Fatalf("unlinked commit=%t error=%v", committed, err)
		}
		var linked bool
		if err := repo.pool.QueryRow(ctx, `SELECT session_id IS NOT NULL
			FROM prompt_better.evidence_artifacts WHERE evidence_artifact_id=$1`, missing.ID).Scan(&linked); err != nil || linked {
			t.Fatalf("missing lineage linked=%t error=%v", linked, err)
		}
	})

	t.Run("keyed alias rotation loss and deletion", func(t *testing.T) {
		active := base
		firstKeyID := "35000000-0000-0000-0000-000000000001"
		secondKeyID := "35000000-0000-0000-0000-000000000002"
		if err := repo.PutKeyVersion(ctx, KeyVersion{ID: firstKeyID,
			Reference: "synthetic-identity-v1", State: "active", CreatedAt: base,
			ActivatedAt: &active}); err != nil {
			t.Fatal(err)
		}
		if err := repo.PutKeyVersion(ctx, KeyVersion{ID: secondKeyID,
			Reference: "synthetic-identity-v2", State: "active", CreatedAt: base.Add(time.Hour),
			ActivatedAt: &active}); err != nil {
			t.Fatal(err)
		}
		identifier := []byte("synthetic-private-project")
		firstDigest, err := identity.Alias(bytes.Repeat([]byte{1}, 32), identifier)
		if err != nil {
			t.Fatal(err)
		}
		oldAlias := ProjectAlias{ID: "35000000-0000-0000-0000-000000000011",
			ProjectID: project.ID, SourceID: source.ID, KeyVersionID: firstKeyID,
			Kind: "repository", Digest: firstDigest[:], CreatedAt: base}
		if err := repo.PutProjectAlias(ctx, oldAlias); err != nil {
			t.Fatal(err)
		}
		secondDigest, err := identity.Alias(bytes.Repeat([]byte{2}, 32), identifier)
		if err != nil {
			t.Fatal(err)
		}
		newAlias := ProjectAlias{ID: "35000000-0000-0000-0000-000000000012",
			ProjectID: project.ID, SourceID: source.ID, KeyVersionID: secondKeyID,
			Kind: "repository", Digest: secondDigest[:], CreatedAt: base.Add(time.Hour)}
		if err := repo.RekeyProjectAlias(ctx, oldAlias.ID, newAlias); err != nil {
			t.Fatal(err)
		}
		if err := repo.MarkKeyLost(ctx, firstKeyID, base.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := repo.DeleteKeyVersion(ctx, secondKeyID, base.Add(3*time.Hour)); err != nil {
			t.Fatal(err)
		}
		var aliasCount int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.project_aliases
            WHERE project_id=$1`, project.ID).Scan(&aliasCount); err != nil {
			t.Fatal(err)
		}
		if aliasCount != 0 {
			t.Fatalf("aliases after key deletion = %d", aliasCount)
		}
	})

	t.Run("callback failure rolls back", func(t *testing.T) {
		err := repo.WithSerializable(ctx, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(ctx, `INSERT INTO prompt_better.projects (project_id, created_at)
                VALUES ('40000000-0000-0000-0000-000000000001', $1)`, base)
			if execErr != nil {
				return execErr
			}
			return errors.New("synthetic rollback")
		})
		if err == nil || !strings.Contains(err.Error(), "synthetic rollback") {
			t.Fatalf("rollback error = %v", err)
		}
		var count int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.projects
            WHERE project_id='40000000-0000-0000-0000-000000000001'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("rolled-back project persisted")
		}
	})

	t.Run("concurrent identical SCD2 change leaves one current row", func(t *testing.T) {
		changes := []Project{project, project}
		for i := range changes {
			changes[i].VersionID = []string{
				"50000000-0000-0000-0000-000000000001",
				"50000000-0000-0000-0000-000000000002",
			}[i]
			changes[i].Classification = "confidential"
			changes[i].EffectiveAt = base.Add(3 * time.Hour)
		}
		var wg sync.WaitGroup
		errs := make(chan error, len(changes))
		for _, change := range changes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- repo.PutProject(ctx, change)
			}()
		}
		wg.Wait()
		close(errs)
		successes := 0
		for err := range errs {
			if err == nil {
				successes++
			}
		}
		if successes < 1 {
			t.Fatal("no concurrent change succeeded")
		}
		var current int
		if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.project_versions
            WHERE project_id=$1 AND valid_to IS NULL`, project.ID).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if current != 1 {
			t.Fatalf("current SCD2 rows = %d", current)
		}
	})
}

func prepareDatabase(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	db, err := OpenMigrationDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := BootstrapRoles(ctx, db); err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
}
