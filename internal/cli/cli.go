package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nijanthan-dev/codex-prompt-better/internal/audit"
	"github.com/nijanthan-dev/codex-prompt-better/internal/boundary"
	"github.com/nijanthan-dev/codex-prompt-better/internal/compiler"
	"github.com/nijanthan-dev/codex-prompt-better/internal/config"
	"github.com/nijanthan-dev/codex-prompt-better/internal/lint"
	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
	"github.com/nijanthan-dev/codex-prompt-better/internal/policypack"
	"github.com/nijanthan-dev/codex-prompt-better/internal/setup"
	"github.com/nijanthan-dev/codex-prompt-better/internal/store/postgres"
	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

const (
	exitOK         = 0
	exitInternal   = 1
	exitInvalid    = 2
	exitSemantic   = 3
	exitPermission = 4
	exitApproval   = 5
	exitBudget     = 124
)

// Streams are the owned CLI input and output streams. Run closes Input.
// Input.Close must unblock a pending Input.Read.
type Streams struct {
	Input  io.ReadCloser
	Output io.Writer
	Error  io.Writer
}

type onceReadCloser struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (r *onceReadCloser) Close() error {
	r.once.Do(func() { r.err = r.ReadCloser.Close() })
	return r.err
}

type options struct {
	configPath       string
	inputPath        string
	format           string
	executionPolicy  string
	hostPermission   string
	timeout          string
	phase            string
	reviewHead       string
	maxInputBytes    int
	positional       []string
	isRequestJSON    bool
	isShowProvenance bool
	isFormatSet      bool
	isPolicySet      bool
	isHostSet        bool
	isTimeoutSet     bool
	isMaxInputSet    bool
	isPhaseSet       bool
	contextRoot      string
	scopes           stringList
	policyPacks      stringList
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type runner struct {
	ctx     context.Context
	input   []byte
	options options
	config  config.Config
	streams Streams
}

// Run executes one deterministic CLI command and returns its process exit code.
func Run(parent context.Context, args []string, streams Streams) int {
	if streams.Input == nil || streams.Output == nil || streams.Error == nil {
		return exitInternal
	}
	input := &onceReadCloser{ReadCloser: streams.Input}
	defer func() { _ = input.Close() }()
	if len(args) == 0 {
		if writeUsage(streams.Error) != exitOK {
			return exitInternal
		}
		return exitInvalid
	}
	command := args[0]
	if command == "help" || command == "--help" || command == "-h" {
		return writeUsage(streams.Output)
	}
	if command == "doctor" {
		return setup.RunDoctor(parent, args[1:], setup.Streams{Output: streams.Output, Error: streams.Error})
	}
	if command == "init" {
		return setup.RunInit(parent, args[1:], setup.Streams{Output: streams.Output, Error: streams.Error})
	}
	if command == "audit" {
		return runAudit(parent, args[1:], streams)
	}
	if !validCommand(command) {
		err := contracts.NewError(contracts.ErrorCodeInvalidSchema, "unknown command", "command", false)
		return emitError(streams.Error, "text", err)
	}

	opt, parseErr := parseOptions(command, args[1:])
	if parseErr != nil {
		return emitError(streams.Error, requestedErrorFormat(opt), parseErr)
	}
	cfg, loadErr := loadConfig(parent, opt)
	if loadErr != nil {
		return emitError(streams.Error, requestedErrorFormat(opt), loadErr)
	}
	if opt.isShowProvenance {
		if _, err := fmt.Fprintln(streams.Error, formatProvenance(cfg.Provenance)); err != nil {
			return exitInternal
		}
	}

	ctx, cancel := context.WithTimeout(parent, cfg.Timeout)
	defer cancel()
	data, readErr := readInput(ctx, input, opt.inputPath, opt.positional, cfg.MaxInputBytes)
	if readErr != nil {
		return emitError(streams.Error, cfg.Format, readErr)
	}
	r := runner{ctx: ctx, input: data, options: opt, config: cfg, streams: streams}
	switch command {
	case "improve_prompt":
		return r.runImprove()
	case "create_goal_prompt":
		return r.runGoal()
	case "create_review_fix_prompt":
		return r.runReview()
	case "lint_prompt":
		return r.runLint()
	default:
		return exitInternal
	}
}

func requestedErrorFormat(opt options) string {
	if opt.isFormatSet && opt.format == "json" {
		return "json"
	}
	return "text"
}

func parseOptions(command string, args []string) (options, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opt options
	fs.StringVar(&opt.configPath, "config", "", "explicit JSON configuration file")
	fs.StringVar(&opt.inputPath, "input", "", "input file; default stdin or positional text")
	fs.StringVar(&opt.format, "format", "", "text or json")
	fs.StringVar(&opt.executionPolicy, "execution-policy", "", "execution policy")
	fs.StringVar(&opt.hostPermission, "host-permission", "", "host permission")
	fs.StringVar(&opt.timeout, "timeout", "", "positive duration up to 1m")
	fs.StringVar(&opt.phase, "phase", "design", "active phase")
	fs.StringVar(&opt.reviewHead, "review-head", "", "review commit hash")
	fs.IntVar(&opt.maxInputBytes, "max-input-bytes", 0, "input byte limit")
	fs.BoolVar(&opt.isRequestJSON, "request-json", false, "parse frozen v1 request JSON")
	fs.BoolVar(&opt.isShowProvenance, "show-provenance", false, "print configuration source names")
	fs.StringVar(&opt.contextRoot, "context-root", "", "explicit discovery root")
	fs.Var(&opt.scopes, "scope", "relative requested scope; repeatable")
	fs.Var(&opt.policyPacks, "policy-pack", "extension policy pack; repeatable")
	parseErr := fs.Parse(args)
	fs.Visit(func(item *flag.Flag) {
		switch item.Name {
		case "format":
			opt.isFormatSet = true
		case "execution-policy":
			opt.isPolicySet = true
		case "host-permission":
			opt.isHostSet = true
		case "timeout":
			opt.isTimeoutSet = true
		case "max-input-bytes":
			opt.isMaxInputSet = true
		case "phase":
			opt.isPhaseSet = true
		}
	})
	if parseErr != nil {
		return opt, contracts.NewError(
			contracts.ErrorCodeInvalidSchema,
			"invalid command flags",
			"flags",
			false,
		)
	}
	opt.positional = append([]string{}, fs.Args()...)
	discovery := opt.contextRoot != "" || len(opt.scopes) > 0 || len(opt.policyPacks) > 0
	if discovery && command != "improve_prompt" && command != "lint_prompt" {
		return opt, contracts.NewError(contracts.ErrorCodeInvalidSchema, "boundary flags unsupported for command", "flags", false)
	}
	if discovery && opt.contextRoot == "" {
		return opt, contracts.NewError(contracts.ErrorCodeInvalidSchema, "context root required for boundary discovery", "context_root", false)
	}
	return opt, nil
}

func loadConfig(parent context.Context, opt options) (config.Config, error) {
	cfg := config.Default()
	if opt.configPath != "" {
		setupCtx, cancel := context.WithTimeout(parent, cfg.Timeout)
		loaded, err := config.LoadFile(setupCtx, opt.configPath)
		cancel()
		if err != nil {
			return config.Config{}, err
		}
		cfg = loaded
	}
	if opt.isFormatSet {
		cfg.Format = opt.format
		cfg.Provenance["format"] = "cli"
	}
	if opt.isPolicySet {
		cfg.ExecutionPolicy = contracts.ExecutionPolicy(opt.executionPolicy)
		cfg.Provenance["execution_policy"] = "cli"
	}
	if opt.isHostSet {
		cfg.HostPermission = policy.HostPermission(opt.hostPermission)
		cfg.Provenance["host_permission"] = "cli"
	}
	if opt.isMaxInputSet {
		cfg.MaxInputBytes = opt.maxInputBytes
		cfg.Provenance["max_input_bytes"] = "cli"
	}
	if opt.isTimeoutSet {
		cfg.TimeoutText = opt.timeout
		cfg.Provenance["timeout"] = "cli"
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func (r runner) runImprove() int {
	var request contracts.ImprovePromptRequest
	if r.options.isRequestJSON {
		if err := decodeStrict(r.input, &request); err != nil {
			return r.emitError(err)
		}
		for _, path := range [][]string{{"prompt_plan"}, {"budget"}, {"budget", "context_mode"}} {
			if err := rejectJSONNull(r.input, path...); err != nil {
				return r.emitError(err)
			}
		}
		if request.Budget != nil && request.Budget.ContextMode == "" {
			if _, ok := jsonValueAtPath(r.input, "budget", "context_mode"); ok {
				return r.emitError(contracts.NewError(
					contracts.ErrorCodeInvalidSchema,
					"context_mode must be a supported string",
					"budget.context_mode",
					false,
				))
			}
		}
		if err := rejectEmptyJSONList(r.input, "prompt_plan", "artifact_priorities"); err != nil {
			return r.emitError(err)
		}
		if err := compiler.ValidateImproveRequest(request); err != nil {
			return r.emitError(err)
		}
		if source := r.config.Provenance["execution_policy"]; source == "file" || source == "cli" {
			request.ExecutionPolicy = r.config.ExecutionPolicy
		}
	} else {
		request = contracts.ImprovePromptRequest{
			SchemaVersion:   contracts.SchemaVersion,
			Kind:            "request",
			Intent:          string(r.input),
			ExecutionPolicy: r.config.ExecutionPolicy,
		}
	}
	decisions, discoveryErr := r.boundaryDecisions()
	if discoveryErr != nil {
		return r.emitError(discoveryErr)
	}
	if r.options.contextRoot != "" {
		if request.PromptPlan == nil {
			plan := compiler.NewPlan(request.Intent)
			request.PromptPlan = &plan
		}
		for _, scope := range r.options.scopes {
			request.PromptPlan.Scope = appendUnique(request.PromptPlan.Scope, scope)
		}
		for _, decision := range decisions {
			switch decision.Category {
			case "non_goal":
				request.PromptPlan.NonGoals = appendUnique(request.PromptPlan.NonGoals, decision.Explanation)
			case "validation", "release":
				request.PromptPlan.Gates = appendUnique(request.PromptPlan.Gates, decision.Explanation)
			}
		}
	}
	var result contracts.ImprovePromptResult
	var err error
	if r.options.isPhaseSet {
		result, err = compiler.ImproveWithPhase(
			r.ctx, request, r.config.HostPermission, r.options.phase,
		)
	} else {
		result, err = compiler.Improve(r.ctx, request, r.config.HostPermission)
	}
	if err != nil {
		return r.emitError(err)
	}
	result.BoundaryDecisions = decisions
	return r.emitResult(result, result.ImprovedPrompt)
}

func (r runner) runGoal() int {
	var request contracts.CreateGoalPromptRequest
	if r.options.isRequestJSON {
		if err := decodeStrict(r.input, &request); err != nil {
			return r.emitError(err)
		}
		if err := rejectEmptyJSONList(r.input, "prompt_plan", "artifact_priorities"); err != nil {
			return r.emitError(err)
		}
	} else {
		result, err := compiler.CreateGoalFromObjective(r.ctx, string(r.input))
		if err != nil {
			return r.emitError(err)
		}
		return r.emitResult(result, result.GoalPrompt)
	}
	result, err := compiler.CreateGoal(r.ctx, request)
	if err != nil {
		return r.emitError(err)
	}
	return r.emitResult(result, result.GoalPrompt)
}

func (r runner) runReview() int {
	var request contracts.CreateReviewFixPromptRequest
	if r.options.isRequestJSON {
		if err := decodeStrict(r.input, &request); err != nil {
			return r.emitError(err)
		}
	} else {
		request = contracts.CreateReviewFixPromptRequest{
			SchemaVersion: contracts.SchemaVersion,
			Kind:          "request",
			Findings:      nonEmptyLines(string(r.input)),
			ReviewHead:    r.options.reviewHead,
		}
	}
	result, err := compiler.CreateReviewFix(r.ctx, request)
	if err != nil {
		return r.emitError(err)
	}
	return r.emitResult(result, result.FixPrompt)
}

func (r runner) runLint() int {
	var request contracts.LintPromptRequest
	if r.options.isRequestJSON {
		if err := decodeStrict(r.input, &request); err != nil {
			return r.emitError(err)
		}
	} else {
		request = contracts.LintPromptRequest{
			SchemaVersion: contracts.SchemaVersion,
			Kind:          "request",
			Candidate:     string(r.input),
		}
	}
	result, err := lint.CheckPrompt(request)
	if err != nil {
		return r.emitError(err)
	}
	decisions, discoveryErr := r.boundaryDecisions()
	if discoveryErr != nil {
		return r.emitError(discoveryErr)
	}
	result.BoundaryDecisions = decisions
	result = addBoundaryDiagnostics(result, decisions)
	if err := r.writeLint(result); err != nil {
		return r.outputFailure()
	}
	if !result.Valid {
		return exitSemantic
	}
	return exitOK
}

func addBoundaryDiagnostics(result contracts.LintPromptResult, decisions []contracts.BoundaryDecision) contracts.LintPromptResult {
	const maxDiagnostics = 100
	for _, decision := range decisions {
		if decision.Outcome == "continue" {
			continue
		}
		severity := "warning"
		if decision.Outcome == "block" {
			severity = "error"
			result.Valid = false
		}
		if len(result.Diagnostics) < maxDiagnostics {
			result.Diagnostics = append(result.Diagnostics, contracts.Diagnostic{Code: "boundary-" + decision.Outcome, Severity: severity, Message: decision.Explanation, Location: decision.SourceRef, Rationale: "Repository boundary policy applies.", Remediation: "Respect the boundary decision before proceeding."})
		}
	}
	return result
}

func readInput(ctx context.Context, stdin io.ReadCloser, path string, args []string, limit int) ([]byte, error) {
	if path != "" && len(args) > 0 {
		return nil, inputError(
			contracts.ErrorCodeInvalidSchema,
			"use either positional input or --input",
			false,
		)
	}
	if len(args) > 0 {
		if err := stdin.Close(); err != nil {
			return nil, contracts.NewError(contracts.ErrorCodeInternal, "input close failed", "input", true)
		}
		return validateInput([]byte(strings.Join(args, " ")), limit)
	}
	reader := stdin
	if path != "" {
		if err := stdin.Close(); err != nil {
			return nil, contracts.NewError(contracts.ErrorCodeInternal, "input close failed", "input", true)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, contracts.NewError(contracts.ErrorCodeNotFound, "input file unavailable", "input", false)
		}
		if !info.Mode().IsRegular() {
			return nil, contracts.NewError(contracts.ErrorCodeInvalidSchema, "input must be a regular file", "input", false)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, contracts.NewError(contracts.ErrorCodeNotFound, "input file unavailable", "input", false)
		}
		reader = file
	}
	data, err := readOwned(ctx, reader, limit)
	if err != nil {
		return nil, err
	}
	return validateInput(data, limit)
}

func readOwned(ctx context.Context, reader io.ReadCloser, limit int) ([]byte, error) {
	if ctx.Err() != nil {
		if err := reader.Close(); err != nil {
			return nil, contracts.NewError(contracts.ErrorCodeInternal, "input close failed", "input", true)
		}
		return nil, contracts.NewError(contracts.ErrorCodeBudgetExhausted, "input read cancelled or timed out", "input", true)
	}
	type readResult struct {
		data []byte
		err  error
	}
	result := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
		result <- readResult{data: data, err: err}
	}()
	select {
	case <-ctx.Done():
		closeErr := reader.Close()
		<-result
		if closeErr != nil {
			return nil, contracts.NewError(contracts.ErrorCodeInternal, "input close failed", "input", true)
		}
		return nil, contracts.NewError(contracts.ErrorCodeBudgetExhausted, "input read cancelled or timed out", "input", true)
	case read := <-result:
		closeErr := reader.Close()
		if read.err != nil {
			return nil, contracts.NewError(contracts.ErrorCodeInternal, "input read failed", "input", true)
		}
		if closeErr != nil {
			return nil, contracts.NewError(contracts.ErrorCodeInternal, "input close failed", "input", true)
		}
		if ctx.Err() != nil {
			return nil, inputError(
				contracts.ErrorCodeBudgetExhausted,
				"input read cancelled or timed out",
				true,
			)
		}
		return read.data, nil
	}
}

func validateInput(data []byte, limit int) ([]byte, error) {
	if len(data) > limit {
		return nil, inputError(
			contracts.ErrorCodeInvalidSchema,
			"input exceeds configured byte limit",
			false,
		)
	}
	if !utf8.Valid(data) {
		return nil, contracts.NewError(contracts.ErrorCodeInvalidSchema, "input must be valid utf-8", "input", false)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, contracts.NewError(contracts.ErrorCodeInvalidSchema, "input must not be empty", "input", false)
	}
	return data, nil
}

func inputError(code contracts.ErrorCode, message string, retryable bool) error {
	return contracts.NewError(code, message, "input", retryable)
}

func decodeStrict(data []byte, target any) error {
	if !utf8.Valid(data) {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "request must be valid utf-8", "request", false)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid request json", "request", false)
	}
	var extra any
	err := decoder.Decode(&extra)
	if err == nil {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "request contains multiple json values", "request", false)
	}
	if !errors.Is(err, io.EOF) {
		return contracts.NewError(contracts.ErrorCodeInvalidSchema, "invalid trailing request data", "request", false)
	}
	return nil
}

