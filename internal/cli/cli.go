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
	"strings"
	"unicode/utf8"

	"github.com/nijanthan-dev/codex-prompt-better/internal/compiler"
	"github.com/nijanthan-dev/codex-prompt-better/internal/config"
	"github.com/nijanthan-dev/codex-prompt-better/internal/lint"
	"github.com/nijanthan-dev/codex-prompt-better/internal/policy"
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
	if len(args) == 0 {
		return writeUsage(streams.Error)
	}
	command := args[0]
	if command == "help" || command == "--help" || command == "-h" {
		return writeUsage(streams.Output)
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
	input, readErr := readInput(ctx, streams.Input, opt.inputPath, opt.positional, cfg.MaxInputBytes)
	if readErr != nil {
		return emitError(streams.Error, cfg.Format, readErr)
	}
	r := runner{ctx: ctx, input: input, options: opt, config: cfg, streams: streams}
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
		if err := compiler.ValidateImproveRequest(request); err != nil {
			return r.emitError(err)
		}
		if r.options.isPolicySet {
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
	if request.PromptPlan == nil && r.options.phase != "design" {
		plan := compiler.NewPlan(request.Intent)
		plan.PhaseScope = r.options.phase
		request.PromptPlan = &plan
	} else if request.PromptPlan != nil && r.options.isPhaseSet {
		request.PromptPlan.PhaseScope = r.options.phase
	}
	result, err := compiler.Improve(r.ctx, request, r.config.HostPermission)
	if err != nil {
		return r.emitError(err)
	}
	return r.emitResult(result, result.ImprovedPrompt)
}

func (r runner) runGoal() int {
	var request contracts.CreateGoalPromptRequest
	if r.options.isRequestJSON {
		if err := decodeStrict(r.input, &request); err != nil {
			return r.emitError(err)
		}
	} else {
		objective := string(r.input)
		request = contracts.CreateGoalPromptRequest{
			SchemaVersion: contracts.SchemaVersion,
			Kind:          "request",
			Objective:     objective,
			PromptPlan:    compiler.NewPlan(objective),
		}
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
	if err := r.writeLint(result); err != nil {
		return r.outputFailure()
	}
	if !result.Valid {
		return exitSemantic
	}
	return exitOK
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

func validCommand(command string) bool {
	switch command {
	case "improve_prompt", "create_goal_prompt", "create_review_fix_prompt", "lint_prompt":
		return true
	default:
		return false
	}
}

func writeUsage(output io.Writer) int {
	_, err := fmt.Fprintln(
		output,
		"usage: prompt-better <improve_prompt|create_goal_prompt|create_review_fix_prompt|lint_prompt> [flags] [text]",
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
