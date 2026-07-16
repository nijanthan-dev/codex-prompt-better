package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/mcpserver"
	"github.com/nijanthan-dev/codex-prompt-better/internal/setup"
	"github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
	"github.com/nijanthan-dev/codex-prompt-better/internal/tools"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := runWithArgs(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "prompt-better-mcp: server_failed")
		os.Exit(1)
	}
}

func run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	return runWithArgs(ctx, nil, stdin, stdout, stderr)
}

func runWithArgs(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("prompt-better-mcp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var configPath string
	flags.StringVar(&configPath, "config", "", "PromptBetter integration config")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return errors.New("configuration invalid")
	}
	configuredSources := []string{}
	if configPath != "" {
		integrationConfig, err := setup.LoadIntegrationConfig(configPath)
		if err != nil {
			return err
		}
		configuredSources = integrationConfig.SourceKinds
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	options := mcpserver.DefaultOptions(logger)
	server, err := mcpserver.New(options)
	if err != nil {
		return errors.New("configuration invalid")
	}
	auditStore, closeStore, err := openAuditStore(ctx)
	if err != nil {
		return err
	}
	defer closeStore()
	if _, err := tools.RegisterConfigured(server, auditStore, configuredSources); err != nil {
		return errors.New("tool registration failed")
	}
	transport, err := mcpserver.NewStdioTransport(stdin, stdout, options.MaxInputBytes)
	if err != nil {
		return errors.New("transport invalid")
	}
	return server.Run(ctx, transport)
}

func openAuditStore(ctx context.Context) (tools.AuditStore, func(), error) {
	dsn := os.Getenv("PROMPT_BETTER_DATABASE_URL")
	if dsn == "" {
		return nil, func() {}, nil
	}
	openContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	repository, err := postgres.OpenRepository(openContext, dsn, postgres.DefaultPoolConfig())
	if err != nil {
		return nil, nil, errors.New("audit store unavailable")
	}
	return repository, repository.Close, nil
}
