package iac

import (
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

var ambientChildEnv = map[string]struct{}{
	"COLORTERM":     {},
	"FORCE_COLOR":   {},
	"LANG":          {},
	"LANGUAGE":      {},
	"NO_COLOR":      {},
	"PATH":          {},
	"SSL_CERT_DIR":  {},
	"SSL_CERT_FILE": {},
	"TEMP":          {},
	"TERM":          {},
	"TMP":           {},
	"TMPDIR":        {},
	"TZ":            {},
}

// CommandEnv builds a child environment without inheriting controller
// credentials. The cleanup removes the isolated home created in CI.
func CommandEnv(explicit, defaults map[string]string) ([]string, func(), error) {
	env := make(map[string]string, len(ambientChildEnv)+len(defaults)+len(explicit))
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		_, allowed := ambientChildEnv[key]
		if allowed || strings.HasPrefix(key, "LC_") {
			env[key] = value
		}
	}
	cleanup := func() {}
	_, explicitHome := explicit["HOME"]
	_, defaultHome := defaults["HOME"]
	if os.Getenv("CI") != "" {
		if !explicitHome && !defaultHome {
			homeEnv, homeCleanup, err := ExecutionEnv()
			if err != nil {
				return nil, cleanup, err
			}
			cleanup = homeCleanup
			for key, value := range homeEnv {
				env[key] = value
			}
		}
	} else {
		for _, key := range []string{"HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME"} {
			if value, ok := os.LookupEnv(key); ok {
				env[key] = value
			}
		}
	}
	for key, value := range defaults {
		env[key] = value
	}
	for key, value := range explicit {
		env[key] = value
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out, cleanup, nil
}

// ExecutionEnv returns run-scoped home settings in CI and no settings for a
// local run. Its cleanup removes every file the child writes under that home.
func ExecutionEnv() (map[string]string, func(), error) {
	cleanup := func() {}
	if os.Getenv("CI") == "" {
		return nil, cleanup, nil
	}
	home, err := os.MkdirTemp("", "reeve-child-home-")
	if err != nil {
		return nil, cleanup, err
	}
	cleanup = func() { _ = os.RemoveAll(home) }
	return map[string]string{
		"HOME":            home,
		"XDG_CACHE_HOME":  home + "/.cache",
		"XDG_CONFIG_HOME": home + "/.config",
		"XDG_DATA_HOME":   home + "/.local/share",
	}, cleanup, nil
}

// DefaultStopGrace is how long a cancelled engine subprocess gets between
// SIGINT and SIGKILL. Engine CLIs (pulumi up, terraform apply) use the
// grace period to shut down cleanly and release their state locks; a
// straight SIGKILL orphans engine-side locks and can corrupt in-flight
// state writes.
const DefaultStopGrace = 30 * time.Second

// SetupGracefulStop configures cmd (built with exec.CommandContext) so a
// context cancel/timeout sends SIGINT instead of the default SIGKILL, then
// waits up to grace (DefaultStopGrace when <= 0) before the kill.
// Call after exec.CommandContext and before cmd.Start/Run.
func SetupGracefulStop(cmd *exec.Cmd, grace time.Duration) {
	if grace <= 0 {
		grace = DefaultStopGrace
	}
	cmd.Cancel = func() error {
		err := cmd.Process.Signal(os.Interrupt)
		if err == nil || errors.Is(err, os.ErrProcessDone) {
			return err
		}
		// Interrupt failed (unsupported platform or signal error): fall
		// back to the default hard kill rather than leaving the process.
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = grace
}
