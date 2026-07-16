package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nijanthan-dev/codex-prompt-better/pkg/contracts"
)

func execute(args []string, input string) (int, string, string) {
	var out, errOut bytes.Buffer
	streams := Streams{Input: io.NopCloser(strings.NewReader(input)), Output: &out, Error: &errOut}
	code := Run(context.Background(), args, streams)
	return code, out.String(), errOut.String()
}

func TestMissingCommandIsInvalidButHelpSucceeds(t *testing.T) {
	code, out, stderr := execute(nil, "")
	if code != exitInvalid || out != "" || !strings.Contains(stderr, "usage:") {
		t.Fatalf("missing command: code=%d out=%q stderr=%q", code, out, stderr)
	}
	code, out, stderr = execute([]string{"help"}, "")
	if code != exitOK || stderr != "" || !strings.Contains(out, "usage:") {
		t.Fatalf("help: code=%d out=%q stderr=%q", code, out, stderr)
	}
}

func TestImprovePlainJSONIsByteStable(t *testing.T) {
	args := []string{"improve_prompt", "--format", "json"}
	code, first, stderr := execute(args, "Return synthetic output.")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	code, second, stderr := execute(args, "Return synthetic output.")
	if code != 0 || stderr != "" || first != second {
		t.Fatalf("unstable output: %q %q %q", first, second, stderr)
	}
	var result contracts.ImprovePromptResult
	if err := json.Unmarshal([]byte(first), &result); err != nil {
		t.Fatal(err)
	}
	if result.PolicyOutcome != contracts.PolicyOutcomeReturnOnly || result.ImprovedPrompt == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestBoundaryDiscoveryIsExplicitAndSanitized(t *testing.T) {
	root := t.TempDir()
	const rawInstruction = "Non-goal: private-synthetic-directive."
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(rawInstruction), 0o600); err != nil {
		t.Fatal(err)
	}
	const rawWorktreeTarget = "gitdir: /private/synthetic/worktree-target"
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte(rawWorktreeTarget), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := execute([]string{"improve_prompt", "--format", "json", "--context-root", root, "--scope", "src"}, "Change synthetic code.")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	for _, sensitive := range []string{root, rawInstruction, rawWorktreeTarget, "private-synthetic-directive", "worktree-target"} {
		if strings.Contains(out, sensitive) {
			t.Fatalf("sensitive value escaped: %s", out)
		}
	}
	var result contracts.ImprovePromptResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.BoundaryDecisions) == 0 || !strings.Contains(result.ImprovedPrompt, "Scope:\n- src") || !strings.Contains(result.ImprovedPrompt, "Non-goals:") {
		t.Fatalf("missing boundary integration: %+v", result)
	}
}

func TestLintContextRootAllowsPublicSecurityPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SECURITY.md"), []byte("Public vulnerability reporting policy."), 0o600); err != nil {
		t.Fatal(err)
	}
	code, prompt, stderr := execute([]string{"improve_prompt"}, "Return synthetic output.")
	if code != exitOK || stderr != "" {
		t.Fatalf("compile code=%d stderr=%s", code, stderr)
	}
	code, out, stderr := execute([]string{"lint_prompt", "--format", "json", "--context-root", root}, prompt)
	if code != exitOK || stderr != "" {
		t.Fatalf("lint code=%d out=%s stderr=%s", code, out, stderr)
	}
	var result contracts.LintPromptResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	for _, decision := range result.BoundaryDecisions {
		if decision.Outcome == "block" {
			t.Fatalf("public security policy blocked lint: %+v", decision)
		}
	}
}

func TestBoundaryFlagsRejectUnsafeCombinations(t *testing.T) {
	root := t.TempDir()
	tests := [][]string{
		{"create_goal_prompt", "--context-root", root},
		{"lint_prompt", "--scope", "src"},
		{"improve_prompt", "--context-root", root, "--scope", "../outside"},
	}
	for _, args := range tests {
		code, _, _ := execute(args, "Synthetic prompt.")
		if code == 0 {
			t.Fatalf("unsafe flags accepted: %v", args)
		}
	}
}

