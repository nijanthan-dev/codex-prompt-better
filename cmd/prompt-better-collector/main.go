// Command prompt-better-collector runs the separate incremental collector lifecycle.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/adapters"
	"github.com/nijanthan-dev/codex-prompt-better/internal/collector"
	"github.com/nijanthan-dev/codex-prompt-better/internal/evidence"
	"github.com/nijanthan-dev/codex-prompt-better/internal/runner"
	"github.com/nijanthan-dev/codex-prompt-better/internal/status"
	"github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, time.Now); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type fileConfig struct {
	Owner   string         `json:"owner"`
	Sources []sourceConfig `json:"sources"`
}

type sourceConfig struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	SourceID  string `json:"source_id"`
	VersionID string `json:"version_id"`
	CursorID  string `json:"cursor_id"`
	ProjectID string `json:"project_id,omitempty"`
	Enabled   bool   `json:"enabled"`
	Supported bool   `json:"supported"`
	Purpose   string `json:"purpose,omitempty"`
}

func run(ctx context.Context, arguments []string, output io.Writer, now func() time.Time) error {
	flags := flag.NewFlagSet("prompt-better-collector", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	statusOnly := flags.Bool("status", false, "print sanitized collector status")
	configPath := flags.String("config", "", "collector configuration path")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return errors.New("invalid collector arguments")
	}
	if *statusOnly {
		return writeDisabledStatus(output, now())
	}
	if *configPath == "" {
		return errors.New("collector requires explicit --config")
	}
	return collect(ctx, *configPath, output, now)
}

func collect(ctx context.Context, configPath string, output io.Writer, now func() time.Time) error {
	configFile, err := os.Open(configPath)
	if err != nil {
		return errors.New("collector configuration unavailable")
	}
	defer configFile.Close()
	const maxConfigBytes = 64 * 1024
	configData, err := io.ReadAll(io.LimitReader(configFile, maxConfigBytes+1))
	if err != nil || len(configData) > maxConfigBytes {
		return errors.New("invalid collector configuration")
	}
	decoder := json.NewDecoder(bytes.NewReader(configData))
	decoder.DisallowUnknownFields()
	var config fileConfig
	if err := decoder.Decode(&config); err != nil || config.Owner == "" || len(config.Sources) == 0 {
		return errors.New("invalid collector configuration")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid collector configuration")
	}
	if err := validateConfig(config); err != nil {
		return err
	}
	dsn := os.Getenv("PROMPT_BETTER_DATABASE_URL")
	if dsn == "" {
		return errors.New("collector database reference unavailable")
	}
	repository, err := postgres.OpenLocalRepository(ctx, dsn, postgres.RoleCollector)
	if err != nil {
		return err
	}
	defer repository.Close()
	keyText := os.Getenv("PROMPT_BETTER_IDENTITY_KEY")
	if len(keyText) < 32 {
		return errors.New("collector identity key unavailable")
	}
	key := []byte(keyText)
	sources := make([]adapters.Adapter, 0, len(config.Sources))
	mappings := make(map[string]collector.PersistentSource, len(config.Sources))
	files := []*os.File{}
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	effectiveAt := now().UTC()
	for _, item := range config.Sources {
		var reader io.Reader = strings.NewReader("")
		if item.Enabled {
			file, err := os.Open(item.Path)
			if err != nil {
				return errors.New("configured source unavailable")
			}
			files = append(files, file)
			reader = file
		}
		adapter, err := configuredAdapter(item, reader, key)
		if err != nil {
			return err
		}
		version := "1"
		if err := repository.PutSource(ctx, postgres.Source{
			ID: item.SourceID, VersionID: item.VersionID, Kind: item.Kind,
			AdapterVersion: &version, ProductSurface: "local", CoverageState: "unknown",
			Enabled: item.Enabled, CreatedAt: effectiveAt, EffectiveAt: effectiveAt,
		}); err != nil {
			return err
		}
		sources = append(sources, adapter)
		mappings[item.Kind] = persistentSource(item)
	}
	backend, err := collector.NewPostgresBackend(repository, mappings)
	if err != nil {
		return err
	}
	service, err := collector.New(backend, backend, collector.Options{
		Owner: config.Owner, BatchLimit: 1000, MaxAttempts: 3,
		LeaseDuration: 5 * time.Minute, BaseBackoff: 100 * time.Millisecond, Now: now,
	})
	if err != nil {
		return err
	}
	summary, err := runner.RunOnce(ctx, service, sources)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(summary)
}

func persistentSource(config sourceConfig) collector.PersistentSource {
	return collector.PersistentSource{
		SourceID: config.SourceID, CursorID: config.CursorID, ProjectID: config.ProjectID,
	}
}

func validateConfig(config fileConfig) error {
	kinds := map[string]bool{}
	sourceIDs := map[string]bool{}
	cursorIDs := map[string]bool{}
	for _, source := range config.Sources {
		if source.Kind == "" || source.SourceID == "" || source.VersionID == "" || source.CursorID == "" {
			return errors.New("invalid collector source identity")
		}
		if kinds[source.Kind] || sourceIDs[source.SourceID] || cursorIDs[source.CursorID] {
			return errors.New("duplicate collector source identity")
		}
		kinds[source.Kind], sourceIDs[source.SourceID], cursorIDs[source.CursorID] = true, true, true
	}
	return nil
}

func configuredAdapter(config sourceConfig, reader io.Reader, key []byte) (adapters.Adapter, error) {
	options := adapters.SourceOptions{Reader: reader, Key: key, Enabled: config.Enabled, Supported: config.Supported, Purpose: config.Purpose, Identity: config.SourceID}
	switch config.Kind {
	case "configuration":
		return adapters.Configuration(options), nil
	case "git":
		return adapters.Git(options), nil
	case "github":
		return adapters.GitHub(options), nil
	case "codex_jsonl":
		return adapters.CodexJSONL(options), nil
	case "codex_state_sqlite":
		return adapters.CodexState(options), nil
	case "rollout_summary":
		return adapters.Rollout(options), nil
	case "process":
		return adapters.Process(options)
	default:
		return nil, errors.New("unsupported collector source kind")
	}
}

func writeDisabledStatus(output io.Writer, at time.Time) error {
	sources := []status.Source{}
	for _, kind := range []string{"configuration", "git", "github", "codex_jsonl", "codex_state_sqlite", "rollout_summary", "process"} {
		sources = append(sources, status.Source{Kind: kind, Enabled: false, Supported: true, Coverage: evidence.CoverageMissing, Errors: []string{}})
	}
	return json.NewEncoder(output).Encode(status.Build(at, sources, 0, 0))
}
