//go:build integration

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBinaryCommands(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	binary := filepath.Join(t.TempDir(), "prompt-better")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/prompt-better")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	inputPath := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(inputPath, []byte("Synthetic file input."), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		args        []string
		stdin, want string
		exit        int
	}{
		{name: "stdin text", args: []string{"improve_prompt"}, stdin: "Synthetic stdin.", want: "Goal:", exit: 0},
		{
			name: "positional goal", args: []string{"create_goal_prompt", "Synthetic", "goal"},
			want: "Take this as a new goal:", exit: 0,
		},
		{name: "file", args: []string{"improve_prompt", "--input", inputPath}, want: "Synthetic file input.", exit: 0},
		{
			name: "json lint error", args: []string{"lint_prompt", "--format", "json"},
			stdin: "Keep going until happy.", want: `"valid":false`, exit: 3,
		},
		{name: "unknown command", args: []string{"unknown"}, want: "invalid_schema", exit: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(binary, test.args...)
			command.Stdin = strings.NewReader(test.stdin)
			var output bytes.Buffer
			command.Stdout = &output
			command.Stderr = &output
			err := command.Run()
			exit := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatal(err)
				}
				exit = exitErr.ExitCode()
			}
			if exit != test.exit || !strings.Contains(output.String(), test.want) {
				t.Fatalf("exit=%d output=%q", exit, output.String())
			}
		})
	}
}
