// Package setup implements the read-only doctor and explicit Codex integration setup.
package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
	"github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
	promptbetter "github.com/nijanthan-dev/codex-prompt-better/skills/prompt-better"
)

const (
	serverName       = "promptBetter"
	configVersion    = "1.0.0"
	defaultConfigRel = "prompt-better/integration.json"
	manifestRel      = "prompt-better/ownership.json"
	skillRel         = "skills/prompt-better/SKILL.md"
	databaseEnv      = "PROMPT_BETTER_DATABASE_URL"
	maxFileBytes     = 64 * 1024
)

var sourceKinds = map[string]bool{
	"configuration": true, "git": true, "github": true, "codex_jsonl": true,
	"codex_state_sqlite": true, "rollout_summary": true, "process": true,
}

// Streams are setup command output streams.
type Streams struct {
	Output io.Writer
	Error  io.Writer
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

// Runner executes documented Codex MCP commands.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type commandRunner struct{ codexHome string }

func (r commandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = append(os.Environ(), "CODEX_HOME="+r.codexHome)
	return command.CombinedOutput()
}

// IntegrationConfig is the versioned, privacy-safe PromptBetter integration configuration.
type IntegrationConfig struct {
	SchemaVersion      string                    `json:"schema_version"`
	ExecutionPolicy    contracts.ExecutionPolicy `json:"execution_policy"`
	SourceKinds        []string                  `json:"source_kinds"`
	ProcessPurpose     string                    `json:"process_purpose,omitempty"`
	RawPromptRetention bool                      `json:"raw_prompt_retention"`
	Telemetry          bool                      `json:"telemetry"`
	DatabaseEnv        string                    `json:"database_env"`
}

// LoadIntegrationConfig reads and validates a bounded integration config.
func LoadIntegrationConfig(path string) (IntegrationConfig, error) {
	data, err := readBounded(path)
	if err != nil {
		return IntegrationConfig{}, errors.New("integration config unavailable")
	}
	var config IntegrationConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(&struct{}{}) != io.EOF || validateStoredConfig(config) != nil {
		return IntegrationConfig{}, errors.New("integration config invalid")
	}
	return config, nil
}

type ownershipManifest struct {
	SchemaVersion    string            `json:"schema_version"`
	ServerName       string            `json:"server_name"`
	RegistrationHash string            `json:"registration_hash"`
	Files            map[string]string `json:"files"`
}

type registration struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd,omitempty"`
}

// RunInit parses and executes prompt-better init.
func RunInit(ctx context.Context, args []string, streams Streams) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var sources stringList
	var executionPolicy, processPurpose, codexHome, configPath, sourceRoot, serverBinary string
	var apply, uninstall bool
	fs.Var(&sources, "source", "enabled source kind; repeatable")
	fs.StringVar(&executionPolicy, "execution-policy", "", "execution policy")
	fs.StringVar(&processPurpose, "process-purpose", "", "targeted process purpose")
	fs.StringVar(&codexHome, "codex-home", "", "Codex home")
	fs.StringVar(&configPath, "config", "", "integration config")
	fs.StringVar(&sourceRoot, "source-root", "", "source checkout")
	fs.StringVar(&serverBinary, "server-binary", "", "MCP server binary")
	fs.BoolVar(&apply, "apply", false, "apply changes")
	fs.BoolVar(&uninstall, "uninstall", false, "remove owned integration")
	if err := fs.Parse(args); err != nil || len(fs.Args()) != 0 {
		return writeError(streams.Error, "invalid command flags")
	}
	resolvedHome, err := resolveCodexHome(codexHome)
	if err != nil {
		return writeError(streams.Error, "invalid Codex home")
	}
	if configPath == "" {
		configPath = filepath.Join(resolvedHome, filepath.FromSlash(defaultConfigRel))
	} else if !filepath.IsAbs(configPath) {
		return writeError(streams.Error, "config path must be absolute")
	}
	runner := commandRunner{codexHome: resolvedHome}
	if uninstall {
		if executionPolicy != "" || len(sources) != 0 || processPurpose != "" || sourceRoot != "" || serverBinary != "" {
			return writeError(streams.Error, "uninstall flags conflict")
		}
		return runUninstall(ctx, resolvedHome, configPath, apply, runner, streams)
	}
	config, expected, err := validateInstall(executionPolicy, sources, processPurpose, sourceRoot, serverBinary)
	if err != nil {
		return writeError(streams.Error, err.Error())
	}
	return runInstall(ctx, resolvedHome, configPath, config, expected, apply, runner, streams)
}

