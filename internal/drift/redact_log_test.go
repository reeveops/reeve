package drift

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/redact"
	"github.com/reeveops/reeve/internal/iac"
	revelog "github.com/reeveops/reeve/internal/log"
)

type capturingLogWriter struct {
	lines *[]string
}

func (w *capturingLogWriter) Write(p []byte) (int, error) {
	*w.lines = append(*w.lines, string(p))
	return len(p), nil
}

// logSecretDriftEngine logs on the run's own sink, un-wrapped: masking the
// registered credential value is the log handler's job.
type logSecretDriftEngine struct {
	scriptEngine
	stacks        []discovery.Stack
	secret, plain string
	logged        bool
}

func (e *logSecretDriftEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	return e.stacks, nil
}

func (e *logSecretDriftEngine) DriftCheck(ctx context.Context, s discovery.Stack, opts iac.PreviewOpts, b bool) (iac.PreviewResult, error) {
	slog.Info("drift side channel", "cred", e.secret, "plain", e.plain)
	e.logged = true
	return iac.PreviewResult{}, nil
}

// TestDriftRunLogsMaskedRegisteredSecret pins the drift path: the resolved
// credential values register into the invocation redactor, drift.Run installs
// it as the process log redactor, and a value matching no pattern rule is
// masked in the sink while unrelated values still pass through.
func TestDriftRunLogsMaskedRegisteredSecret(t *testing.T) {
	secret := "drift-nopattern-secret-42"
	plain := "plain-drift-value"
	var lines []string
	revelog.Install(&capturingLogWriter{lines: &lines}, slog.LevelInfo, revelog.FormatText)
	t.Cleanup(func() { revelog.SetRedactor(nil) })

	eng := &logSecretDriftEngine{scriptEngine: scriptEngine{results: []scriptResult{}}}
	eng.secret, eng.plain = secret, plain
	eng.stacks = []discovery.Stack{{Project: "p", Name: "s", Path: "p/s"}}
	resolver := func(_ context.Context, _ string) (map[string]string, func(), error) {
		return map[string]string{"REEVE_TEST_CREDENTIAL": secret}, func() {}, nil
	}
	opts := Options{
		Engine:       eng,
		Redactor:     redact.New(),
		AuthResolver: resolver,
		Decls:        []discovery.Declaration{{Path: "p/s", Stacks: []string{"s"}}},
	}
	// Run is the unit: it installs opts.Redactor as the process log
	// redactor before any stack resolves credentials.
	out, rerr := Run(context.Background(), opts)
	if rerr != nil {
		t.Fatalf("Run: %v", rerr)
	}
	if len(out.Items) != 1 {
		t.Fatalf("items = %d", len(out.Items))
	}

	joined := strings.Join(lines, "\n")
	if !eng.logged {
		t.Fatalf("the drift check must run")
	}
	if strings.Contains(joined, secret) {
		t.Fatalf("a registered credential value must be masked in the drift log sink:\n%s", joined)
	}
	if !strings.Contains(joined, "[redacted]") {
		t.Fatalf("expected the masking marker:\n%s", joined)
	}
	if !strings.Contains(joined, plain) {
		t.Fatalf("an unregistered value must still be visible (masking is registration-driven):\n%s", joined)
	}
}
