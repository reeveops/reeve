package pulumi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/core/discovery"
	"github.com/reeveops/reeve/internal/iac"
)

func TestEnumerateStacks(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(p, body string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("projects/api/Pulumi.yaml", "name: api\nruntime: nodejs\n")
	mustWrite("projects/api/Pulumi.dev.yaml", "")
	mustWrite("projects/api/Pulumi.prod.yaml", "")
	mustWrite("projects/api/node_modules/junk/Pulumi.yaml", "name: junk\n")
	mustWrite("services/worker/Pulumi.yaml", "name: worker\nruntime: go\n")
	mustWrite("services/worker/Pulumi.prod.yaml", "")

	e := New("")
	got, err := e.EnumerateStacks(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 stacks, got %d: %+v", len(got), got)
	}

	expected := map[string]bool{
		"api/dev":     false,
		"api/prod":    false,
		"worker/prod": false,
	}
	for _, s := range got {
		if _, ok := expected[s.Ref()]; !ok {
			t.Fatalf("unexpected stack %s", s.Ref())
		}
		expected[s.Ref()] = true
	}
	for k, v := range expected {
		if !v {
			t.Fatalf("missing expected stack %s", k)
		}
	}
}

// recordingPulumiBinary writes a fake pulumi binary that appends each
// invocation's argv to a log file, then answers with canned JSON (the
// preview run) or a human diff (any run carrying --diff). The log path is
// returned so tests can put it in the child env (PULUMI_ARGS_LOG) and read
// it back.
func recordingPulumiBinary(t *testing.T, dir string) (string, string) {
	t.Helper()
	argsLog := filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\n" +
		`printf '%s\n' "$*" >> "$PULUMI_ARGS_LOG"` + "\n" +
		`for a in "$@"; do [ "$a" = "--diff" ] && { echo 'Program running: diff'; exit 0; }; done` + "\n" +
		`printf '%s' '{"changeSummary":{"create":1},"steps":[{"op":"create","type":"aws:s3/bucket:Bucket","urn":"urn:pulumi:prod::api::aws:s3/bucket:Bucket::b"}]}'` + "\n"
	fake := filepath.Join(dir, "fake-pulumi")
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return fake, argsLog
}

// TestPreviewDiffMatchesFirstInvocationArgs pins the argument parity fix:
// the human-readable diff is a SECOND `pulumi preview --diff` run, and it
// used to drop the first run's --refresh flag and ExtraArgs. Two runs with
// different arguments can disagree (a refresh changes the displayed
// diff; extra operator args change the plan), so the reviewer-facing diff
// diverged from the saved plan it explains.
func TestPreviewDiffMatchesFirstInvocationArgs(t *testing.T) {
	dir := t.TempDir()
	fake, argsLog := recordingPulumiBinary(t, dir)

	e := New(fake)
	stack := discovery.Stack{Project: "api", Name: "dev", Path: dir}
	opts := iac.PreviewOpts{
		Refresh:   true,
		ExtraArgs: []string{"--logflow", "--color=never"},
		Env:       map[string]string{"PULUMI_ARGS_LOG": argsLog},
	}
	res, err := e.Preview(context.Background(), stack, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("preview errored: %q", res.Error)
	}
	if res.PlanDiff == "" {
		t.Fatalf("a non-clean preview must carry the second-run diff: %+v", res)
	}

	data, err := os.ReadFile(filepath.Join(dir, "argv.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want exactly 2 preview invocations, got %d:\n%s", len(lines), data)
	}
	wantTail := "--refresh=true --logflow --color=never"
	for i, wantHead := range []string{"--json", "--diff"} {
		if !strings.Contains(lines[i], wantHead) {
			t.Fatalf("invocation %d must be the %s run: %q", i+1, wantHead, lines[i])
		}
		if !strings.HasSuffix(lines[i], wantTail) {
			t.Fatalf("invocation %d lost parity with the first run: %q (want suffix %q)", i+1, lines[i], wantTail)
		}
	}
}

// TestPreviewDiffSkippedOnCleanNoOp guards the second-run cost: no diff run
// happens when the preview is a clean no-op.
func TestPreviewDiffSkippedOnCleanNoOp(t *testing.T) {
	dir := t.TempDir()
	fake, argsLog := recordingPulumiBinary(t, dir)
	// Overwrite the fake so every run prints an empty change set.
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PULUMI_ARGS_LOG\"\nprintf '%s' '{\"changeSummary\":{},\"steps\":[]}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	e := New(fake)
	res, err := e.Preview(context.Background(),
		discovery.Stack{Project: "api", Name: "dev", Path: dir},
		iac.PreviewOpts{Env: map[string]string{"PULUMI_ARGS_LOG": argsLog}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("preview errored: %q", res.Error)
	}
	if counts := res.Counts.Total(); counts != 0 {
		t.Fatalf("fixture must be a clean no-op, got %d", counts)
	}
	data, err := os.ReadFile(filepath.Join(dir, "argv.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "--diff") {
		t.Fatalf("clean no-op must not run the second diff invocation:\n%s", data)
	}
}

func TestParsePreviewFromFixture(t *testing.T) {
	json := []byte(`{
		"changeSummary": {"create": 2, "update": 1, "replace": 1},
		"steps": [
			{"op":"create","type":"aws:s3/bucket:Bucket","urn":"urn:pulumi:prod::api::aws:s3/bucket:Bucket::logs-2026"},
			{"op":"create","type":"aws:iam/role:Role","urn":"urn:pulumi:prod::api::aws:iam/role:Role::app-role"},
			{"op":"update","type":"aws:ec2/instance:Instance","urn":"urn:pulumi:prod::api::aws:ec2/instance:Instance::web"},
			{"op":"replace","type":"aws:rds/instance:Instance","urn":"urn:pulumi:prod::api::aws:rds/instance:Instance::db"}
		]
	}`)
	counts, short, diagErr, err := parsePreview(json)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Add != 2 || counts.Change != 1 || counts.Replace != 1 || counts.Delete != 0 {
		t.Fatalf("counts off: %+v", counts)
	}
	if diagErr != "" {
		t.Fatalf("unexpected diagnostic error: %s", diagErr)
	}
	if short == "" {
		t.Fatal("expected non-empty summary")
	}
	if want := "+ logs-2026  (aws:Bucket)"; !contains(short, want) {
		t.Fatalf("summary missing %q: %s", want, short)
	}
}

func TestParsePreviewGroupedSummary(t *testing.T) {
	json := []byte(`{
		"changeSummary": {"create": 1, "update": 1, "same": 3},
		"steps": [
			{"op":"same","urn":"urn:pulumi:prod::api::pulumi:pulumi:Stack::api-prod"},
			{"op":"create",
			 "urn":"urn:pulumi:prod::api::cloudflare:index/zeroTrustTunnelCloudflaredConfig:ZeroTrustTunnelCloudflaredConfig::Apple Pay Abel-config",
			 "newState":{
				"type":"cloudflare:index/zeroTrustTunnelCloudflaredConfig:ZeroTrustTunnelCloudflaredConfig",
				"inputs":{
					"accountId":"0fdf4fa7c335d942f6ec062a4433addf",
					"tunnelId":"74aa6dbe-a42f-4551-921b-e6adc39a6151",
					"config":{"ingresses":[{"hostname":"tunnel-dev1.stg.example.com","service":"http://localhost:3000"},{"service":"http_status:404"}]},
					"token":{"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270","ciphertext":"AAAA"}
				}}},
			{"op":"update",
			 "urn":"urn:pulumi:prod::api::aws:ec2/instance:Instance::web",
			 "type":"aws:ec2/instance:Instance",
			 "detailedDiff":{"instanceType":{"kind":"update"},"tags.env":{"kind":"add"}},
			 "oldState":{"inputs":{"instanceType":"t3.small","tags":{}}},
			 "newState":{"inputs":{"instanceType":"t3.large","tags":{"env":"prod"}}}}
		]
	}`)
	_, short, _, err := parsePreview(json)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"+ Apple Pay Abel-config  (cloudflare:ZeroTrustTunnelCloudflaredConfig)",
		`+     accountId: "0fdf4fa7c335d942f6ec062a4433addf"`,
		`+     config: {ingresses: [{hostname: "tunnel-dev1.stg.example.com", service: "http://localhost:3000"}, {service: "http_status:404"}]}`,
		"+     token: [secret]",
		"~ web  (aws:Instance)",
		`~     instanceType: "t3.small" => "t3.large"`,
		`+     tags.env: "prod"`,
	} {
		if !contains(short, want) {
			t.Fatalf("summary missing %q:\n%s", want, short)
		}
	}
	// The stack "same" step must not appear.
	if contains(short, "api-prod") {
		t.Fatalf("same step leaked into summary:\n%s", short)
	}
}

func TestParsePreviewError(t *testing.T) {
	json := []byte(`{
		"changeSummary": {},
		"steps": [],
		"diagnostics": [{"severity":"error","message":"snake oil"}]
	}`)
	_, _, diagErr, err := parsePreview(json)
	if err != nil {
		t.Fatal(err)
	}
	if diagErr != "snake oil" {
		t.Fatalf("expected snake oil error, got %q", diagErr)
	}
}

func TestFormatDiff(t *testing.T) {
	input := "  + aws:s3/bucket:Bucket\n  - aws:iam/role:Role\n  ~ aws:ec2/instance:Instance\n  normal line"
	got := formatDiff(input)
	lines := splitLines(got)
	if lines[0][0] != '+' {
		t.Errorf("add line should start with +, got %q", lines[0])
	}
	if lines[1][0] != '-' {
		t.Errorf("delete line should start with -, got %q", lines[1])
	}
	if lines[2][0] != '!' {
		t.Errorf("change line should start with !, got %q", lines[2])
	}
	if lines[3] != "  normal line" {
		t.Errorf("normal line should be unchanged, got %q", lines[3])
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
