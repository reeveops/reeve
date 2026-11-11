// Package log is reeve's structured logging seam. It wraps log/slog with a
// process-wide default that subcommands install once at startup and that
// every other package reads via slog.Default().
//
// Conventions:
//   - Debug:  per-iteration trace (e.g. each check_run inspected). Off by default.
//   - Info:   user-facing progress that belongs in CI logs at normal verbosity.
//   - Warn:   recoverable adapter failures - the run continues but the operator
//     should know (Slack post failed, audit write failed, lock release
//     returned an error after work shipped).
//   - Error:  unrecoverable failures that abort the run. Almost always paired
//     with returning the error up the stack.
//
// No package-level mutable state is exported - callers use slog.Default().
package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"github.com/reeveops/reeve/internal/core/redact"
)

// invocationRedactor is the process-scoped redaction registry every log
// record passes through. Runs install their invocation-scoped redactor
// (registered auth env values + credential rules) at startup; a nil
// redactor is a passthrough.
var invocationRedactor atomic.Pointer[redact.Redactor]

// SetRedactor installs the redactor applied to every log record from this
// process. Call it once per run, after registering values that are secret
// in that run. nil clears it (plain passthrough), so tests and non-running
// callers are unaffected.
func SetRedactor(r *redact.Redactor) { invocationRedactor.Store(r) }

// redactValue applies the current redactor to a value the sink is about to
// emit. Only strings (and the string leaves of groups) carry leak potential;
// other kinds pass through untouched.
func redactValue(a slog.Attr) slog.Attr {
	red := invocationRedactor.Load()
	if red == nil {
		return a
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, red.Redact(a.Value.String()))
	case slog.KindGroup:
		group := a.Value.Group()
		out := make([]slog.Attr, 0, len(group))
		for _, ga := range group {
			out = append(out, redactValue(ga))
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	default:
		return a
	}
}

// redactRecord copies r with the message and all attribute values passed
// through the current redactor.
func redactRecord(r slog.Record) slog.Record {
	red := invocationRedactor.Load()
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	if red != nil {
		out.Message = red.Redact(r.Message)
	}
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactValue(a))
		return true
	})
	return out
}

// Level parses a level string (case-insensitive) into a slog.Level. Unknown
// values fall back to LevelInfo. The empty string also resolves to Info so
// callers can pass an unset env var directly.
func Level(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Format selects between text (human-readable) and json output. Default is
// text on TTY-attached stderr; CI workflows can opt into json by setting
// REEVE_LOG_FORMAT=json.
type Format int

const (
	FormatText Format = iota
	FormatJSON
)

// String names the format for log output, so a "logging configured" line
// reports "text"/"json" rather than an opaque enum ordinal.
func (f Format) String() string {
	if f == FormatJSON {
		return "json"
	}
	return "text"
}

// ParseFormat maps "text" / "json" (case-insensitive) to a Format. Empty or
// unknown values resolve to FormatText.
func ParseFormat(name string) Format {
	if strings.EqualFold(strings.TrimSpace(name), "json") {
		return FormatJSON
	}
	return FormatText
}

// Install builds a handler at the given level/format and installs it as
// slog.Default(). Returns the installed logger for callers that want to pass it
// explicitly instead of relying on the default.
//
// When w is nil, records split by level: Debug/Info -> stdout, Warn/Error ->
// stderr. This matters under GitHub Actions, which renders ANY stderr line as a
// red "Error:" - routing normal logs to stdout stops debug traces from looking
// like failures. When w is non-nil (tests), everything goes to w.
func Install(w io.Writer, level slog.Level, format Format) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if w != nil {
		h = newHandler(w, format, opts)
	} else {
		h = &splitHandler{
			out: newHandler(os.Stdout, format, opts),
			err: newHandler(os.Stderr, format, opts),
		}
	}
	logger := slog.New(h)
	slog.SetDefault(logger)
	return logger
}

func newHandler(w io.Writer, format Format, opts *slog.HandlerOptions) slog.Handler {
	var h slog.Handler
	if format == FormatJSON {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	// The sink redacts first: nothing reaching a log line bypasses
	// internal/core/redact, whatever the caller-level formatting.
	return &redactingHandler{next: h}
}

// redactingHandler wraps any slog.Handler and passes every record - message
// and attribute values - through the invocation-scoped redactor before the
// underlying handler formats it. Producers still redact at the source where
// they know the context; this guarantees the sink.
type redactingHandler struct {
	next slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.next.Handle(ctx, redactRecord(r))
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactValue(a)
	}
	return &redactingHandler{next: h.next.WithAttrs(out)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

// splitHandler routes Warn/Error to err and everything below to out. Both
// wrapped handlers share the same level threshold, so Enabled/attrs/groups
// delegate to either (out) consistently.
type splitHandler struct {
	out slog.Handler
	err slog.Handler
}

func (s *splitHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return s.out.Enabled(ctx, l)
}

func (s *splitHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		return s.err.Handle(ctx, r)
	}
	return s.out.Handle(ctx, r)
}

func (s *splitHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &splitHandler{out: s.out.WithAttrs(attrs), err: s.err.WithAttrs(attrs)}
}

func (s *splitHandler) WithGroup(name string) slog.Handler {
	return &splitHandler{out: s.out.WithGroup(name), err: s.err.WithGroup(name)}
}

// FromEnv installs a logger from REEVE_LOG_LEVEL and REEVE_LOG_FORMAT,
// suitable for `cmd/reeve` PersistentPreRun and for tests that need
// deterministic output. Honours an explicit override pair when non-empty.
func FromEnv(levelOverride, formatOverride string) *slog.Logger {
	return FromConfig(levelOverride, formatOverride, "", "")
}

// FromConfig installs a logger with priority: flag > env > config file.
// cfgLevel and cfgFormat come from shared.yaml; flag/env take precedence.
func FromConfig(levelFlag, formatFlag, cfgLevel, cfgFormat string) *slog.Logger {
	level := levelFlag
	if level == "" {
		level = os.Getenv("REEVE_LOG_LEVEL")
	}
	if level == "" {
		level = cfgLevel
	}
	format := formatFlag
	if format == "" {
		format = os.Getenv("REEVE_LOG_FORMAT")
	}
	if format == "" {
		format = cfgFormat
	}
	// nil writer -> level-split (Debug/Info to stdout, Warn/Error to stderr).
	lvl, fmtv := Level(level), ParseFormat(format)
	lg := Install(nil, lvl, fmtv)
	// Report what actually won, and from where: an operator who passes
	// --log-level debug must be able to confirm it took effect without
	// inferring it from whether debug lines appear later.
	lg.Debug("logging configured", "level", lvl.String(), "format", fmtv.String(), "source", levelSource(levelFlag))
	return lg
}

// levelSource names the precedence tier that supplied the level, for the
// confirmation line above.
func levelSource(levelFlag string) string {
	switch {
	case levelFlag != "":
		return "flag"
	case os.Getenv("REEVE_LOG_LEVEL") != "":
		return "env"
	default:
		return "config"
	}
}
