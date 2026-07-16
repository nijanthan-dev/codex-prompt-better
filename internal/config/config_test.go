package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func TestLoadFilePrecedenceAndProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(
		`{"execution_policy":"ask_before_execute","host_permission":"denied",` +
			`"max_input_bytes":2048,"timeout":"3s","format":"json"}`,
	)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExecutionPolicy != contracts.ExecutionPolicyAskBeforeExecute ||
		cfg.HostPermission != policy.HostDenied ||
		cfg.MaxInputBytes != 2048 ||
		cfg.Timeout != 3*time.Second ||
		cfg.Format != "json" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	for _, field := range []string{"execution_policy", "host_permission", "max_input_bytes", "timeout", "format"} {
		if cfg.Provenance[field] != "file" {
			t.Fatalf("%s provenance=%q", field, cfg.Provenance[field])
		}
	}
}

func TestLoadFileRejectsUnknownAndUnsafeValues(t *testing.T) {
	tests := map[string]string{
		"unknown":       `{"extra":true}`,
		"limit":         `{"max_input_bytes":1048577}`,
		"timeout":       `{"timeout":"2m"}`,
		"empty timeout": `{"timeout":""}`,
		"trailing":      `{} garbage`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFile(context.Background(), path); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestLoadFileRejectsOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, make([]byte, maxConfigBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(context.Background(), path); err == nil {
		t.Fatal("oversized config accepted")
	}
}

func FuzzLoadFile(f *testing.F) {
	f.Add([]byte(`{"execution_policy":"improve_only"}`))
	f.Add([]byte(`{"unknown":true}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxConfigBytes+1 {
			return
		}
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = LoadFile(context.Background(), path)
	})
}