func TestBoundaryDiscoveryPreservesAndNarrowsRequestScopes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Out of scope: synthetic deployment."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "arbitrary-name.yaml"), []byte("name: Synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := strings.Replace(improveRequestJSON(), `"phase_scope":"implementation"`, `"scope":["existing"],"non_goals":["Keep explicit non-goal."],"gates":["Keep explicit gate."],"phase_scope":"implementation"`, 1)
	code, out, stderr := execute([]string{"improve_prompt", "--request-json", "--format", "json", "--context-root", root, "--scope", "added", "--scope", "added"}, request)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var result contracts.ImprovePromptResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.ImprovedPrompt, "Scope:\n- existing\n- added") {
		t.Fatalf("scope lost: %s", result.ImprovedPrompt)
	}
	for _, preserved := range []string{"Keep explicit non-goal.", "Keep explicit gate.", "A non-goal cannot be broadened", "Preserve applicable validation gates."} {
		if strings.Count(result.ImprovedPrompt, preserved) != 1 {
			t.Fatalf("boundary not preserved/deduplicated: %q in %s", preserved, result.ImprovedPrompt)
		}
	}
	code, out, stderr = execute([]string{"improve_prompt", "--request-json", "--format", "json", "--context-root", root}, request)
	if code != 0 || stderr != "" || !strings.Contains(out, "Scope:\\n- existing") {
		t.Fatalf("scope lost without CLI scope: code=%d out=%s stderr=%s", code, out, stderr)
	}
}

func TestBoundaryDiagnosticsRemainWithinResultLimit(t *testing.T) {
	result := contracts.LintPromptResult{Valid: true, Diagnostics: make([]contracts.Diagnostic, 99)}
	decisions := []contracts.BoundaryDecision{
		{Outcome: "warn", Explanation: "Synthetic warning.", SourceRef: "source.a"},
		{Outcome: "clarify", Explanation: "Synthetic clarification.", SourceRef: "source.b"},
		{Outcome: "block", Explanation: "Synthetic block.", SourceRef: "source.c"},
	}
	result = addBoundaryDiagnostics(result, decisions)
	if len(result.Diagnostics) != 100 || result.Valid {
		t.Fatalf("diagnostics=%d valid=%t", len(result.Diagnostics), result.Valid)
	}
}

func TestImproveRequestJSONPolicyOutcomes(t *testing.T) {
	request := improveRequestJSON()
	code, out, stderr := execute(
		[]string{
			"improve_prompt", "--request-json", "--format", "json",
			"--host-permission", "permitted",
		},
		request,
	)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var result contracts.ImprovePromptResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.PolicyOutcome != contracts.PolicyOutcomeExecutionRecommended {
		t.Fatalf("outcome=%s", result.PolicyOutcome)
	}
	code, out, stderr = execute(
		[]string{
			"improve_prompt", "--request-json", "--format", "json",
			"--host-permission", "denied",
		},
		request,
	)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.PolicyOutcome != contracts.PolicyOutcomeDenied {
		t.Fatalf("outcome=%s", result.PolicyOutcome)
	}
	code, out, stderr = execute(
		[]string{
			"improve_prompt", "--request-json", "--format", "json",
			"--execution-policy", "improve_only", "--host-permission", "permitted",
		},
		request,
	)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.PolicyOutcome != contracts.PolicyOutcomeReturnOnly {
		t.Fatalf("flag did not override request policy: %s", result.PolicyOutcome)
	}
}

func TestGoalAndReviewText(t *testing.T) {
	code, out, stderr := execute([]string{"create_goal_prompt"}, "Synthetic objective")
	if code != 0 || stderr != "" || !strings.HasPrefix(out, "Take this as a new goal:") {
		t.Fatalf("code=%d out=%q err=%q", code, out, stderr)
	}
	code, out, stderr = execute(
		[]string{"create_review_fix_prompt", "--review-head", "abcdef1"},
		"Missing synthetic test.\nUnsafe fallback.",
	)
	if code != 0 || stderr != "" || !strings.Contains(out, "same failure class") {
		t.Fatalf("code=%d out=%q err=%q", code, out, stderr)
	}
}

