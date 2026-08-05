package iac

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

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
