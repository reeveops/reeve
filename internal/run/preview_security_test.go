package run

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/FynxLabs/reeve/internal/auth/factory"
	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/config"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"

	// Compile in the webhook channel so BuildNotifyChannels can resolve it.
	_ "github.com/FynxLabs/reeve/internal/notify/channels/webhook"
)

// writeNotificationsConfig writes a .reeve/notifications.yaml declaring a
// webhook channel that carries a ${env:EXFIL_SECRET} header and subscribes
// to the pre-approval events, then loads it through the real config loader
// (env expansion + channel-source-file recording included).
func loadExfilConfig(t *testing.T, webhookURL string) *config.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".reeve"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `version: 2
config_type: notifications
channels:
  - type: webhook
    name: exfil
    url: ` + webhookURL + `
    headers:
      X-Auth: "Bearer ${env:EXFIL_SECRET}"
    on: [planning, plan]
`
	if err := os.WriteFile(filepath.Join(root, ".reeve", "notifications.yaml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

type recordingServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rs := &recordingServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.requests = append(rs.requests, r.Clone(context.Background()))
		rs.mu.Unlock()
		w.WriteHeader(200)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordingServer) recorded() []*http.Request {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]*http.Request(nil), rs.requests...)
}

func securityPreviewInput(t *testing.T, cfg *config.Config, fvcs *fakeVCS) PreviewInput {
	t.Helper()
	store, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"}},
	}
	return PreviewInput{
		PRNumber:  7,
		CommitSHA: "attack-sha",
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev"}}},
		}},
		Shared:             &schemas.Shared{},
		Notifications:      cfg.Notifications,
		ChannelSourceFiles: cfg.ChannelSourceFiles,
		Blob:               store,
		VCS:                fvcs,
		Comments:           fvcs,
	}
}

// TestPreviewExfilSuppressedWhenNotificationConfigModified is the composite
// attack scenario: a branch pusher adds a webhook channel with
// `headers: {X-Auth: ${env:EXFIL_SECRET}}` on the pre-approval `planning`
// event and pushes it in the PR. The automatic preview must NOT dispatch to
// any channel - zero outbound requests - and the PR comment must say why.
func TestPreviewExfilSuppressedWhenNotificationConfigModified(t *testing.T) {
	const secret = "ghp_superSecretDoNotExfil0000000000000000"
	t.Setenv("EXFIL_SECRET", secret)

	srv := newRecordingServer(t)
	cfg := loadExfilConfig(t, srv.URL)
	if len(cfg.ChannelSourceFiles) == 0 {
		t.Fatal("loader must record which files declared channels")
	}

	fvcs := &fakeVCS{
		changed: []string{".reeve/notifications.yaml", "projects/api/main.ts"},
		headSHA: "attack-sha",
	}
	out, err := Preview(context.Background(), securityPreviewInput(t, cfg, fvcs))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	if got := srv.recorded(); len(got) != 0 {
		t.Fatalf("SECURITY FAILURE: %d outbound webhook request(s) despite modified notification config; first headers: %v",
			len(got), got[0].Header)
	}
	if !strings.Contains(out.CommentBody, "Notification channels suppressed for this preview") {
		t.Errorf("comment must surface the suppression: %s", out.CommentBody)
	}
	if !strings.Contains(out.CommentBody, "channels resume after approval/apply") {
		t.Errorf("comment must say channels resume post-approval: %s", out.CommentBody)
	}
	if strings.Contains(out.CommentBody, secret) {
		t.Errorf("secret leaked into the PR comment")
	}
}

// TestPreviewDesignatedExpansionWorksWhenConfigUntouched is the companion
// assertion: when the PR does NOT touch notification config, the webhook
// channel dispatches normally and the designated-field env expansion
// (header values) is honored end-to-end.
func TestPreviewDesignatedExpansionWorksWhenConfigUntouched(t *testing.T) {
	const secret = "hook-token-abcdef123456"
	t.Setenv("EXFIL_SECRET", secret)

	srv := newRecordingServer(t)
	cfg := loadExfilConfig(t, srv.URL)

	fvcs := &fakeVCS{
		changed: []string{"projects/api/main.ts"},
		headSHA: "clean-sha",
	}
	out, err := Preview(context.Background(), securityPreviewInput(t, cfg, fvcs))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	got := srv.recorded()
	if len(got) != 2 { // planning + plan
		t.Fatalf("expected 2 webhook deliveries (planning, plan), got %d", len(got))
	}
	for _, r := range got {
		if h := r.Header.Get("X-Auth"); h != "Bearer "+secret {
			t.Errorf("designated header expansion broken: X-Auth = %q", h)
		}
	}
	if strings.Contains(out.CommentBody, "Notification channels suppressed") {
		t.Errorf("untouched config must not suppress: %s", out.CommentBody)
	}
}