func validateInstall(policyText string, sources []string, purpose, sourceRoot, serverBinary string) (IntegrationConfig, registration, error) {
	policyValue := contracts.ExecutionPolicy(policyText)
	if !policy.ValidExecutionPolicy(policyValue) {
		return IntegrationConfig{}, registration{}, errors.New("invalid execution policy")
	}
	if len(sources) == 0 || len(sources) > len(sourceKinds) {
		return IntegrationConfig{}, registration{}, errors.New("at least one valid source required")
	}
	seen := make(map[string]bool, len(sources))
	for _, kind := range sources {
		if !sourceKinds[kind] || seen[kind] {
			return IntegrationConfig{}, registration{}, errors.New("invalid or duplicate source")
		}
		seen[kind] = true
	}
	if seen["process"] != (purpose != "") {
		return IntegrationConfig{}, registration{}, errors.New("process source requires purpose")
	}
	if (sourceRoot == "") == (serverBinary == "") {
		return IntegrationConfig{}, registration{}, errors.New("choose one server mode")
	}
	var expected registration
	if sourceRoot != "" {
		root, err := regularDirectory(sourceRoot)
		if err != nil {
			return IntegrationConfig{}, registration{}, errors.New("source checkout unavailable")
		}
		if _, err := os.Stat(filepath.Join(root, "cmd", "prompt-better-mcp")); err != nil {
			return IntegrationConfig{}, registration{}, errors.New("source server unavailable")
		}
		expected = registration{Command: "go", Args: []string{"-C", root, "run", "./cmd/prompt-better-mcp"}}
	} else {
		binary, err := regularFile(serverBinary)
		if err != nil {
			return IntegrationConfig{}, registration{}, errors.New("server binary unavailable")
		}
		info, _ := os.Stat(binary)
		if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
			return IntegrationConfig{}, registration{}, errors.New("server binary is not executable")
		}
		expected = registration{Command: binary}
	}
	sortedSources := append([]string(nil), sources...)
	sort.Strings(sortedSources)
	return IntegrationConfig{
		SchemaVersion: configVersion, ExecutionPolicy: policyValue, SourceKinds: sortedSources,
		ProcessPurpose: purpose, RawPromptRetention: false, Telemetry: false, DatabaseEnv: databaseEnv,
	}, expected, nil
}

func runInstall(ctx context.Context, home, configPath string, config IntegrationConfig, expected registration, apply bool, runner Runner, streams Streams) int {
	expected.Args = append(append([]string(nil), expected.Args...), "--config", configPath)
	configData, _ := json.MarshalIndent(config, "", "  ")
	configData = append(configData, '\n')
	files := map[string][]byte{configPath: configData, filepath.Join(home, filepath.FromSlash(skillRel)): promptbetter.Skill}
	manifestPath := filepath.Join(home, filepath.FromSlash(manifestRel))
	manifest := ownershipManifest{SchemaVersion: configVersion, ServerName: serverName, RegistrationHash: registrationDigest(expected), Files: map[string]string{}}
	missing := map[string]bool{}
	for path, data := range files {
		manifest.Files[ownedName(home, configPath, path)] = digest(data)
		if state, err := fileState(path, data); err != nil {
			return writeError(streams.Error, "source_conflict")
		} else if state == "conflict" {
			return writeError(streams.Error, "source_conflict")
		} else if state == "missing" {
			missing[path] = true
		}
	}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	manifestData = append(manifestData, '\n')
	manifestState, err := fileState(manifestPath, manifestData)
	if err != nil || manifestState == "conflict" {
		return writeError(streams.Error, "source_conflict")
	}
	if manifestState == "missing" {
		missing[manifestPath] = true
	}
	registrationState, err := getRegistration(ctx, runner)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return writeError(streams.Error, "Codex MCP state unavailable")
	}
	pluginRegistration := registrationState != nil && isPluginRegistration(*registrationState)
	if registrationState != nil && !sameRegistration(*registrationState, expected) && !pluginRegistration {
		return writeError(streams.Error, "source_conflict")
	}
	if !apply {
		return writeStatus(streams.Output, "preview", "install")
	}
	for path, data := range files {
		if !missing[path] {
			continue
		}
		if err := atomicWrite(path, data); err != nil {
			removeNewFiles(missing)
			return writeError(streams.Error, "integration write failed")
		}
	}
	if missing[manifestPath] && atomicWrite(manifestPath, manifestData) != nil {
		removeNewFiles(missing)
		return writeError(streams.Error, "integration write failed")
	}
	if registrationState == nil || pluginRegistration {
		args := []string{"mcp", "add", serverName, "--", expected.Command}
		args = append(args, expected.Args...)
		if _, err := runner.Run(ctx, "codex", args...); err != nil {
			removeNewFiles(missing)
			return writeError(streams.Error, "Codex MCP registration failed")
		}
		current, err := getRegistration(ctx, runner)
		if err != nil || current == nil || !sameRegistration(*current, expected) {
			_, _ = runner.Run(ctx, "codex", "mcp", "remove", serverName)
			removeNewFiles(missing)
			return writeError(streams.Error, "Codex MCP registration failed")
		}
	}
	return writeStatus(streams.Output, "applied", "install")
}

