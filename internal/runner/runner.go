// Package runner owns scheduled collector execution separately from MCP.
package runner

import (
	"context"
	"errors"

	"github.com/nijanthan-dev/codex-prompt-better/internal/adapters"
)

type Collector interface {
	Run(context.Context, adapters.Adapter) (adapters.Result, error)
}

type Summary struct {
	Attempted int `json:"attempted"`
	Collected int `json:"collected"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
}

func RunOnce(ctx context.Context, collector Collector, sources []adapters.Adapter) (Summary, error) {
	if collector == nil {
		return Summary{}, errors.New("collector is required")
	}
	result := Summary{}
	for _, source := range sources {
		if source == nil {
			result.Skipped++
			continue
		}
		result.Attempted++
		collection, err := collector.Run(ctx, source)
		if errors.Is(err, adapters.ErrDisabled) || errors.Is(err, adapters.ErrUnsupported) {
			result.Skipped++
			continue
		}
		if err != nil {
			result.Failed++
			continue
		}
		for _, record := range collection.Records {
			if !record.GovernanceOverhead {
				result.Collected++
			}
		}
	}
	if result.Failed > 0 {
		return result, errors.New("one or more sources failed")
	}
	return result, nil
}