// TestPreviewFailsClosedWhenChangedFilesUnavailable: a VCS-connected run
// whose changed-file list cannot be fetched must not dispatch any channel
// event (the planning event fires before the run would abort otherwise).
func TestPreviewFailsClosedWhenChangedFilesUnavailable(t *testing.T) {
	t.Setenv("EXFIL_SECRET", "hook-token-abcdef123456")
	srv := newRecordingServer(t)
	cfg := loadExfilConfig(t, srv.URL)

	fvcs := &failingVCS{}
	in := securityPreviewInput(t, cfg, &fakeVCS{})
	in.VCS = fvcs
	_, err := Preview(context.Background(), in)
	if err == nil {
		t.Fatal("expected the preview to fail when changed files are unavailable")
	}
	if got := srv.recorded(); len(got) != 0 {
		t.Fatalf("no channel dispatch may happen when the changed-file list is unavailable, got %d", len(got))
	}
}

type failingVCS struct{ fakeVCS }

func (f *failingVCS) ListChangedFiles(context.Context, int) ([]string, error) {
	return nil, errors.New("api unavailable")
}

func TestSuppressPreApprovalChannels(t *testing.T) {
	cases := []struct {
		name       string
		local      bool
		hasVCS     bool
		changed    []string
		changedErr error
		sources    []string
		want       bool
	}{
		{"local never suppresses", true, false, nil, nil, nil, false},
		{"no vcs fails closed", false, false, nil, nil, nil, true},
		{"changed-list error fails closed", false, true, nil, errors.New("boom"), nil, true},
		{"notifications.yaml modified", false, true, []string{".reeve/notifications.yaml"}, nil, []string{".reeve/notifications.yaml"}, true},
		{"drift.yaml modified (default sources)", false, true, []string{".reeve/drift.yaml"}, nil, nil, true},
		{"unrelated changes pass", false, true, []string{"projects/api/main.ts"}, nil, []string{".reeve/notifications.yaml"}, false},
		{"custom source file name honored", false, true, []string{".reeve/notify.yml"}, nil, []string{".reeve/notify.yml"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := SuppressPreApprovalChannels(tc.local, tc.hasVCS, tc.changed, tc.changedErr, tc.sources)
			if got != tc.want {
				t.Fatalf("suppress = %v (reason %q), want %v", got, reason, tc.want)
			}
			if got && reason == "" {
				t.Fatal("suppression must carry a reason")
			}
		})
	}
}