func removeNewFiles(paths map[string]bool) {
	for path := range paths {
		_ = os.Remove(path)
	}
}

func runUninstall(ctx context.Context, home, configPath string, apply bool, runner Runner, streams Streams) int {
	manifestPath := filepath.Join(home, filepath.FromSlash(manifestRel))
	data, err := readBounded(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return writeStatus(streams.Output, map[bool]string{true: "applied", false: "preview"}[apply], "uninstall_noop")
	}
	if err != nil {
		return writeError(streams.Error, "ownership unavailable")
	}
	var manifest ownershipManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.SchemaVersion != configVersion || manifest.ServerName != serverName || manifest.RegistrationHash == "" {
		return writeError(streams.Error, "ownership invalid")
	}
	paths := map[string]string{defaultConfigRel: filepath.Join(home, filepath.FromSlash(defaultConfigRel)), skillRel: filepath.Join(home, filepath.FromSlash(skillRel)), "config": configPath}
	for name, expectedHash := range manifest.Files {
		path := paths[name]
		if path == "" {
			return writeError(streams.Error, "ownership invalid")
		}
		content, err := readBounded(path)
		if err != nil || digest(content) != expectedHash {
			return writeError(streams.Error, "source_conflict")
		}
	}
	current, err := getRegistration(ctx, runner)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return writeError(streams.Error, "Codex MCP state unavailable")
	}
	if current != nil && registrationDigest(*current) != manifest.RegistrationHash {
		return writeError(streams.Error, "source_conflict")
	}
	if !apply {
		return writeStatus(streams.Output, "preview", "uninstall")
	}
	staged := []stagedOwnedFile{}
	for name, expectedHash := range manifest.Files {
		file, err := stageOwnedFile(paths[name], expectedHash)
		if err != nil {
			restoreStagedFiles(staged)
			return writeError(streams.Error, "owned file removal failed")
		}
		staged = append(staged, file)
	}
	if current != nil {
		if _, err := runner.Run(ctx, "codex", "mcp", "remove", serverName); err != nil {
			restoreStagedFiles(staged)
			return writeError(streams.Error, "Codex MCP removal failed")
		}
	}
	for _, file := range staged {
		if err := os.Remove(file.staged); err != nil && !errors.Is(err, os.ErrNotExist) {
			return writeError(streams.Error, "owned file removal failed")
		}
	}
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return writeError(streams.Error, "ownership removal failed")
	}
	return writeStatus(streams.Output, "applied", "uninstall")
}

type stagedOwnedFile struct {
	original string
	staged   string
}

func stageOwnedFile(path, expectedHash string) (stagedOwnedFile, error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".prompt-better-uninstall-*")
	if err != nil {
		return stagedOwnedFile{}, err
	}
	staged := temporary.Name()
	if err := temporary.Close(); err != nil {
		os.Remove(staged)
		return stagedOwnedFile{}, err
	}
	if err := os.Remove(staged); err != nil {
		return stagedOwnedFile{}, err
	}
	if err := os.Rename(path, staged); err != nil {
		return stagedOwnedFile{}, err
	}
	file := stagedOwnedFile{original: path, staged: staged}
	content, err := readBounded(staged)
	if err != nil || digest(content) != expectedHash {
		_ = os.Rename(staged, path)
		return stagedOwnedFile{}, errors.New("owned file changed during uninstall")
	}
	return file, nil
}

func restoreStagedFiles(files []stagedOwnedFile) {
	for index := len(files) - 1; index >= 0; index-- {
		_ = os.Rename(files[index].staged, files[index].original)
	}
}

