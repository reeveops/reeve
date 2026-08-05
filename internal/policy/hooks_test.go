package policy

import (
	"context"
	"testing"

	"github.com/FynxLabs/reeve/internal/core/redact"
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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
