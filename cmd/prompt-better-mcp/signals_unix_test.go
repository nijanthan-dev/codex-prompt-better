//go:build integration && !windows

package main

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestBuiltServer_SIGTERMExitsPromptly(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "prompt-better-mcp")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s", output)
	}
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "PROMPT_BETTER_DATABASE_URL=")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(input, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"signal-test","version":"1.0"}}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(output).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		_ = command.Process.Kill()
		t.Fatal("server did not exit after SIGTERM")
	}
}
