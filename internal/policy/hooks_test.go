package policy

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/core/redact"
)

func TestRunPass(t *testing.T) {
	h := Hook{Name: "true", Command: []string{"true"}, OnFail: FailBlock, Required: true}
	res := Run(context.Background(), h, Context{}, redact.New())
	if res.Outcome != "pass" || res.ExitCode != 0 {
		t.Fatalf("unexpected: %+v", res)
	}
}

func TestRunFailBlocks(t *testing.T) {
	h := Hook{Name: "false", Command: []string{"false"}, OnFail: FailBlock, Required: true}
	res := Run(context.Background(), h, Context{}, redact.New())
	if res.Outcome != "fail" {
		t.Fatalf("expected fail, got %+v", res)
	}
}

func TestRunFailWarn(t *testing.T) {
	h := Hook{Name: "false-warn", Command: []string{"false"}, OnFail: FailWarn, Required: true}
	res := Run(context.Background(), h, Context{}, redact.New())
	if res.Outcome != "warn" {
		t.Fatalf("expected warn, got %+v", res)
	}
}

func TestRunMissingNotRequiredSkips(t *testing.T) {
	h := Hook{Name: "missing", Command: []string{"/definitely/not/a/real/binary-xyz"}, OnFail: FailBlock, Required: false}
	res := Run(context.Background(), h, Context{}, redact.New())
	if res.Outcome != "skipped" {
		t.Fatalf("expected skipped, got %+v", res)
	}
}

func TestRedactsStdout(t *testing.T) {
	r := redact.New()
	r.AddSecret("super-secret-value-123")
	h := Hook{Name: "echo", Command: []string{"sh", "-c", "echo super-secret-value-123 leaked"}, OnFail: FailWarn, Required: true}
	res := Run(context.Background(), h, Context{}, r)
	if contains(res.Stdout, "super-secret-value-123") {
		t.Fatalf("secret not redacted in stdout: %q", res.Stdout)
	}
	if !contains(res.Stdout, "[redacted]") {
		t.Fatalf("replacement missing: %q", res.Stdout)
	}
}

func TestTemplateExpansion(t *testing.T) {
	h := Hook{Name: "echo-stack", Command: []string{"sh", "-c", "echo stack={{stack_name}} project={{project}}"},
		OnFail: FailBlock, Required: true}
	tc := Context{StackName: "prod", Project: "api"}
	res := Run(context.Background(), h, tc, redact.New())
	if !contains(res.Stdout, "stack=prod project=api") {
		t.Fatalf("expansion failed: %q", res.Stdout)
	}
}

