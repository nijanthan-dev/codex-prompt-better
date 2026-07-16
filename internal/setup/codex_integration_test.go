//go:build integration

package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCodexPluginInitDoctorUninstallAndCollision(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex unavailable")
	}
	home := t.TempDir()
	root := sourceRoot(t)
	marketplace := buildTestMarketplace(t, root)
	runner := commandRunner{codexHome: home}
	ctx := context.Background()
	if output, err := runner.Run(ctx, "codex", "plugin", "marketplace", "add", marketplace); err != nil {
		t.Fatalf("marketplace add: %s", output)
	}
	if output, err := runner.Run(ctx, "codex", "plugin", "add", "prompt-better@prompt-better-test"); err != nil {
		t.Fatalf("plugin add: %s", output)
	}
	plugin, err := getRegistration(ctx, runner)
	if err != nil || plugin == nil || !isPluginRegistration(*plugin) {
		t.Fatalf("plugin registration=%#v err=%v", plugin, err)
	}

	config, expected, err := validateInstall("improve_only", []string{"git"}, "", root, "")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, filepath.FromSlash(defaultConfigRel))
	installRegistration := expected
	streams := Streams{Output: &bytes.Buffer{}, Error: &bytes.Buffer{}}
	if code := runInstall(ctx, home, configPath, config, expected, true, runner, streams); code != 0 {
		t.Fatalf("install code=%d", code)
	}
	current, err := getRegistration(ctx, runner)
	expected.Args = append(expected.Args, "--config", configPath)
	if err != nil || current == nil || !sameRegistration(*current, expected) {
		t.Fatalf("configured registration=%#v err=%v", current, err)
	}
	checks := doctorChecks(ctx, home, configPath, runner)
	if stateFor(checks, "server") != "ready" || stateFor(checks, "skill") != "ready" {
		t.Fatalf("doctor checks=%#v", checks)
	}
	if code := runUninstall(ctx, home, configPath, true, runner, streams); code != 0 {
		t.Fatalf("uninstall code=%d", code)
	}
	restored, err := getRegistration(ctx, runner)
	if err != nil || restored == nil || !isPluginRegistration(*restored) {
		t.Fatalf("restored plugin=%#v err=%v", restored, err)
	}

	if output, err := runner.Run(ctx, "codex", "mcp", "add", serverName, "--", "synthetic-conflict"); err != nil {
		t.Fatalf("collision add: %s", output)
	}
	if code := runInstall(ctx, home, configPath, config, installRegistration, true, runner, streams); code == 0 {
		t.Fatal("conflicting registration accepted")
	}
}

func buildTestMarketplace(t *testing.T, root string) string {
	t.Helper()
	marketplace := t.TempDir()
	plugin := filepath.Join(marketplace, "plugin")
	for _, relative := range []string{".codex-plugin/plugin.json", ".mcp.json", "skills/prompt-better/SKILL.md"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(plugin, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	document := map[string]any{
		"name":      "prompt-better-test",
		"interface": map[string]string{"displayName": "Prompt Better test"},
		"plugins": []map[string]any{{
			"name":     "prompt-better",
			"source":   map[string]string{"source": "local", "path": "./plugin"},
			"policy":   map[string]string{"installation": "AVAILABLE", "authentication": "ON_INSTALL"},
			"category": "Productivity",
		}},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(marketplace, ".agents", "plugins", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return marketplace
}

func stateFor(checks []doctorCheck, name string) string {
	for _, check := range checks {
		if check.Name == name {
			return check.State
		}
	}
	return ""
}