// RunDoctor parses and executes prompt-better doctor without mutation.
func RunDoctor(ctx context.Context, args []string, streams Streams) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var format, codexHome, configPath string
	fs.StringVar(&format, "format", "text", "text or json")
	fs.StringVar(&codexHome, "codex-home", "", "Codex home")
	fs.StringVar(&configPath, "config", "", "integration config")
	if err := fs.Parse(args); err != nil || len(fs.Args()) != 0 || (format != "text" && format != "json") {
		return writeError(streams.Error, "invalid command flags")
	}
	home, err := resolveCodexHome(codexHome)
	if err != nil {
		return writeError(streams.Error, "invalid Codex home")
	}
	if configPath == "" {
		configPath = filepath.Join(home, filepath.FromSlash(defaultConfigRel))
	} else if !filepath.IsAbs(configPath) {
		return writeError(streams.Error, "config path must be absolute")
	}
	checks := doctorChecks(ctx, home, configPath, commandRunner{codexHome: home})
	ready := allReady(checks)
	if format == "json" {
		data, _ := json.Marshal(struct {
			Ready  bool          `json:"ready"`
			Checks []doctorCheck `json:"checks"`
		}{Ready: ready, Checks: checks})
		_, err = fmt.Fprintln(streams.Output, string(data))
	} else {
		for _, check := range checks {
			if _, err = fmt.Fprintf(streams.Output, "%s: %s\n", check.Name, check.State); err != nil {
				break
			}
		}
	}
	if err != nil {
		return 1
	}
	if !ready {
		return 1
	}
	return 0
}

type doctorCheck struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

func doctorChecks(ctx context.Context, home, configPath string, runner Runner) []doctorCheck {
	checks := []doctorCheck{{Name: "platform", State: platformState()}}
	configData, configErr := readBounded(configPath)
	var config IntegrationConfig
	configState := "missing"
	if configErr == nil && json.Unmarshal(configData, &config) == nil && validateStoredConfig(config) == nil {
		configState = "ready"
	} else if configErr != nil && !errors.Is(configErr, os.ErrNotExist) {
		configState = "unavailable"
	} else if configErr == nil {
		configState = "invalid"
	}
	checks = append(checks, doctorCheck{Name: "config", State: configState})
	manifestData, manifestErr := readBounded(filepath.Join(home, filepath.FromSlash(manifestRel)))
	manifestState, skillState := "missing", "unknown"
	if manifestErr == nil {
		var manifest ownershipManifest
		if json.Unmarshal(manifestData, &manifest) == nil && manifest.SchemaVersion == configVersion && manifest.ServerName == serverName && manifest.RegistrationHash != "" {
			manifestState = "ready"
			expected := manifest.Files[skillRel]
			skillData, err := readBounded(filepath.Join(home, filepath.FromSlash(skillRel)))
			if err == nil && digest(skillData) == expected {
				skillState = "ready"
			} else {
				skillState = "modified_or_missing"
			}
		} else {
			manifestState = "invalid"
		}
	}
	checks = append(checks, doctorCheck{Name: "ownership", State: manifestState}, doctorCheck{Name: "skill", State: skillState})
	registrationState := "missing"
	var current *registration
	current, registrationErr := getRegistration(ctx, runner)
	if registrationErr == nil && current != nil {
		registrationState = "present"
		if manifestErr == nil {
			var manifest ownershipManifest
			if json.Unmarshal(manifestData, &manifest) == nil && registrationDigest(*current) == manifest.RegistrationHash {
				registrationState = "ready"
			}
		}
	} else if registrationErr != nil && !errors.Is(registrationErr, os.ErrNotExist) {
		registrationState = "unknown"
	}
	checks = append(checks, doctorCheck{Name: "mcp_registration", State: registrationState})
	serverState := "unknown"
	if registrationState == "ready" {
		serverState = serverReadiness(ctx, *current)
	}
	checks = append(checks, doctorCheck{Name: "server", State: serverState})
	databaseState := "not_configured"
	collectorState := "unknown"
	if dsn := os.Getenv(databaseEnv); dsn != "" {
		databaseState = "unavailable"
		dbctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if repository, err := postgres.OpenRepository(dbctx, dsn, postgres.DefaultPoolConfig()); err == nil {
			result := repository.Doctor(dbctx)
			if result.Ready {
				databaseState = "ready"
				if configState == "ready" {
					if ready, err := repository.ConfiguredCollectors(dbctx, config.SourceKinds); err == nil && ready {
						collectorState = "ready"
					} else {
						collectorState = "missing"
					}
				}
			}
			repository.Close()
		}
		cancel()
	}
	checks = append(checks, doctorCheck{Name: "database", State: databaseState})
	if configState == "ready" && collectorState == "unknown" {
		collectorState = "missing"
	}
	checks = append(checks, doctorCheck{Name: "collectors", State: collectorState}, doctorCheck{Name: "host_capabilities", State: "unknown"})
	return checks
}

