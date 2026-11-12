package log

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/core/redact"
)

func TestLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"DEBUG", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"garbage", slog.LevelInfo},
	}
	for _, c := range cases {
		if got := Level(c.in); got != c.want {
			t.Errorf("Level(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseFormat(t *testing.T) {
	if ParseFormat("json") != FormatJSON {
		t.Error("ParseFormat(json) should be JSON")
	}
	if ParseFormat("JSON") != FormatJSON {
		t.Error("ParseFormat is case-insensitive")
	}
	if ParseFormat("text") != FormatText {
		t.Error("ParseFormat(text) should be Text")
	}
	if ParseFormat("") != FormatText {
		t.Error("empty defaults to Text")
	}
}

func TestInstallEmitsAtLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := Install(&buf, slog.LevelWarn, FormatText)
	logger.Info("hidden")
	logger.Warn("shown")
	out := buf.String()
	if strings.Contains(out, "hidden") {
		t.Errorf("info-level message leaked at warn threshold: %s", out)
	}
	if !strings.Contains(out, "shown") {
		t.Errorf("warn-level message missing: %s", out)
	}
}

func TestInstallSetsDefault(t *testing.T) {
	var buf bytes.Buffer
	Install(&buf, slog.LevelInfo, FormatJSON)
	slog.Info("hello", "k", "v")
	if !strings.Contains(buf.String(), `"msg":"hello"`) {
		t.Errorf("default logger did not pick up JSON handler: %s", buf.String())
	}
}

// TestSinkRedactsRegisteredSecret pins the sink invariant: the log handler
// applies the invocation-scoped redactor to EVERY record - message and
// attribute values - so a secret registered during the run (an auth env
// value, a recovered plan artifact token) cannot leak through a log line in
// either format.
func TestSinkRedactsRegisteredSecret(t *testing.T) {
	for _, c := range []struct {
		name   string
		format Format
	}{
		{"text", FormatText},
		{"json", FormatJSON},
	} {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := Install(&buf, slog.LevelInfo, c.format)
			r := redact.New()
			r.AddSecret("s3cr3t-value-42")
			SetRedactor(r)
			t.Cleanup(func() { SetRedactor(nil) })

			logger.Warn("engine output contained s3cr3t-value-42 in the body",
				"err", "manifest write failed near s3cr3t-value-42")

			out := buf.String()
			if strings.Contains(out, "s3cr3t-value-42") {
				t.Fatalf("registered secret leaked to the log sink: %s", out)
			}
			if !strings.Contains(out, "[redacted]") {
				t.Fatalf("expected masked output:\n%s", out)
			}
		})
	}
}

// TestSinkWithoutRedactorUsesRuleFloor: nil restores the credential-rule
// floor, never a bare passthrough - a logger with no redactor installed
// must not be reachable. Plain text survives; a rule-matching credential is
// masked even then.
func TestSinkWithoutRedactorUsesRuleFloor(t *testing.T) {
	var buf bytes.Buffer
	logger := Install(&buf, slog.LevelInfo, FormatText)
	SetRedactor(nil)
	logger.Info("plain message", "k", "plain-value with nothing to hide",
		"cred", "ghp_"+strings.Repeat("A", 36))
	out := buf.String()
	if !strings.Contains(out, "plain-value with nothing to hide") {
		t.Fatalf("plain text must survive the rule floor: %s", out)
	}
	if strings.Contains(out, "ghp_AAAA") {
		t.Fatalf("credential leaked with only the rule floor: %s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("the rule floor must mask rule-matching values: %s", out)
	}
}

// TestSinkRedactsEveryAttributeKind pins the sink guarantee across kinds:
// error attributes, fmt.Stringers and LogValuers render to text at emit
// time and MUST pass the redactor - the old handler only handled
// KindString, so an error attribute leaked raw.
func TestSinkRedactsEveryAttributeKind(t *testing.T) {
	secret := "s3cr3t-value-42"
	secretError := errors.New("put https://state.example/plan.json failed near " + secret)
	stringer := stringerWithSecret{secret}
	resolver := &logValuerWithSecret{secret}

	for _, c := range []struct {
		name   string
		format Format
	}{
		{"text", FormatText},
		{"json", FormatJSON},
	} {
		t.Run(c.name+"/error", func(t *testing.T) {
			var buf bytes.Buffer
			logger := Install(&buf, slog.LevelInfo, c.format)
			SetRedactor(funcWithSecrets(secret))
			t.Cleanup(func() { SetRedactor(nil) })
			logger.Warn("manifest persistence failed", "err", secretError)
			out := buf.String()
			if strings.Contains(out, secret) {
				t.Fatalf("error attribute leaked: %s", out)
			}
			if !strings.Contains(out, "put https://state.example/plan.json failed near") {
				t.Fatalf("error text must survive redacted: %s", out)
			}
			if !strings.Contains(out, "[redacted]") {
				t.Fatalf("expected the masked value: %s", out)
			}
		})
		t.Run(c.name+"/stringer", func(t *testing.T) {
			var buf bytes.Buffer
			logger := Install(&buf, slog.LevelInfo, c.format)
			SetRedactor(funcWithSecrets(secret))
			t.Cleanup(func() { SetRedactor(nil) })
			logger.Warn("stack render failed", "stack", stringer)
			out := buf.String()
			if strings.Contains(out, secret) {
				t.Fatalf("Stringer attribute leaked: %s", out)
			}
			if !strings.Contains(out, "stack-api/prod near") {
				t.Fatalf("Stringer text must survive redacted: %s", out)
			}
		})
		t.Run(c.name+"/logvaluer", func(t *testing.T) {
			var buf bytes.Buffer
			logger := Install(&buf, slog.LevelInfo, c.format)
			SetRedactor(funcWithSecrets(secret))
			t.Cleanup(func() { SetRedactor(nil) })
			logger.Warn("state fetch failed", "value", slog.AnyValue(resolver))
			out := buf.String()
			if strings.Contains(out, secret) {
				t.Fatalf("LogValuer attribute leaked: %s", out)
			}
			if !strings.Contains(out, "resolved-cred") {
				t.Fatalf("LogValuer text must survive redacted: %s", out)
			}
		})
	}
}

func funcWithSecrets(secrets ...string) *redact.Redactor {
	r := redact.NewDefault()
	for _, s := range secrets {
		r.AddSecret(s)
	}
	return r
}

type stringerWithSecret struct{ secret string }

func (s stringerWithSecret) String() string { return "stack-api/prod near " + s.secret }

type logValuerWithSecret struct{ secret string }

func (s *logValuerWithSecret) LogValue() slog.Value {
	return slog.StringValue("resolved-cred " + s.secret)
}
