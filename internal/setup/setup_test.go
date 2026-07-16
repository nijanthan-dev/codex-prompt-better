package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeRunner struct {
	registration *registration
	plugin       *registration
	calls        [][]string
	failAdd      bool
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if len(args) >= 3 && args[0] == "mcp" && args[1] == "get" {
		if r.registration == nil {
			return []byte("not found"), errors.New("missing")
		}
		return json.Marshal(r.registration)
	}
	if len(args) >= 3 && args[0] == "mcp" && args[1] == "add" {
		if r.failAdd {
			return []byte("synthetic failure"), errors.New("failed")
		}
		separator := 4
		r.registration = &registration{Command: args[separator], Args: append([]string(nil), args[separator+1:]...)}
	}
	if len(args) >= 3 && args[0] == "mcp" && args[1] == "remove" {
		r.registration = r.plugin
	}
	return nil, nil
}

func TestInstallOverlaysPluginRegistrationAndUninstallRestoresIt(t *testing.T) {
	home := t.TempDir()
	config, expected, _ := validateInstall("improve_only", []string{"git"}, "", sourceRoot(t), "")
	configPath := filepath.Join(home, filepath.FromSlash(defaultConfigRel))
	plugin := &registration{Command: "go", Args: []string{"run", "./cmd/prompt-better-mcp"}, Cwd: sourceRoot(t)}
	runner := &fakeRunner{registration: plugin, plugin: plugin}
	streams := Streams{Output: &bytes.Buffer{}, Error: &bytes.Buffer{}}

	if code := runInstall(context.Background(), home, configPath, config, expected, true, runner, streams); code != 0 {
		t.Fatalf("plugin overlay code=%d", code)
	}
	if runner.registration == nil || isPluginRegistration(*runner.registration) {
		t.Fatalf("configured registration not overlaid: %#v", runner.registration)
	}
	if code := runUninstall(context.Background(), home, configPath, true, runner, streams); code != 0 {
		t.Fatalf("plugin restore code=%d", code)
	}
	if runner.registration == nil || !sameRegistration(*runner.registration, *plugin) {
		t.Fatalf("plugin registration not restored: %#v", runner.registration)
	}
}

func TestInstallRollsBackNewFilesWhenRegistrationFails(t *testing.T) {
	home := t.TempDir()
	config, expected, _ := validateInstall("improve_only", []string{"git"}, "", sourceRoot(t), "")
	configPath := filepath.Join(home, filepath.FromSlash(defaultConfigRel))
	runner := &fakeRunner{failAdd: true}
	streams := Streams{Output: &bytes.Buffer{}, Error: &bytes.Buffer{}}
	if code := runInstall(context.Background(), home, configPath, config, expected, true, runner, streams); code == 0 {
		t.Fatal("registration failure accepted")
	}
	for _, path := range []string{configPath, filepath.Join(home, filepath.FromSlash(skillRel)), filepath.Join(home, filepath.FromSlash(manifestRel))} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial file retained: %s", filepath.Base(path))
		}
	}
}

func TestInstallPreviewApplyIdempotentAndUninstall(t *testing.T) {
	home := t.TempDir()
	root := sourceRoot(t)
	config, expected, err := validateInstall("improve_only", []string{"git", "configuration"}, "", root, "")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	configPath := filepath.Join(home, filepath.FromSlash(defaultConfigRel))
	streams := Streams{Output: &bytes.Buffer{}, Error: &bytes.Buffer{}}
	if code := runInstall(context.Background(), home, configPath, config, expected, false, runner, streams); code != 0 {
		t.Fatalf("preview code %d", code)
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) || runner.registration != nil {
		t.Fatal("preview mutated state")
	}
	if code := runInstall(context.Background(), home, configPath, config, expected, true, runner, streams); code != 0 {
		t.Fatalf("apply code %d", code)
	}
	for _, path := range []string{configPath, filepath.Join(home, filepath.FromSlash(skillRel)), filepath.Join(home, filepath.FromSlash(manifestRel))} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("owned file mode: %v %v", info, err)
		}
	}
	manifestData, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(manifestRel)))
	if err != nil || strings.Contains(string(manifestData), root) {
		t.Fatalf("ownership manifest retained source path: %v", err)
	}
	callCount := len(runner.calls)
	if code := runInstall(context.Background(), home, configPath, config, expected, true, runner, streams); code != 0 || len(runner.calls) != callCount+1 {
		t.Fatalf("idempotent apply code=%d calls=%d", code, len(runner.calls)-callCount)
	}
	if code := runUninstall(context.Background(), home, configPath, false, runner, streams); code != 0 {
		t.Fatalf("uninstall preview %d", code)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatal("uninstall preview removed file")
	}
	if code := runUninstall(context.Background(), home, configPath, true, runner, streams); code != 0 {
		t.Fatalf("uninstall apply %d", code)
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) || runner.registration != nil {
		t.Fatal("uninstall retained owned state")
	}
}

