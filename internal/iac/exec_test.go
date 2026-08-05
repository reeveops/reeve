package iac

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// writeScript drops an executable shell script into a temp dir.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine.sh")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// waitForFile polls until path exists or the deadline passes.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s never appeared", path)
}

func TestSetupGracefulStopSendsSIGINTFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal test")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	caught := filepath.Join(dir, "caught")
	script := writeScript(t, `#!/bin/sh
trap 'echo sigint > "`+caught+`"; exit 42' INT
echo up > "`+ready+`"
sleep 30 &
wait $!
exit 1
`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, script)
	SetupGracefulStop(cmd, 10*time.Second)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, ready)
	cancel()

	err := cmd.Wait()
	// The script trapped SIGINT and exited 42 on its own - it must NOT have
	// been SIGKILLed.
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 42 {
		t.Fatalf("want clean trap exit 42, got %v", err)
	}
	waitForFile(t, caught)
}

func TestSetupGracefulStopKillsAfterGrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal test")
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	script := writeScript(t, `#!/bin/sh
trap '' INT
echo up > "`+ready+`"
sleep 30 &
wait $!
`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, script)
	SetupGracefulStop(cmd, 200*time.Millisecond)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, ready)
	cancel()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("SIGINT-ignoring process must not exit cleanly")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WaitDelay must SIGKILL a process that ignores SIGINT")
	}
}
