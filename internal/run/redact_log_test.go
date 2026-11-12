package run

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/reeveops/reeve/internal/auth"
	"github.com/reeveops/reeve/internal/blob/filesystem"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/core/summary"
	"github.com/reeveops/reeve/internal/iac"
	revelog "github.com/reeveops/reeve/internal/log"
)

// The round-2 redaction guarantee: every command that resolves credentials
// registers the resolved values into the invocation redactor AND installs it
// as the process log redactor, so a value matching NO pattern rule is still
// masked the moment it resolves. A value the run never registered still
// passes (masking is registration-driven, not blanket).

// secretCredentialProvider yields an env value that no credential pattern
// rule matches.
type secretCredentialProvider struct {
	acquires int
	mu       sync.Mutex
	envValue string
}

func (p *secretCredentialProvider) Name() string { return "shared" }
func (p *secretCredentialProvider) Type() string { return "test" }
func (p *secretCredentialProvider) Acquire(context.Context) (*auth.Credential, error) {
	p.mu.Lock()
	p.acquires++
	p.mu.Unlock()
	return &auth.Credential{
		Env:     map[string]string{"REEVE_TEST_CREDENTIAL": p.envValue},
		Kind:    "test",
		Source:  "shared",
		Cleanup: func() error { return nil },
	}, nil
}

// logSideChannelEngine logs values on the run's own sink - un-wrapped, so
// masking them is the log handler's job.
type logSideChannelEngine struct {
	secret, plain string
	logged        bool
}

func (*logSideChannelEngine) Name() string { return "sidechannel" }
func (e *logSideChannelEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	return []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}, nil
}
func (*logSideChannelEngine) Capabilities() iac.Capabilities {
	return iac.Capabilities{SupportsRefresh: true}
}
func (e *logSideChannelEngine) Preview(ctx context.Context, s discovery.Stack, _ iac.PreviewOpts) (iac.PreviewResult, error) {
	slog.Info("preview side channel", "cred", e.secret, "plain", e.plain)
	e.logged = true
	return iac.PreviewResult{Counts: summary.Counts{Add: 1}, PlanSummary: e.plain}, nil
}
func (e *logSideChannelEngine) Refresh(ctx context.Context, s discovery.Stack, _ iac.RefreshOpts) (iac.RefreshResult, error) {
	slog.Info("refresh side channel", "cred", e.secret, "plain", e.plain)
	e.logged = true
	return iac.RefreshResult{Counts: summary.Counts{Change: 1}}, nil
}

type capturingLogWriter struct {
	lines *[]string
}

func (w *capturingLogWriter) Write(p []byte) (int, error) {
	*w.lines = append(*w.lines, string(p))
	return len(p), nil
}

// captureSlog installs the REPO's log stack (the redacting handler) over a
// capturing buffer and returns the captured lines. Capturing through
// slog.SetDefault with a bare handler would bypass the sink under test.
func captureSlog(t *testing.T) *[]string {
	t.Helper()
	var lines []string
	revelog.Install(&capturingLogWriter{lines: &lines}, slog.LevelInfo, revelog.FormatText)
	t.Cleanup(func() { revelog.SetRedactor(nil) })
	return &lines
}

// TestPreviewLogsMaskedRegisteredSecret pins the preview path: the resolved
// credential value is registered into the invocation redactor, which is the
// process log redactor - the engine's own sink log masks it - while an
// unrelated value still passes through.
func TestPreviewLogsMaskedRegisteredSecret(t *testing.T) {
	secret := "pv-nopattern-secret-42"
	plain := "plain-preview-value"
	captured := captureSlog(t)

	provider := &secretCredentialProvider{envValue: secret}
	registry := auth.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	store, ferr := filesystem.New(t.TempDir())
	if ferr != nil {
		t.Fatal(ferr)
	}
	engine := &logSideChannelEngine{secret: secret, plain: plain}
	fv := &fakeVCS{changed: []string{"projects/api/main.tf"}}
	in := previewInputFor(engine, store, fv)
	in.AuthConfig = &schemas.Auth{
		Providers: map[string]schemas.ProviderYAML{"shared": {Type: "test"}},
		Bindings:  []schemas.BindingYAML{{Match: schemas.BindingMatch{Stack: "*/*"}, Providers: []string{"shared"}}},
	}
	in.AuthRegistry = registry

	out, err := Preview(context.Background(), in)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !engine.logged || len(out.Stacks) == 0 || out.Stacks[0].Status != summary.StatusPlanned {
		t.Fatalf("the preview must run the engine and succeed: out=%+v", out.Stacks)
	}
	joined := strings.Join(*captured, "\n")
	if strings.Contains(joined, secret) {
		t.Fatalf("a registered credential value must be masked in the log sink:\n%s", joined)
	}
	if !strings.Contains(joined, "[redacted]") {
		t.Fatalf("expected the masking marker:\n%s", joined)
	}
	if !strings.Contains(joined, plain) {
		t.Fatalf("an unregistered value must still be visible (masking is registration-driven):\n%s", joined)
	}
}

// TestRefreshLogsMaskedRegisteredSecret pins the same guarantee on the
// refresh path.
func TestRefreshLogsMaskedRegisteredSecret(t *testing.T) {
	secret := "rf-nopattern-secret-42"
	plain := "plain-refresh-value"
	captured := captureSlog(t)

	provider := &secretCredentialProvider{envValue: secret}
	registry := auth.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	engine2 := &refreshSideChannelEngine{logSideChannelEngine: logSideChannelEngine{secret: secret, plain: plain}}
	out, err := Refresh(context.Background(), RefreshInput{
		Local: true, DryRun: true, RepoRoot: "/repo", Engine: engine2,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Type: "tofu", State: schemas.EngineState{AuthProvider: "shared"},
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"prod"}}},
		}},
		AuthConfig: &schemas.Auth{
			Providers: map[string]schemas.ProviderYAML{"shared": {Type: "test"}},
			Bindings: []schemas.BindingYAML{{
				Match: schemas.BindingMatch{Stack: "*/*", Mode: "apply"}, Providers: []string{"shared"},
			}},
		},
		AuthRegistry: registry,
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if out.Failed || out.Blocked || len(engine2.refreshed) != 1 {
		t.Fatalf("refresh must run: %+v refreshed=%v", out, engine2.refreshed)
	}
	joined := strings.Join(*captured, "\n")
	if strings.Contains(joined, secret) {
		t.Fatalf("a registered credential value must be masked in the refresh log sink:\n%s", joined)
	}
	if !strings.Contains(joined, "[redacted]") {
		t.Fatalf("expected the masking marker:\n%s", joined)
	}
	if !strings.Contains(joined, plain) {
		t.Fatalf("an unregistered value must still be visible:\n%s", joined)
	}
}

type refreshSideChannelEngine struct {
	logSideChannelEngine
	stacks    []discovery.Stack
	refreshed []string
}

func (e *refreshSideChannelEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	e.stacks = []discovery.Stack{{Project: "api", Path: "projects/api", Name: "prod", Env: "prod"}}
	return e.stacks, nil
}
func (e *refreshSideChannelEngine) Refresh(ctx context.Context, s discovery.Stack, opts iac.RefreshOpts) (iac.RefreshResult, error) {
	e.refreshed = append(e.refreshed, s.Ref())
	return e.logSideChannelEngine.Refresh(ctx, s, iac.RefreshOpts{})
}
