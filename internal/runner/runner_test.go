package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nijanthan-dev/codex-prompt-better/internal/adapters"
	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
)

type fakeCollector struct {
	results map[string]adapters.Result
	errors  map[string]error
}

func (f fakeCollector) Run(_ context.Context, adapter adapters.Adapter) (adapters.Result, error) {
	return f.results[adapter.Kind()], f.errors[adapter.Kind()]
}

func TestRunOnce_IsolatesSourcesAndExcludesOverhead(t *testing.T) {
	t.Parallel()
	sources := []adapters.Adapter{
		adapters.NewJSONL("git", strings.NewReader(""), nil, true, true),
		adapters.NewJSONL("github", strings.NewReader(""), nil, true, true),
		adapters.NewJSONL("process", strings.NewReader(""), nil, true, true),
		nil,
	}
	collector := fakeCollector{
		results: map[string]adapters.Result{
			"git":     {Records: []evidence.Record{{ID: "user"}}},
			"process": {Records: []evidence.Record{{ID: "self", GovernanceOverhead: true}}},
		},
		errors: map[string]error{"github": adapters.ErrUnsupported},
	}
	result, err := RunOnce(context.Background(), collector, sources)
	if err != nil || result.Attempted != 3 || result.Collected != 1 || result.Skipped != 2 || result.Failed != 0 {
		t.Fatalf("unexpected run: %#v, %v", result, err)
	}
}

func TestRunOnce_ReturnsAggregateFailureWithoutStoppingLaterSource(t *testing.T) {
	t.Parallel()
	sources := []adapters.Adapter{
		adapters.NewJSONL("git", strings.NewReader(""), nil, true, true),
		adapters.NewJSONL("configuration", strings.NewReader(""), nil, true, true),
	}
	collector := fakeCollector{results: map[string]adapters.Result{"configuration": {Records: []evidence.Record{{ID: "user"}}}}, errors: map[string]error{"git": errors.New("synthetic")}}
	result, err := RunOnce(context.Background(), collector, sources)
	if err == nil || result.Failed != 1 || result.Collected != 1 {
		t.Fatalf("failure isolation changed: %#v, %v", result, err)
	}
}
