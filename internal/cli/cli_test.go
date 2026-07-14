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