func TestRunDoesNotInheritControllerEnvironment(t *testing.T) {
	t.Setenv("REEVE_SENTINEL_SECRET", "controller-only")
	h := Hook{
		Name: "environment", Command: []string{"sh", "-c", "printf %s \"${REEVE_SENTINEL_SECRET-unset}\""},
		OnFail: FailBlock, Required: true,
	}
	res := Run(context.Background(), h, Context{}, redact.New())
	if res.Outcome != "pass" {
		t.Fatalf("hook failed: %+v", res)
	}
	if res.Stdout != "unset" {
		t.Fatalf("controller environment leaked to hook: %q", res.Stdout)
	}
	if os.Getenv("REEVE_SENTINEL_SECRET") != "controller-only" {
		t.Fatal("test unexpectedly changed the controller environment")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// A hook that fails writing its diagnosis to stderr used to report only
// "exit status 1", with the actual reason held in Result.Stderr and never
// rendered.
func TestRunFailureSurfacesStderr(t *testing.T) {
	h := Hook{
		Name:     "policy",
		Command:  []string{"sh", "-c", "echo 'denied: missing tag owner' >&2; echo 'and more' >&2; exit 1"},
		OnFail:   FailBlock,
		Required: true,
	}
	res := Run(context.Background(), h, Context{}, redact.New())
	if res.Outcome != "fail" {
		t.Fatalf("expected fail, got %+v", res)
	}
	for _, want := range []string{"denied: missing tag owner", "and more"} {
		if !strings.Contains(res.Error, want) {
			t.Fatalf("error lost %q: %q", want, res.Error)
		}
	}

	// And the rendered section shows it, not just the exit status.
	out := RenderSection([]Result{res})
	if !strings.Contains(out, "denied: missing tag owner") {
		t.Fatalf("rendered section dropped stderr:\n%s", out)
	}
}

func TestHookFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stderr string
		err    error
		want   string
	}{
		{"stderr and error", "denied\n", errors.New("exit status 1"), "denied (exit status 1)"},
		{"stderr only", "denied", nil, "denied"},
		{"error only", "  ", errors.New("signal: killed"), "signal: killed"},
		{"neither", "", nil, "hook failed with no output"},
	}
	for _, tt := range tests {
		if got := hookFailure(tt.stderr, tt.err, redact.New()); got != tt.want {
			t.Errorf("%s: hookFailure = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// Result.Error is an output path: RenderSection prints it. A hook that leaks a
// known secret to stderr must not carry it there either.
func TestRedactsStderrIntoError(t *testing.T) {
	r := redact.New()
	r.AddSecret("super-secret-value-123")
	h := Hook{
		Name:     "leaky",
		Command:  []string{"sh", "-c", "echo super-secret-value-123 denied >&2; exit 1"},
		OnFail:   FailBlock,
		Required: true,
	}
	res := Run(context.Background(), h, Context{}, r)
	if strings.Contains(res.Error, "super-secret-value-123") {
		t.Fatalf("secret reached Error: %q", res.Error)
	}
	if strings.Contains(RenderSection([]Result{res}), "super-secret-value-123") {
		t.Fatal("secret reached the rendered section")
	}
}

func TestFailedResultRedacts(t *testing.T) {
	t.Parallel()
	r := redact.New()
	r.AddSecret("super-secret-value-123")
	// The setup-failure path returns before stdout/stderr redaction runs, so
	// it has to redact on its own.
	res := failedResult(Hook{Name: "h", OnFail: FailBlock}, errors.New("prepare failed: super-secret-value-123"), r)
	if strings.Contains(res.Error, "super-secret-value-123") {
		t.Fatalf("failedResult bypassed redaction: %q", res.Error)
	}
}

// A hook controls its own output, so a fence sequence in it must not close the
// markdown block and let the remainder render as markup.
func TestRenderSectionFenceSafe(t *testing.T) {
	t.Parallel()
	res := Result{
		Name:    "h",
		Outcome: "fail",
		Stdout:  "out\n```\n### not a heading",
		Stderr:  "err\n```\n| not | a table |",
	}
	out := RenderSection([]Result{res})
	// Three opening fences (stdout, stderr) plus their closers, and no
	// payload fence left intact to terminate one early.
	if strings.Contains(out, "\n```\n### not a heading") {
		t.Fatalf("stdout fence escaped:\n%s", out)
	}
	if strings.Contains(out, "\n```\n| not | a table |") {
		t.Fatalf("stderr fence escaped:\n%s", out)
	}
	for _, want := range []string{"### not a heading", "| not | a table |"} {
		if !strings.Contains(out, want) {
			t.Fatalf("content lost %q:\n%s", want, out)
		}
	}
}

// Error carries the hook's full stderr, which is unbounded. The summary row
// must stay one bounded line while the block below shows the output.
func TestRenderSectionBoundsErrorHeadline(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 5000)
	res := Result{Name: "h", Outcome: "fail", Error: "first line\n" + long, Stderr: "first line\n" + long}
	out := RenderSection([]Result{res})
	// Find the hook's summary row: the line carrying its name.
	var head string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "❌ h") {
			head = line
			break
		}
	}
	if head == "" {
		t.Fatalf("no summary row:\n%s", out)
	}
	if len(head) > 300 {
		t.Fatalf("summary row not bounded (%d bytes): %q", len(head), head)
	}
	if !strings.Contains(head, "first line") {
		t.Fatalf("summary row lost the leading line: %q", head)
	}
	if !strings.Contains(out, "…(truncated)") {
		t.Fatal("block should be trimmed, not unbounded")
	}
}

func TestHeadline(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{"short single line", "boom", "boom"},
		{"multi-line marks continuation", "boom\ndetail", "boom …"},
		{"long line is cut", strings.Repeat("a", 250), strings.Repeat("a", 200) + "…"},
	}
	for _, tt := range tests {
		if got := headline(tt.in, 200); got != tt.want {
			t.Errorf("%s: headline = %q, want %q", tt.name, got, tt.want)
		}
	}
}