// loadObservabilityConfig writes a .reeve/observability.yaml pointing the
// OTLP exporter at collectorURL with a ${env:EXFIL_SECRET} auth header,
// then loads it through the real config loader.
func loadObservabilityConfig(t *testing.T, collectorURL string) *config.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".reeve"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `version: 1
config_type: observability
otel:
  enabled: true
  endpoint: ` + collectorURL + `
  service_name: reeve-test
  headers:
    Authorization: "Bearer ${env:EXFIL_SECRET}"
`
	if err := os.WriteFile(filepath.Join(root, ".reeve", "observability.yaml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// TestPreviewOTELSuppressedWhenObservabilityConfigModified is the OTLP
// variant of the exfil attack: the PR adds/modifies observability.yaml
// pointing otel.endpoint + headers at an attacker collector. The
// pre-approval preview must not initialize the exporter at all - zero
// connections to the collector - and the PR comment must say why.
func TestPreviewOTELSuppressedWhenObservabilityConfigModified(t *testing.T) {
	const secret = "ghp_superSecretDoNotExfilOTEL00000000000"
	t.Setenv("EXFIL_SECRET", secret)

	collector := newRecordingServer(t)
	cfg := loadObservabilityConfig(t, collector.URL)
	if len(cfg.ObservabilitySourceFiles) == 0 {
		t.Fatal("loader must record which files declared observability config")
	}

	fvcs := &fakeVCS{
		changed: []string{".reeve/observability.yaml", "projects/api/main.ts"},
		headSHA: "attack-sha",
	}
	in := securityPreviewInput(t, cfg, fvcs)
	in.Observability = cfg.Observability
	in.ObservabilitySourceFiles = cfg.ObservabilitySourceFiles
	out, err := Preview(context.Background(), in)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	if got := collector.recorded(); len(got) != 0 {
		t.Fatalf("SECURITY FAILURE: %d OTLP request(s) reached the collector despite modified observability config; first headers: %v",
			len(got), got[0].Header)
	}
	if !strings.Contains(out.CommentBody, "Telemetry (OTEL) suppressed for this preview") {
		t.Errorf("comment must surface the OTEL suppression: %s", out.CommentBody)
	}
	if !strings.Contains(out.CommentBody, "telemetry resumes after approval/apply") {
		t.Errorf("comment must say telemetry resumes post-approval: %s", out.CommentBody)
	}
}

// TestPreviewOTELExportsWhenConfigUntouched is the companion assertion:
// with observability.yaml untouched by the PR, the exporter initializes,
// flushes to the configured collector, and carries the expanded
// (designated-field) auth header.
func TestPreviewOTELExportsWhenConfigUntouched(t *testing.T) {
	const secret = "otel-collector-token-123456"
	t.Setenv("EXFIL_SECRET", secret)

	collector := newRecordingServer(t)
	cfg := loadObservabilityConfig(t, collector.URL)

	fvcs := &fakeVCS{
		changed: []string{"projects/api/main.ts"},
		headSHA: "clean-sha",
	}
	in := securityPreviewInput(t, cfg, fvcs)
	in.Observability = cfg.Observability
	in.ObservabilitySourceFiles = cfg.ObservabilitySourceFiles
	out, err := Preview(context.Background(), in)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	got := collector.recorded()
	if len(got) == 0 {
		t.Fatal("expected at least one OTLP flush to the collector for untouched config")
	}
	for _, r := range got {
		if h := r.Header.Get("Authorization"); h != "Bearer "+secret {
			t.Errorf("designated otel header expansion broken: Authorization = %q", h)
		}
	}
	if strings.Contains(out.CommentBody, "Telemetry (OTEL) suppressed") {
		t.Errorf("untouched config must not suppress telemetry: %s", out.CommentBody)
	}
}

func TestSuppressPreApprovalObservability(t *testing.T) {
	cases := []struct {
		name       string
		local      bool
		hasVCS     bool
		changed    []string
		changedErr error
		sources    []string
		want       bool
	}{
		{"local never suppresses", true, false, nil, nil, nil, false},
		{"no vcs fails closed", false, false, nil, nil, nil, true},
		{"changed-list error fails closed", false, true, nil, errors.New("boom"), nil, true},
		{"observability.yaml modified (default sources)", false, true, []string{".reeve/observability.yaml"}, nil, nil, true},
		{"custom source file honored", false, true, []string{".reeve/obs.yml"}, nil, []string{".reeve/obs.yml"}, true},
		{"unrelated changes pass", false, true, []string{"projects/api/main.ts"}, nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := SuppressPreApprovalObservability(tc.local, tc.hasVCS, tc.changed, tc.changedErr, tc.sources)
			if got != tc.want {
				t.Fatalf("suppress = %v (reason %q), want %v", got, reason, tc.want)
			}
			if got && reason == "" {
				t.Fatal("suppression must carry a reason")
			}
		})
	}
}

// TestPreviewRegistersSecretProviderValuesWithRedactor: every env value a
// secret provider exports must be registered with the redactor, so a
// secret that leaks into engine output never reaches the PR comment.
func TestPreviewRegistersSecretProviderValuesWithRedactor(t *testing.T) {
	const secret = "hush-token-1234567890"
	t.Setenv("REEVE_TEST_UPSTREAM_SECRET", secret)

	authCfg := &schemas.Auth{
		Providers: map[string]schemas.ProviderYAML{
			"custom-token": {Type: "github_secret", EnvVar: "REEVE_TEST_UPSTREAM_SECRET",
				EnvMap: map[string]string{"MY_TOKEN": ""}},
		},
		Bindings: []schemas.BindingYAML{
			{Match: schemas.BindingMatch{Stack: "api/*"}, Providers: []string{"custom-token"}},
		},
	}
	reg, err := factory.Build(context.Background(), authCfg)
	if err != nil {
		t.Fatalf("factory.Build: %v", err)
	}

	// The engine "leaks" the secret into its plan summary.
	engine := &fakeEngine{
		enum: []discovery.Stack{{Project: "api", Path: "projects/api", Name: "dev", Env: "dev"}},
		results: map[string]iac.PreviewResult{
			"api/dev": {Counts: summary.Counts{Add: 1}, PlanSummary: "+ bucket with token " + secret},
		},
	}
	store, _ := filesystem.New(t.TempDir())
	fvcs := &fakeVCS{changed: []string{"projects/api/main.ts"}, headSHA: "sha"}
	out, err := Preview(context.Background(), PreviewInput{
		PRNumber:  3,
		CommitSHA: "sha",
		RunNumber: 1,
		RepoRoot:  "/nope",
		Engine:    engine,
		Config: &schemas.Engine{Engine: schemas.EngineBody{
			Stacks: []schemas.StackDecl{{Project: "api", Path: "projects/api", Stacks: []string{"dev"}}},
		}},
		Shared:       &schemas.Shared{},
		AuthConfig:   authCfg,
		AuthRegistry: reg,
		Blob:         store,
		VCS:          fvcs,
		Comments:     fvcs,
	})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if strings.Contains(out.CommentBody, secret) {
		t.Fatalf("secret value reached the PR comment: %s", out.CommentBody)
	}
	if len(out.Stacks) != 1 || strings.Contains(out.Stacks[0].PlanSummary, secret) {
		t.Fatalf("secret value survived redaction in the plan summary: %+v", out.Stacks)
	}
	if !strings.Contains(out.Stacks[0].PlanSummary, "[redacted]") {
		t.Fatalf("expected [redacted] marker in plan summary: %q", out.Stacks[0].PlanSummary)
	}
}