func TestLintSemanticExitAndStructuredDiagnostics(t *testing.T) {
	code, out, stderr := execute(
		[]string{"lint_prompt", "--format", "json"},
		"Keep going until happy and show chain-of-thought.",
	)
	if code != exitSemantic || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var result contracts.LintPromptResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Valid || len(result.Diagnostics) == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestInputFileCRLFAndLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte("Synthetic\r\ninput\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := execute([]string{"improve_prompt", "--input", path}, "")
	if code != 0 || stderr != "" || strings.Contains(out, "\r") {
		t.Fatalf("code=%d out=%q err=%q", code, out, stderr)
	}
	code, _, stderr = execute([]string{"improve_prompt", "--max-input-bytes", "3"}, "four")
	if code != exitInvalid || !strings.Contains(stderr, "exceeds configured byte limit") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestSynthesizedPlansPreserveLongValidInput(t *testing.T) {
	input := strings.Repeat("x", 3000)
	tests := [][]string{
		{"improve_prompt", "--phase", "implementation"},
		{"create_goal_prompt"},
	}
	for _, args := range tests {
		code, out, stderr := execute(args, input)
		if code != exitOK || stderr != "" || !strings.Contains(out, input) {
			t.Fatalf("args=%v code=%d preserved=%t stderr=%q", args, code, strings.Contains(out, input), stderr)
		}
	}
}

func TestExplicitConfigurationFlagsAreValidated(t *testing.T) {
	tests := []struct {
		name string
		flag string
	}{
		{name: "zero input limit", flag: "--max-input-bytes=0"},
		{name: "empty format", flag: "--format="},
		{name: "empty host permission", flag: "--host-permission="},
		{name: "empty timeout", flag: "--timeout="},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, _, stderr := execute(
				[]string{"improve_prompt", test.flag},
				"Synthetic",
			)
			if code != exitInvalid || stderr == "" {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
		})
	}
}

func TestConfigErrorsHonorRequestedJSONFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"timeout":"bad"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "invalid flag",
			args: []string{"improve_prompt", "--format", "json", "--unknown"},
		},
		{
			name: "invalid config flag",
			args: []string{"improve_prompt", "--format", "json", "--timeout", "bad"},
		},
		{
			name: "invalid config",
			args: []string{"improve_prompt", "--format", "json", "--config", path},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, _, stderr := execute(test.args, "Synthetic")
			if code != exitInvalid {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
			var stable contracts.StableError
			if err := json.Unmarshal([]byte(stderr), &stable); err != nil {
				t.Fatalf("non-JSON error %q: %v", stderr, err)
			}
			if stable.Code != contracts.ErrorCodeInvalidSchema {
				t.Fatalf("error=%+v", stable)
			}
		})
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	streams := Streams{Input: io.NopCloser(strings.NewReader("Synthetic")), Output: &out, Error: &errOut}
	code := Run(ctx, []string{"improve_prompt"}, streams)
	if code != exitBudget {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
}

func TestConfigThenCLIOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"format":"json","execution_policy":"ask_before_execute"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := execute([]string{"improve_prompt", "--config", path, "--format", "text"}, "Synthetic")
	if code != 0 || stderr != "" || strings.HasPrefix(out, "{") {
		t.Fatalf("code=%d out=%q err=%q", code, out, stderr)
	}
}

func TestConfigPolicyOverridesRequestJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"execution_policy":"improve_only"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := execute(
		[]string{
			"improve_prompt", "--request-json", "--format", "json",
			"--config", path, "--host-permission", "permitted",
		},
		improveRequestJSON(),
	)
	if code != exitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var result contracts.ImprovePromptResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.PolicyOutcome != contracts.PolicyOutcomeReturnOnly {
		t.Fatalf("file policy not applied: %s", result.PolicyOutcome)
	}
}

