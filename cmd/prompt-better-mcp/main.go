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
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	if err := runProcess(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "prompt-better-mcp: server_failed")
		os.Exit(1)
	}
}

func runProcess(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	exited := make(chan error, 1)
	go func() { exited <- runWithArgs(ctx, args, stdin, stdout, stderr) }()
	select {
	case err := <-exited:
		return err
	case <-ctx.Done():
		select {
		case err := <-exited:
			return err
		case <-time.After(250 * time.Millisecond):
			return nil
		}
	}
}

func runWithArgs(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("prompt-better-mcp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var configPath string
	flags.StringVar(&configPath, "config", "", "PromptBetter integration config")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return errors.New("configuration invalid")
	}
	toolConfig := tools.Configuration{ExecutionPolicy: contracts.ExecutionPolicyImproveOnly, SourceKinds: []string{}}
	if configPath != "" {
		integrationConfig, err := setup.LoadIntegrationConfig(configPath)
		if err != nil {
			return err
		}
		toolConfig = tools.Configuration{
			ExecutionPolicy:          integrationConfig.ExecutionPolicy,
			SourceKinds:              integrationConfig.SourceKinds,
			ProcessPurposeConfigured: integrationConfig.ProcessPurpose != "",
		}
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
	if _, err := tools.RegisterWithConfig(server, auditStore, toolConfig); err != nil {
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
	repository, err := postgres.OpenLocalRepository(openContext, dsn, postgres.RoleRuntime)
	if err != nil {
		return nil, nil, errors.New("audit store unavailable")
	}
	return repository, repository.Close, nil
}
