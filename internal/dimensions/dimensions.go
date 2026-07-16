// Package dimensions routes mutable identity attributes through SCD2 repositories.
package dimensions

import (
	"context"
	"errors"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

var ErrUnknownEventTime = errors.New("dimension event time unknown")

type ProjectObservation struct {
	ID             string
	VersionID      string
	CreatedAt      time.Time
	EventAt        *time.Time
	IngestedAt     time.Time
	Classification string
	LifecycleState string
}

type SourceObservation struct {
	ID             string
	VersionID      string
	Kind           string
	AdapterVersion *string
	ProductSurface string
	CoverageState  string
	Enabled        bool
	CreatedAt      time.Time
	EventAt        *time.Time
	IngestedAt     time.Time
}

type Repository interface {
	PutProject(context.Context, postgres.Project) error
	PutSource(context.Context, postgres.Source) error
}

type Outcome struct {
	State      string
	Reason     string
	EventAt    *time.Time
	IngestedAt time.Time
}

func PutProject(ctx context.Context, repository Repository, observation ProjectObservation) (Outcome, error) {
	outcome := Outcome{State: "quarantined", EventAt: observation.EventAt, IngestedAt: observation.IngestedAt}
	if observation.EventAt == nil {
		outcome.Reason = "unknown_event_time"
		return outcome, ErrUnknownEventTime
	}
	err := repository.PutProject(ctx, postgres.Project{
		ID: observation.ID, VersionID: observation.VersionID, CreatedAt: observation.CreatedAt,
		EffectiveAt: *observation.EventAt, Classification: observation.Classification,
		LifecycleState: observation.LifecycleState,
	})
	if errors.Is(err, postgres.ErrInvalidEffectiveTime) {
		outcome.Reason = "late_or_out_of_order"
		return outcome, err
	}
	if err != nil {
		outcome.Reason = "repository_failure"
		return outcome, err
	}
	outcome.State = "applied_or_identical"
	return outcome, nil
}

func PutSource(ctx context.Context, repository Repository, observation SourceObservation) (Outcome, error) {
	outcome := Outcome{State: "quarantined", EventAt: observation.EventAt, IngestedAt: observation.IngestedAt}
	if observation.EventAt == nil {
		outcome.Reason = "unknown_event_time"
		return outcome, ErrUnknownEventTime
	}
	err := repository.PutSource(ctx, postgres.Source{
		ID: observation.ID, VersionID: observation.VersionID, Kind: observation.Kind,
		AdapterVersion: observation.AdapterVersion, ProductSurface: observation.ProductSurface,
		CoverageState: observation.CoverageState, Enabled: observation.Enabled,
		CreatedAt: observation.CreatedAt, EffectiveAt: *observation.EventAt,
	})
	if errors.Is(err, postgres.ErrInvalidEffectiveTime) {
		outcome.Reason = "late_or_out_of_order"
		return outcome, err
	}
	if err != nil {
		outcome.Reason = "repository_failure"
		return outcome, err
	}
	outcome.State = "applied_or_identical"
	return outcome, nil
}