func TestInstallAndUninstallRefuseModifiedOwnedFiles(t *testing.T) {
	home := t.TempDir()
	root := sourceRoot(t)
	config, expected, _ := validateInstall("ask_before_execute", []string{"git"}, "", root, "")
	configPath := filepath.Join(home, filepath.FromSlash(defaultConfigRel))
	runner := &fakeRunner{}
	streams := Streams{Output: &bytes.Buffer{}, Error: &bytes.Buffer{}}
	if code := runInstall(context.Background(), home, configPath, config, expected, true, runner, streams); code != 0 {
		t.Fatal(code)
	}
	if err := os.WriteFile(configPath, []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runUninstall(context.Background(), home, configPath, true, runner, streams); code == 0 {
		t.Fatal("modified file removed")
	}
	if code := runInstall(context.Background(), home, configPath, config, expected, true, runner, streams); code == 0 {
		t.Fatal("modified file overwritten")
	}
}

func TestInstallRefusesInsecureOrLinkedExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions and symlinks")
	}
	home := t.TempDir()
	root := sourceRoot(t)
	config, expected, _ := validateInstall("improve_only", []string{"git"}, "", root, "")
	configPath := filepath.Join(home, filepath.FromSlash(defaultConfigRel))
	configData, _ := json.MarshalIndent(config, "", "  ")
	configData = append(configData, '\n')
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatal(err)
	}
	streams := Streams{Output: &bytes.Buffer{}, Error: &bytes.Buffer{}}
	if code := runInstall(context.Background(), home, configPath, config, expected, true, &fakeRunner{}, streams); code == 0 {
		t.Fatal("insecure existing file claimed")
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "target")
	if err := os.WriteFile(target, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, configPath); err != nil {
		t.Fatal(err)
	}
	if code := runInstall(context.Background(), home, configPath, config, expected, true, &fakeRunner{}, streams); code == 0 {
		t.Fatal("linked existing file claimed")
	}
}

func TestUninstallRefusesChangedRegistration(t *testing.T) {
	home := t.TempDir()
	config, expected, _ := validateInstall("improve_only", []string{"git"}, "", sourceRoot(t), "")
	configPath := filepath.Join(home, filepath.FromSlash(defaultConfigRel))
	runner := &fakeRunner{}
	streams := Streams{Output: &bytes.Buffer{}, Error: &bytes.Buffer{}}
	if code := runInstall(context.Background(), home, configPath, config, expected, true, runner, streams); code != 0 {
		t.Fatal(code)
	}
	runner.registration = &registration{Command: "different-owned-command"}
	if code := runUninstall(context.Background(), home, configPath, true, runner, streams); code == 0 {
		t.Fatal("changed registration removed")
	}
}

func TestValidateInstallRejectsUnsafeModes(t *testing.T) {
	root := sourceRoot(t)
	for _, test := range []struct {
		name    string
		policy  string
		sources []string
		purpose string
		root    string
		binary  string
	}{
		{"policy", "execute_all", []string{"git"}, "", root, ""},
		{"source", "improve_only", []string{"unknown"}, "", root, ""},
		{"process purpose", "improve_only", []string{"process"}, "", root, ""},
		{"both modes", "improve_only", []string{"git"}, "", root, root},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := validateInstall(test.policy, test.sources, test.purpose, test.root, test.binary); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestDoctorSanitizesOutput(t *testing.T) {
	home := t.TempDir()
	secretPath := filepath.Join(home, "secret-user-path")
	var output bytes.Buffer
	checks := doctorChecks(context.Background(), home, secretPath, &fakeRunner{})
	data, err := json.Marshal(checks)
	if err != nil {
		t.Fatal(err)
	}
	output.Write(data)
	text := output.String()
	if strings.Contains(text, home) || strings.Contains(text, "secret-user-path") {
		t.Fatal("doctor leaked path")
	}
	if !strings.Contains(text, `"host_capabilities","state":"unknown"`) {
		t.Fatal("unknown capability not preserved")
	}
}

func TestAllReadyRequiresVerifiedCollectors(t *testing.T) {
	checks := []doctorCheck{
		{Name: "platform", State: "supported"},
		{Name: "config", State: "ready"},
		{Name: "ownership", State: "ready"},
		{Name: "skill", State: "ready"},
		{Name: "mcp_registration", State: "ready"},
		{Name: "server", State: "ready"},
		{Name: "database", State: "ready"},
		{Name: "collectors", State: "ready"},
		{Name: "host_capabilities", State: "unknown"},
	}
	if !allReady(checks) {
		t.Fatal("verified readiness rejected")
	}
	checks[7].State = "configured"
	if allReady(checks) {
		t.Fatal("unverified collector state accepted")
	}
}

func TestLoadIntegrationConfig_StrictAndSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "integration.json")
	valid := `{"schema_version":"1.0.0","execution_policy":"improve_only","source_kinds":["git"],"raw_prompt_retention":false,"telemetry":false,"database_env":"PROMPT_BETTER_DATABASE_URL"}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadIntegrationConfig(path)
	if err != nil || len(config.SourceKinds) != 1 {
		t.Fatalf("load config=%#v err=%v", config, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSuffix(valid, "}")+`,"secret":"value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIntegrationConfig(path); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestGetRegistration_ParsesDocumentedCodexShape(t *testing.T) {
	runner := staticRunner{data: []byte(`{"name":"promptBetter","transport":{"type":"stdio","command":"go","args":["run","./cmd/prompt-better-mcp"],"cwd":"/synthetic/plugin"}}`)}
	registration, err := getRegistration(context.Background(), runner)
	if err != nil || registration.Command != "go" || len(registration.Args) != 2 || registration.Cwd == "" {
		t.Fatalf("registration=%#v err=%v", registration, err)
	}
}

func TestServerReadiness_MissingBinary(t *testing.T) {
	if state := serverReadiness(context.Background(), registration{Command: filepath.Join(t.TempDir(), "missing")}); state != "unavailable" {
		t.Fatalf("missing binary state=%s", state)
	}
}

func TestSupportedGoVersion(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{"go version go1.25.0 darwin/arm64", true},
		{"go version go1.26rc1 linux/amd64", false},
		{"go version go1.24.9 windows/amd64", false},
		{"invalid", false},
	} {
		if got := supportedGoVersion(test.value); got != test.want {
			t.Fatalf("supportedGoVersion(%q)=%t", test.value, got)
		}
	}
}

type staticRunner struct{ data []byte }

func (runner staticRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return runner.data, nil
}

func sourceRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