func rejectJSONNull(data []byte, path ...string) error {
	value, ok := jsonValueAtPath(data, path...)
	if !ok || !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil
	}
	return contracts.NewError(
		contracts.ErrorCodeInvalidSchema,
		path[len(path)-1]+" must not be null",
		strings.Join(path, "."),
		false,
	)
}

func rejectEmptyJSONList(data []byte, path ...string) error {
	value, ok := jsonValueAtPath(data, path...)
	if !ok {
		return nil
	}
	var items []json.RawMessage
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) ||
		(json.Unmarshal(value, &items) == nil && len(items) == 0) {
		return contracts.NewError(
			contracts.ErrorCodeInvalidSchema,
			path[len(path)-1]+" must contain at least one item",
			strings.Join(path, "."),
			false,
		)
	}
	return nil
}

func jsonValueAtPath(data []byte, path ...string) (json.RawMessage, bool) {
	current := json.RawMessage(data)
	for _, segment := range path {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(current, &object); err != nil {
			return nil, false
		}
		next, ok := object[segment]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func (r runner) emitResult(value any, text string) int {
	var err error
	if r.config.Format == "json" {
		err = writeJSON(r.streams.Output, value)
	} else {
		_, err = fmt.Fprintln(r.streams.Output, text)
	}
	if err != nil {
		return r.outputFailure()
	}
	return exitOK
}

func (r runner) emitError(err error) int { return emitError(r.streams.Error, r.config.Format, err) }

func (r runner) writeLint(result contracts.LintPromptResult) error {
	if r.config.Format == "json" {
		return writeJSON(r.streams.Output, result)
	}
	if len(result.Diagnostics) == 0 {
		_, err := fmt.Fprintln(r.streams.Output, "valid")
		return err
	}
	for _, diagnostic := range result.Diagnostics {
		if _, err := fmt.Fprintf(
			r.streams.Output,
			"%s %s %s: %s\n",
			diagnostic.Severity,
			diagnostic.Code,
			diagnostic.Location,
			diagnostic.Message,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r runner) outputFailure() int {
	err := contracts.NewError(contracts.ErrorCodeInternal, "output write failed", "output", true)
	if writeErr := writeStableError(r.streams.Error, "text", err); writeErr != nil {
		return exitInternal
	}
	return exitInternal
}

func emitError(output io.Writer, format string, err error) int {
	stable := asStable(err)
	if writeErr := writeStableError(output, format, stable); writeErr != nil {
		return exitInternal
	}
	return exitFor(stable)
}

func writeStableError(output io.Writer, format string, err *contracts.StableError) error {
	if format == "json" {
		return writeJSON(output, err)
	}
	_, writeErr := fmt.Fprintln(output, err.Error())
	return writeErr
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func asStable(err error) *contracts.StableError {
	var stable *contracts.StableError
	if errors.As(err, &stable) {
		return stable
	}
	return contracts.NewError(contracts.ErrorCodeInternal, "internal error", "", true)
}

func exitFor(err *contracts.StableError) int {
	switch err.Code {
	case contracts.ErrorCodeInvalidSchema,
		contracts.ErrorCodeNotFound,
		contracts.ErrorCodeUnsupportedCapability,
		contracts.ErrorCodeUnknownCapability:
		return exitInvalid
	case contracts.ErrorCodeSemanticInvalid:
		return exitSemantic
	case contracts.ErrorCodePermissionDenied:
		return exitPermission
	case contracts.ErrorCodeApprovalRequired:
		return exitApproval
	case contracts.ErrorCodeBudgetExhausted:
		return exitBudget
	default:
		return exitInternal
	}
}

func nonEmptyLines(value string) []string {
	values := []string{}
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if line != "" {
			values = append(values, line)
		}
	}
	return values
}

func (r runner) boundaryDecisions() ([]contracts.BoundaryDecision, error) {
	if r.options.contextRoot == "" {
		return nil, nil
	}
	root := filepath.Clean(r.options.contextRoot)
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, contracts.NewError(contracts.ErrorCodeSemanticInvalid, "context root must be a real directory", "context_root", false)
	}
	extensions := make([]policypack.Pack, 0, len(r.options.policyPacks))
	if len(r.options.policyPacks) > policypack.MaxPacks {
		return nil, contracts.NewError(contracts.ErrorCodeInvalidSchema, "too many extension policy packs", "policy_pack", false)
	}
	for _, name := range r.options.policyPacks {
		packInfo, statErr := os.Lstat(name)
		if statErr != nil || !packInfo.Mode().IsRegular() || packInfo.Mode()&os.ModeSymlink != 0 || packInfo.Size() > policypack.MaxPackBytes {
			return nil, contracts.NewError(contracts.ErrorCodeInvalidSchema, "policy pack must be a bounded regular file", "policy_pack", false)
		}
		data, readErr := os.ReadFile(name)
		if readErr != nil {
			return nil, contracts.NewError(contracts.ErrorCodeNotFound, "policy pack unavailable", "policy_pack", false)
		}
		pack, parseErr := policypack.Parse(data)
		if parseErr != nil {
			return nil, parseErr
		}
		extensions = append(extensions, pack)
	}
	ignored, err := ignoredRepositoryPaths(r.ctx, root)
	if err != nil {
		return nil, err
	}
	discovered, err := boundary.Discover(r.ctx, boundary.FSReader{FS: os.DirFS(root), IgnoredPaths: ignored}, r.options.scopes)
	if err != nil {
		return nil, err
	}
	return boundary.EvaluateContext(discovered, extensions)
}

const maxIgnoredMetadataBytes = 1 << 20

type boundedOutput struct {
	bytes.Buffer
	overflow bool
}

func (output *boundedOutput) Write(data []byte) (int, error) {
	remaining := maxIgnoredMetadataBytes - output.Len()
	if len(data) > remaining {
		output.overflow = true
		return 0, errors.New("ignored metadata limit exceeded")
	}
	return output.Buffer.Write(data)
}

func ignoredRepositoryPaths(ctx context.Context, root string) (map[string]struct{}, error) {
	if !hasGitMarker(root) {
		return map[string]struct{}{}, nil
	}
	output := &boundedOutput{}
	command := exec.CommandContext(ctx, "git", "-c", "core.fsmonitor=false", "-C", root, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, contracts.NewError(contracts.ErrorCodeBudgetExhausted, "ignore discovery cancelled or timed out", "context_root", true)
		}
		if output.overflow {
			return nil, contracts.NewError(contracts.ErrorCodeSemanticInvalid, "ignored metadata exceeds bounded limit", "context_root", false)
		}
		return nil, contracts.NewError(contracts.ErrorCodeSemanticInvalid, "repository ignore metadata unavailable", "context_root", false)
	}
	return parseIgnoredPaths(output.Bytes())
}

func hasGitMarker(root string) bool {
	current := filepath.Clean(root)
	for depth := 0; depth < boundary.MaxScopeDepth; depth++ {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return false
}

func parseIgnoredPaths(data []byte) (map[string]struct{}, error) {
	ignored := make(map[string]struct{})
	for _, raw := range bytes.Split(data, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		name := strings.TrimSuffix(filepath.ToSlash(string(raw)), "/")
		clean := filepath.ToSlash(filepath.Clean(name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, contracts.NewError(contracts.ErrorCodeSemanticInvalid, "invalid ignored metadata", "context_root", false)
		}
		ignored[clean] = struct{}{}
		if len(ignored) > boundary.MaxMetadataEntries {
			return nil, contracts.NewError(contracts.ErrorCodeSemanticInvalid, "ignored metadata exceeds entry limit", "context_root", false)
		}
	}
	return ignored, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func validCommand(command string) bool {
	switch command {
	case "improve_prompt", "create_goal_prompt", "create_review_fix_prompt", "lint_prompt":
		return true
	default:
		return false
	}
}

func runAudit(ctx context.Context, args []string, streams Streams) int {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var scope, reference, startsAt, endsAt, asOf, format string
	var sources stringList
	var consent bool
	fs.StringVar(&scope, "scope", "", "portfolio, project, task, or trajectory")
	fs.StringVar(&reference, "reference", "", "opaque local scope reference")
	fs.StringVar(&startsAt, "starts-at", "", "inclusive RFC3339 window start")
	fs.StringVar(&endsAt, "ends-at", "", "exclusive RFC3339 window end")
	fs.StringVar(&asOf, "as-of", "", "immutable RFC3339 audit watermark")
	fs.Var(&sources, "source", "configured local source kind; repeatable")
	fs.StringVar(&format, "format", "json", "json or text")
	fs.BoolVar(&consent, "consent", false, "explicitly consent to configured local evidence")
	if err := fs.Parse(args); err != nil || len(fs.Args()) != 0 {
		return emitError(streams.Error, "text", contracts.NewError(
			contracts.ErrorCodeInvalidSchema,
			"invalid audit flags",
			"flags",
			false,
		))
	}
	start, startErr := time.Parse(time.RFC3339Nano, startsAt)
	end, endErr := time.Parse(time.RFC3339Nano, endsAt)
	watermark, asOfErr := time.Parse(time.RFC3339Nano, asOf)
	if startErr != nil || endErr != nil || asOfErr != nil ||
		(format != "json" && format != "text") {
		return emitError(streams.Error, "text", contracts.NewError(
			contracts.ErrorCodeInvalidSchema,
			"invalid audit window or format",
			"flags",
			false,
		))
	}
	auditConsent := contracts.AuditConsentUnknown
	if consent {
		auditConsent = contracts.AuditConsentGranted
	}
	request := contracts.AuditProjectRequest{
		SchemaVersion: contracts.SchemaVersion, Kind: "request",
		Scope: contracts.AuditScope(scope), Reference: reference,
		ConfiguredSources: append([]string{}, sources...),
		StartsAt:          start, EndsAt: end, AsOf: watermark, Consent: auditConsent,
	}
	dsn := os.Getenv("PROMPT_BETTER_DATABASE_URL")
	if dsn == "" {
		return emitError(streams.Error, format, contracts.NewError(
			contracts.ErrorCodeCoverageIncomplete,
			"project audit store unavailable",
			"reference",
			true,
		))
	}
	repository, err := postgres.OpenRepository(ctx, dsn, postgres.DefaultPoolConfig())
	if err != nil {
		return emitError(streams.Error, format, contracts.NewError(
			contracts.ErrorCodeCoverageIncomplete,
			"project audit store unavailable",
			"reference",
			true,
		))
	}
	defer repository.Close()
	result, err := repository.AuditProject(ctx, request)
	if err != nil {
		return emitError(streams.Error, format, err)
	}
	if format == "json" {
		encoded, err := json.Marshal(result)
		if err != nil {
			return exitInternal
		}
		if len(encoded)+1 > audit.MaxResultBytes {
			return emitError(streams.Error, format, contracts.NewError(
				contracts.ErrorCodeBudgetExhausted, "audit result exceeds output budget",
				"result", false))
		}
		if _, err := streams.Output.Write(append(encoded, '\n')); err != nil {
			return exitInternal
		}
		return exitOK
	}
	if _, err := fmt.Fprintf(
		streams.Output,
		"audit %s coverage=%s metrics=%d findings=%d recommendations=%d\n",
		result.AuditReference,
		result.Coverage,
		len(result.Metrics),
		len(result.Findings),
		len(result.Recommendations),
	); err != nil {
		return exitInternal
	}
	return exitOK
}

func writeUsage(output io.Writer) int {
	_, err := fmt.Fprintln(
		output,
		"usage: prompt-better <improve_prompt|create_goal_prompt|create_review_fix_prompt|lint_prompt|audit|doctor|init> [flags] [text]",
	)
	if err != nil {
		return exitInternal
	}
	return exitOK
}

func formatProvenance(values map[string]string) string {
	fields := []string{"execution_policy", "host_permission", "max_input_bytes", "timeout", "format"}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, field+"="+values[field])
	}
	return "config provenance: " + strings.Join(parts, ", ")
}