func validateStoredConfig(config IntegrationConfig) error {
	if config.SchemaVersion != configVersion || !policy.ValidExecutionPolicy(config.ExecutionPolicy) || config.RawPromptRetention || config.Telemetry || config.DatabaseEnv != databaseEnv || len(config.SourceKinds) == 0 {
		return errors.New("invalid integration config")
	}
	seen := map[string]bool{}
	for _, kind := range config.SourceKinds {
		if !sourceKinds[kind] || seen[kind] {
			return errors.New("invalid source kind")
		}
		seen[kind] = true
	}
	if seen["process"] != (config.ProcessPurpose != "") {
		return errors.New("invalid process purpose")
	}
	return nil
}

func serverReadiness(ctx context.Context, value registration) string {
	probeContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	commandName := value.Command
	commandArgs := append([]string(nil), value.Args...)
	if value.Command == "go" {
		if _, err := exec.LookPath("go"); err != nil || len(value.Args) < 4 || value.Args[0] != "-C" || value.Args[2] != "run" || value.Args[3] != "./cmd/prompt-better-mcp" {
			return "unavailable"
		}
		if _, err := regularDirectory(value.Args[1]); err != nil {
			return "unavailable"
		}
		if info, err := os.Stat(filepath.Join(value.Args[1], "cmd", "prompt-better-mcp")); err != nil || !info.IsDir() {
			return "unavailable"
		}
		versionCommand := exec.CommandContext(probeContext, "go", "version")
		versionCommand.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		version, err := versionCommand.Output()
		if err != nil || !supportedGoVersion(string(version)) {
			return "unavailable"
		}
		temp, err := os.MkdirTemp("", "prompt-better-doctor-*")
		if err != nil {
			return "unavailable"
		}
		defer os.RemoveAll(temp)
		commandName = filepath.Join(temp, "prompt-better-mcp")
		if runtime.GOOS == "windows" {
			commandName += ".exe"
		}
		build := exec.CommandContext(probeContext, "go", "build", "-mod=readonly", "-trimpath", "-o", commandName, "./cmd/prompt-better-mcp")
		build.Dir = value.Args[1]
		build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off")
		if build.Run() != nil {
			return "unavailable"
		}
		commandArgs = append([]string(nil), value.Args[4:]...)
	} else {
		path, err := regularFile(value.Command)
		if err != nil {
			return "unavailable"
		}
		info, _ := os.Stat(path)
		if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
			return "unavailable"
		}
		commandName = path
	}

	command := exec.CommandContext(probeContext, commandName, commandArgs...)
	command.Dir = value.Cwd
	command.Env = os.Environ()
	command.Stderr = io.Discard
	client := mcp.NewClient(&mcp.Implementation{Name: "prompt-better-doctor", Version: configVersion}, nil)
	session, err := client.Connect(probeContext, &mcp.CommandTransport{Command: command, TerminateDuration: time.Second}, nil)
	if err != nil {
		return "unavailable"
	}
	defer session.Close()
	listed, err := session.ListTools(probeContext, nil)
	if err != nil || len(listed.Tools) != 8 {
		return "unavailable"
	}
	required := map[string]bool{
		"improve_prompt": true, "create_goal_prompt": true, "create_review_fix_prompt": true,
		"lint_prompt": true, "get_checkpoint": true, "audit_session": true,
		"audit_project": true, "render_governance_report": true,
	}
	for _, tool := range listed.Tools {
		delete(required, tool.Name)
	}
	if len(required) != 0 {
		return "unavailable"
	}
	return "ready"
}

func supportedGoVersion(output string) bool {
	fields := strings.Fields(output)
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "go") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(fields[2], "go"), ".")
	if len(parts) < 3 {
		return false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	patch, patchErr := strconv.Atoi(parts[2])
	return majorErr == nil && minorErr == nil && patchErr == nil &&
		(major > 1 || major == 1 && (minor > 25 || minor == 25 && patch >= 12))
}

