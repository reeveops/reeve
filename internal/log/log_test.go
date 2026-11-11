package log

import (
	"bytes"
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

// TestSinkWithoutRedactorPassthrough: with no redactor installed the sink
// is a plain passthrough - install must not alter output.
func TestSinkWithoutRedactorPassthrough(t *testing.T) {
	var buf bytes.Buffer
	logger := Install(&buf, slog.LevelInfo, FormatText)
	SetRedactor(nil)
	logger.Info("plain message", "k", "plain-value with nothing to hide")
	if !strings.Contains(buf.String(), "plain-value with nothing to hide") {
		t.Fatalf("passthrough altered output: %s", buf.String())
	}
}
