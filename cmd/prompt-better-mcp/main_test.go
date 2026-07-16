package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const helperEnvironment = "PROMPT_BETTER_MCP_TEST_HELPER"

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv(helperEnvironment) != "1" {
		return
	}
	if err := run(context.Background(), os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestSubprocessLifecycle_ProtocolOnlyAndPromptExit(t *testing.T) {
	var stderr bytes.Buffer
	command := exec.Command(os.Args[0], "-test.run=TestMCPHelperProcess")
	command.Env = append(os.Environ(), helperEnvironment+"=1", "PROMPT_BETTER_DATABASE_URL=")
	command.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "synthetic-client", Version: "0.0.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) < 1 || !containsTool(tools.Tools, "improve_prompt") {
		t.Fatalf("unexpected shell tools: %#v, %v", tools, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "improve_prompt", Arguments: map[string]any{
		"schema_version": "1.0.0", "kind": "request", "intent": "Return a synthetic result.", "execution_policy": "improve_only",
	}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("subprocess call result=%#v err=%v", result, err)
	}
	audit, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "audit_session", Arguments: map[string]any{
		"schema_version": "1.0.0", "kind": "request", "reference": "00000000-0000-4000-8000-000000000001",
		"consent": "granted", "configured_sources": []string{"git"},
	}})
	if err != nil || !audit.IsError {
		t.Fatalf("unconfigured audit result=%#v err=%v", audit, err)
	}
	if err := session.Close(); err != nil && !strings.Contains(err.Error(), "signal: terminated") {
		t.Fatal(err)
	}
	if output := stderr.String(); strings.Contains(output, "synthetic-client") || strings.Contains(output, "initialize") {
		t.Fatalf("unsafe stderr: %q", output)
	}
}

func TestRunWithArgs_RejectsInvalidConfigWithoutLeakingPath(t *testing.T) {
	var stderr bytes.Buffer
	err := runWithArgs(context.Background(), []string{"--config", "/synthetic/private/config"}, bytes.NewReader(nil), &bytes.Buffer{}, &stderr)
	if err == nil || strings.Contains(err.Error(), "/synthetic/private/config") {
		t.Fatalf("unsafe config error: %v", err)
	}
}

func containsTool(tools []*mcp.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