func getRegistration(ctx context.Context, runner Runner) (*registration, error) {
	data, err := runner.Run(ctx, "codex", "mcp", "get", serverName, "--json")
	if err != nil {
		lower := strings.ToLower(string(data))
		if strings.Contains(lower, "not found") || strings.Contains(lower, "no mcp") {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("registration query failed")
	}
	var document struct {
		Command   string   `json:"command"`
		Args      []string `json:"args"`
		Cwd       string   `json:"cwd"`
		Transport struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Cwd     string   `json:"cwd"`
		} `json:"transport"`
	}
	if json.Unmarshal(data, &document) != nil {
		return nil, errors.New("registration response invalid")
	}
	if document.Transport.Command != "" {
		if document.Transport.Type != "stdio" {
			return nil, errors.New("registration response invalid")
		}
		document.Command = document.Transport.Command
		document.Args = document.Transport.Args
		document.Cwd = document.Transport.Cwd
	}
	if document.Command == "" {
		return nil, errors.New("registration response invalid")
	}
	return &registration{Command: document.Command, Args: document.Args, Cwd: document.Cwd}, nil
}

func sameRegistration(left, right registration) bool {
	return left.Command == right.Command &&
		slices.Equal(left.Args, right.Args) &&
		left.Cwd == right.Cwd
}

func isPluginRegistration(value registration) bool {
	if value.Command != "go" || len(value.Args) != 2 || value.Args[0] != "run" ||
		value.Args[1] != "./cmd/prompt-better-mcp" || value.Cwd == "" {
		return false
	}
	pluginData, pluginErr := readBounded(filepath.Join(value.Cwd, ".codex-plugin", "plugin.json"))
	mcpData, mcpErr := readBounded(filepath.Join(value.Cwd, ".mcp.json"))
	if pluginErr != nil || mcpErr != nil {
		return false
	}
	var plugin struct {
		Name       string `json:"name"`
		MCPServers string `json:"mcpServers"`
	}
	var manifest struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(pluginData, &plugin) != nil || json.Unmarshal(mcpData, &manifest) != nil {
		return false
	}
	server, ok := manifest.MCPServers[serverName]
	return ok && plugin.Name == "prompt-better" && plugin.MCPServers == "./.mcp.json" &&
		server.Command == value.Command && slices.Equal(server.Args, value.Args)
}

func registrationDigest(value registration) string {
	data, _ := json.Marshal(value)
	return digest(data)
}

func resolveCodexHome(value string) (string, error) {
	if value == "" {
		value = os.Getenv("CODEX_HOME")
	}
	if value == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		value = filepath.Join(userHome, ".codex")
	}
	if !filepath.IsAbs(value) {
		return "", errors.New("Codex home must be absolute")
	}
	return filepath.Clean(value), nil
}

func regularDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return "", errors.New("directory unavailable")
	}
	return clean, nil
}

func regularFile(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("file unavailable")
	}
	return clean, nil
}

func fileState(path string, expected []byte) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("file invalid")
	}
	actual, err := readBounded(path)
	if err != nil {
		return "", err
	}
	if bytes.Equal(actual, expected) && (runtime.GOOS == "windows" || info.Mode().Perm() == 0o600) {
		return "same", nil
	}
	return "conflict", nil
}

func readBounded(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil, errors.New("file invalid")
	}
	return os.ReadFile(path)
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".prompt-better-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryName, path); err != nil {
		return err
	}
	if err := os.Remove(temporaryName); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func ownedName(home, configPath, path string) string {
	if path == configPath && filepath.Clean(configPath) != filepath.Join(home, filepath.FromSlash(defaultConfigRel)) {
		return "config"
	}
	relative, _ := filepath.Rel(home, path)
	return filepath.ToSlash(relative)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func platformState() string {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		return "supported"
	default:
		return "unknown"
	}
}

func allReady(checks []doctorCheck) bool {
	for _, check := range checks {
		switch check.Name {
		case "platform":
			if check.State != "supported" {
				return false
			}
		case "collectors":
			if check.State != "ready" {
				return false
			}
		case "host_capabilities":
			continue
		default:
			if check.State != "ready" {
				return false
			}
		}
	}
	return true
}

func writeError(output io.Writer, message string) int {
	if _, err := fmt.Fprintln(output, "error:", message); err != nil {
		return 1
	}
	return 2
}

func writeStatus(output io.Writer, state, action string) int {
	if _, err := fmt.Fprintf(output, "%s: %s\n", state, action); err != nil {
		return 1
	}
	return 0
}
