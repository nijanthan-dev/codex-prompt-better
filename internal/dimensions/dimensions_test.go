package dimensions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

type fakeRepository struct {
	projects []postgres.Project
	sources  []postgres.Source
	err      error
}

func (f *fakeRepository) PutProject(_ context.Context, project postgres.Project) error {
	f.projects = append(f.projects, project)
	return f.err
}

func (f *fakeRepository) PutSource(_ context.Context, source postgres.Source) error {
	f.sources = append(f.sources, source)
	return f.err
}

func TestPutProject_UsesEventTimeNotIngestionTime(t *testing.T) {
	t.Parallel()
	repository := &fakeRepository{}
	eventAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ingestedAt := eventAt.Add(24 * time.Hour)
	outcome, err := PutProject(context.Background(), repository, ProjectObservation{
		ID: "stable", VersionID: "version", CreatedAt: eventAt, EventAt: &eventAt,
		IngestedAt: ingestedAt, Classification: "internal", LifecycleState: "active",
	})
	if err != nil || outcome.State != "applied_or_identical" || len(repository.projects) != 1 {
		t.Fatalf("unexpected result: %#v, %v", outcome, err)
	}
	if !repository.projects[0].EffectiveAt.Equal(eventAt) || repository.projects[0].EffectiveAt.Equal(ingestedAt) {
		t.Fatalf("ingestion time substituted: %#v", repository.projects[0])
	}
}

func TestPutProject_UnknownEventTimeQuarantinesWithoutWrite(t *testing.T) {
	t.Parallel()
	repository := &fakeRepository{}
	outcome, err := PutProject(context.Background(), repository, ProjectObservation{IngestedAt: time.Now()})
	if !errors.Is(err, ErrUnknownEventTime) || outcome.Reason != "unknown_event_time" || len(repository.projects) != 0 {
		t.Fatalf("unknown event handling: %#v, %v", outcome, err)
	}
}

func TestPutSource_LateObservationIsAuditableQuarantine(t *testing.T) {
	t.Parallel()
	eventAt := time.Now()
	repository := &fakeRepository{err: postgres.ErrInvalidEffectiveTime}
	outcome, err := PutSource(context.Background(), repository, SourceObservation{EventAt: &eventAt, IngestedAt: eventAt.Add(time.Hour)})
	if !errors.Is(err, postgres.ErrInvalidEffectiveTime) || outcome.State != "quarantined" || outcome.Reason != "late_or_out_of_order" {
		t.Fatalf("late observation handling: %#v, %v", outcome, err)
	}
}
