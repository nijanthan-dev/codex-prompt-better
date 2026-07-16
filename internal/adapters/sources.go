package adapters

import (
	"context"
	"errors"
	"io"
)

var ErrPurposeRequired = errors.New("targeted process purpose required")

type SourceOptions struct {
	Reader    io.Reader
	Key       []byte
	Enabled   bool
	Supported bool
	Purpose   string
	Identity  string
}

type contractAdapter struct {
	base        *JSONL
	requiredAny []string
}

func (a *contractAdapter) Kind() string { return a.base.Kind() }

func (a *contractAdapter) Collect(ctx context.Context, request Request) (Result, error) {
	result, err := a.base.Collect(ctx, request)
	if err != nil {
		return result, err
	}
	for _, record := range result.Records {
		matched := len(a.requiredAny) == 0
		for _, field := range a.requiredAny {
			if record.Attributes[field] != "" {
				matched = true
				break
			}
		}
		if !matched {
			return partial(result, "schema_drift"), ErrMalformed
		}
	}
	return result, nil
}

type ConfigurationAdapter struct{ *contractAdapter }
type GitAdapter struct{ *contractAdapter }
type GitHubAdapter struct{ *contractAdapter }
type CodexJSONLAdapter struct{ *contractAdapter }
type CodexStateAdapter struct{ *contractAdapter }
type RolloutAdapter struct{ *contractAdapter }
type ProcessAdapter struct{ *contractAdapter }

func Configuration(options SourceOptions) Adapter {
	return &ConfigurationAdapter{source("configuration", options, "execution_policy", "activity_class")}
}

func Git(options SourceOptions) Adapter {
	return &GitAdapter{source("git", options, "repository_alias", "worktree_alias")}
}

func GitHub(options SourceOptions) Adapter {
	return &GitHubAdapter{source("github", options, "event_kind", "repository_alias")}
}

func CodexJSONL(options SourceOptions) Adapter {
	return &CodexJSONLAdapter{source("codex_jsonl", options, "phase", "response_lineage")}
}

func CodexState(options SourceOptions) Adapter {
	return &CodexStateAdapter{source("codex_state_sqlite", options, "thread_alias", "state")}
}

func Rollout(options SourceOptions) Adapter {
	return &RolloutAdapter{source("rollout_summary", options, "trajectory_alias", "compaction_state")}
}

func Process(options SourceOptions) (Adapter, error) {
	if options.Enabled && options.Purpose == "" {
		return nil, ErrPurposeRequired
	}
	return &ProcessAdapter{source("process", options, "purpose")}, nil
}

func source(kind string, options SourceOptions, requiredAny ...string) *contractAdapter {
	identity := options.Identity
	if identity == "" {
		identity = kind
	}
	return &contractAdapter{
		base:        NewJSONLWithIdentity(kind, identity, options.Reader, options.Key, options.Enabled, options.Supported),
		requiredAny: requiredAny,
	}
}