func TestProvenanceShowsSourcesOnly(t *testing.T) {
	code, _, stderr := execute(
		[]string{"improve_prompt", "--show-provenance", "--execution-policy", "improve_only"},
		"Synthetic",
	)
	if code != 0 || !strings.Contains(stderr, "execution_policy=cli") || strings.Contains(stderr, "Synthetic") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestRequestRejectsTrailingGarbage(t *testing.T) {
	request := `{"schema_version":"1.0.0","kind":"request","candidate":"x"} garbage`
	code, _, stderr := execute([]string{"lint_prompt", "--request-json"}, request)
	if code != exitInvalid || !strings.Contains(stderr, "trailing request") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestRequestRejectsInvalidUTF8(t *testing.T) {
	prefix := []byte(`{"schema_version":"1.0.0","kind":"request","candidate":"`)
	request := string(append(append(prefix, 0xff), []byte(`"}`)...))
	code, _, stderr := execute([]string{"lint_prompt", "--request-json"}, request)
	if code != exitInvalid || !strings.Contains(stderr, "valid utf-8") || strings.Contains(stderr, request) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestRequestJSONDoesNotInheritPlainInputDefaults(t *testing.T) {
	minimal := `{"schema_version":"1.0.0","kind":"request"}`
	tests := []struct {
		name    string
		command string
		request string
	}{
		{name: "improve missing fields", command: "improve_prompt", request: minimal},
		{
			name: "improve null intent", command: "improve_prompt",
			request: `{"schema_version":"1.0.0","kind":"request",` +
				`"intent":null,"execution_policy":"improve_only"}`,
		},
		{name: "goal missing fields", command: "create_goal_prompt", request: minimal},
		{name: "review missing fields", command: "create_review_fix_prompt", request: minimal},
		{name: "lint missing fields", command: "lint_prompt", request: minimal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, _, stderr := execute(
				[]string{test.command, "--request-json"},
				test.request,
			)
			if code != exitInvalid || stderr == "" {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
		})
	}
}

func TestRequestJSONIsValidatedBeforeCLIOverrides(t *testing.T) {
	tests := []struct {
		name    string
		request string
		flag    string
	}{
		{
			name: "missing policy",
			request: `{"schema_version":"1.0.0","kind":"request",` +
				`"intent":"Synthetic"}`,
			flag: "--execution-policy=improve_only",
		},
		{
			name: "missing plan phase",
			request: strings.Replace(
				improveRequestJSON(),
				`"phase_scope":"implementation",`,
				"",
				1,
			),
			flag: "--phase=review",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, _, stderr := execute(
				[]string{"improve_prompt", "--request-json", test.flag},
				test.request,
			)
			if code != exitInvalid || stderr == "" {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
		})
	}
}

func TestRequestJSONRejectsNullBudgetContextMode(t *testing.T) {
	base := strings.TrimSuffix(improveRequestJSON(), "}")
	budgetPrefix := `,"budget":{"schema_version":"1.0.0","enforcement":"advisory",` +
		`"active_phases":["implementation"],"delegation_policy":"none",`
	request := base + budgetPrefix +
		`"context_mode":null,"exhaustion_outcome":"stop"}}`
	code, _, stderr := execute(
		[]string{"improve_prompt", "--request-json"},
		request,
	)
	if code != exitInvalid || !strings.Contains(stderr, "context_mode") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}

	withoutContext := base + budgetPrefix + `"exhaustion_outcome":"stop"}}`
	code, _, stderr = execute(
		[]string{"improve_prompt", "--request-json"},
		withoutContext,
	)
	if code != exitOK || stderr != "" {
		t.Fatalf("optional field rejected: code=%d stderr=%q", code, stderr)
	}
}

func TestImproveRequestJSONRejectsNonNullableOptionals(t *testing.T) {
	base := `{"schema_version":"1.0.0","kind":"request",` +
		`"intent":"Synthetic","execution_policy":"improve_only"}`
	budget := `{"schema_version":"1.0.0","enforcement":"advisory",` +
		`"active_phases":["implementation"],"delegation_policy":"none",` +
		`"exhaustion_outcome":"stop"}`
	tests := []struct{ name, field, want string }{
		{name: "null prompt plan", field: `"prompt_plan":null`, want: "prompt_plan"},
		{name: "null budget", field: `"budget":null`, want: "budget"},
		{
			name: "empty context mode",
			want: "context_mode",
			field: `"budget":` + strings.Replace(
				budget,
				`"exhaustion_outcome":"stop"`,
				`"context_mode":"","exhaustion_outcome":"stop"`,
				1,
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := strings.TrimSuffix(base, "}") + "," + test.field + "}"
			code, _, stderr := execute(
				[]string{"improve_prompt", "--request-json"},
				request,
			)
			if code != exitInvalid || !strings.Contains(stderr, test.want) {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
		})
	}

	for _, request := range []string{
		base,
		strings.TrimSuffix(base, "}") + `,"budget":` + budget + "}",
	} {
		code, _, stderr := execute(
			[]string{"improve_prompt", "--request-json"},
			request,
		)
		if code != exitOK || stderr != "" {
			t.Fatalf("optional omission rejected: code=%d stderr=%q", code, stderr)
		}
	}
}

func TestRequestJSONRejectsEmptyArtifactPriorities(t *testing.T) {
	goalRequest := strings.Replace(
		improveRequestJSON(),
		`"intent":"Implement synthetic change.",`+
			`"execution_policy":"follow_user_intent",`,
		`"objective":"Implement synthetic change.",`,
		1,
	)
	requests := map[string]string{
		"improve_prompt":     improveRequestJSON(),
		"create_goal_prompt": goalRequest,
	}
	for command, base := range requests {
		for _, value := range []string{"[]", "null"} {
			request := strings.Replace(
				base,
				`"validation_bar":["Tests pass."]`,
				`"validation_bar":["Tests pass."],"artifact_priorities":`+value,
				1,
			)
			code, _, stderr := execute(
				[]string{command, "--request-json"},
				request,
			)
			if code != exitInvalid || !strings.Contains(stderr, "artifact_priorities") {
				t.Fatalf("command=%s value=%s code=%d stderr=%q", command, value, code, stderr)
			}
		}

		code, _, stderr := execute(
			[]string{command, "--request-json"},
			base,
		)
		if code != exitOK || stderr != "" {
			t.Fatalf("command=%s optional field rejected: code=%d stderr=%q", command, code, stderr)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("synthetic write failure") }

func TestOutputFailuresReturnInternalError(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		input string
	}{
		{name: "result text", args: []string{"improve_prompt"}, input: "Synthetic"},
		{name: "result json", args: []string{"improve_prompt", "--format", "json"}, input: "Synthetic"},
		{name: "lint text", args: []string{"lint_prompt"}, input: "Synthetic"},
		{name: "lint json", args: []string{"lint_prompt", "--format", "json"}, input: "Synthetic"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var errOut bytes.Buffer
			streams := Streams{Input: io.NopCloser(strings.NewReader(test.input)), Output: failingWriter{}, Error: &errOut}
			code := Run(context.Background(), test.args, streams)
			if code != exitInternal || !strings.Contains(errOut.String(), "output write failed") {
				t.Fatalf("code=%d stderr=%q", code, errOut.String())
			}
		})
	}
}

type blockingInput struct {
	started chan struct{}
	closed  chan struct{}
	done    chan struct{}
}

type trackedInput struct{ closed bool }

func (*trackedInput) Read([]byte) (int, error) { return 0, io.EOF }
func (r *trackedInput) Close() error {
	r.closed = true
	return nil
}

func TestEarlyReturnsCloseInput(t *testing.T) {
	tests := [][]string{
		nil,
		{"help"},
		{"unknown"},
		{"improve_prompt", "--unknown"},
		{"improve_prompt", "--config", filepath.Join(t.TempDir(), "missing.json")},
	}
	for _, args := range tests {
		input := &trackedInput{}
		var out, errOut bytes.Buffer
		Run(context.Background(), args, Streams{Input: input, Output: &out, Error: &errOut})
		if !input.closed {
			t.Fatalf("input left open for args=%v", args)
		}
	}
}

func newBlockingInput() *blockingInput {
	return &blockingInput{started: make(chan struct{}), closed: make(chan struct{}), done: make(chan struct{})}
}
func (r *blockingInput) Read([]byte) (int, error) {
	close(r.started)
	<-r.closed
	close(r.done)
	return 0, errors.New("closed")
}
func (r *blockingInput) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

func TestInputTimeoutClosesAndJoinsReader(t *testing.T) {
	input := newBlockingInput()
	var out, errOut bytes.Buffer
	streams := Streams{Input: input, Output: &out, Error: &errOut}
	code := Run(context.Background(), []string{"improve_prompt", "--timeout", "10ms"}, streams)
	if code != exitBudget || !strings.Contains(errOut.String(), "cancelled or timed out") {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
	select {
	case <-input.done:
	case <-time.After(time.Second):
		t.Fatal("reader goroutine survived Run")
	}
}

func TestAllCommandsAreByteStable(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		input string
	}{
		{name: "improve", args: []string{"improve_prompt", "--format", "json"}, input: "Synthetic"},
		{name: "goal", args: []string{"create_goal_prompt", "--format", "json"}, input: "Synthetic"},
		{
			name: "review",
			args: []string{
				"create_review_fix_prompt", "--format", "json", "--review-head", "abcdef1",
			},
			input: "Synthetic finding",
		},
		{name: "lint", args: []string{"lint_prompt", "--format", "json"}, input: "Synthetic"},
		{name: "error", args: []string{"create_review_fix_prompt", "--format", "json"}, input: "Synthetic"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			firstCode, firstOut, firstErr := execute(test.args, test.input)
			secondCode, secondOut, secondErr := execute(test.args, test.input)
			if firstCode != secondCode || firstOut != secondOut || firstErr != secondErr {
				t.Fatalf(
					"unstable results: (%d,%q,%q) != (%d,%q,%q)",
					firstCode, firstOut, firstErr,
					secondCode, secondOut, secondErr,
				)
			}
		})
	}
}

func TestStableErrorExitCodes(t *testing.T) {
	tests := []struct {
		code contracts.ErrorCode
		want int
	}{
		{code: contracts.ErrorCodeInvalidSchema, want: exitInvalid},
		{code: contracts.ErrorCodeSemanticInvalid, want: exitSemantic},
		{code: contracts.ErrorCodePermissionDenied, want: exitPermission},
		{code: contracts.ErrorCodeApprovalRequired, want: exitApproval},
		{code: contracts.ErrorCodeBudgetExhausted, want: exitBudget},
		{code: contracts.ErrorCodeInternal, want: exitInternal},
	}
	for _, test := range tests {
		err := contracts.NewError(test.code, "synthetic", "test", false)
		if got := exitFor(err); got != test.want {
			t.Fatalf("code=%s got=%d want=%d", test.code, got, test.want)
		}
	}
}

func improveRequestJSON() string {
	return `{"schema_version":"1.0.0","kind":"request",` +
		`"intent":"Implement synthetic change.",` +
		`"execution_policy":"follow_user_intent","prompt_plan":{` +
		`"schema_version":"1.0.0","goal":"Implement synthetic change.",` +
		`"success_criteria":["Done."],"invariants":["Synthetic."],` +
		`"decision_rules":["Stay scoped."],"evidence_requirements":["Tests."],` +
		`"tools":["Local."],"output_contract":"Return result.",` +
		`"approval_boundary":"Host permission required.",` +
		`"phase_scope":"implementation","stop_rules":["Stop when tested."],` +
		`"fallback_rules":["Report blocker."],"abstain_rules":["Abstain if unsafe."],` +
		`"validation_bar":["Tests pass."]}}`
}
