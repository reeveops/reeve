package iac

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCommandEnvExcludesAmbientCredentials(t *testing.T) {
	t.Setenv("REEVE_SENTINEL_SECRET", "controller-only")
	t.Setenv("GITHUB_TOKEN", "github-controller-token")
	t.Setenv("PATH", "/safe/bin")

	env, cleanup, err := CommandEnv(map[string]string{"AWS_ACCESS_KEY_ID": "bound-key"}, map[string]string{
		"TF_IN_AUTOMATION": "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got := envMap(env)
	if got["PATH"] != "/safe/bin" {
		t.Fatalf("PATH = %q, want allowlisted ambient value", got["PATH"])
	}
	if got["AWS_ACCESS_KEY_ID"] != "bound-key" || got["TF_IN_AUTOMATION"] != "1" {
		t.Fatalf("explicit/default values missing: %#v", got)
	}
	if _, ok := got["GITHUB_TOKEN"]; ok {
		t.Fatal("GITHUB_TOKEN leaked into child environment")
	}
	if _, ok := got["REEVE_SENTINEL_SECRET"]; ok {
		t.Fatal("sentinel leaked into child environment")
	}
}

func TestCommandEnvExplicitValueWins(t *testing.T) {
	t.Setenv("PATH", "/ambient/bin")
	env, cleanup, err := CommandEnv(map[string]string{"PATH": "/explicit/bin"}, map[string]string{
		"PATH": "/default/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got := envMap(env)
	if got["PATH"] != "/explicit/bin" {
		t.Fatalf("PATH = %q, want explicit value", got["PATH"])
	}
}

func TestCommandEnvUsesIsolatedHomeInCI(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("HOME", "/controller/home")
	env, cleanup, err := CommandEnv(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	home := envMap(env)["HOME"]
	if home == "" || home == "/controller/home" {
		t.Fatalf("HOME = %q, want isolated directory", home)
	}
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		t.Fatalf("isolated home missing: %v", err)
	}
	cleanup()
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("isolated home survived cleanup: %v", err)
	}
}

func TestCommandEnvPreservesExplicitExecutionHome(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("XDG_CONFIG_HOME", "/controller/config")
	home := t.TempDir()
	env, cleanup, err := CommandEnv(map[string]string{"HOME": home}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if envMap(env)["HOME"] != home {
		t.Fatalf("HOME was not preserved: %#v", env)
	}
	if _, ok := envMap(env)["XDG_CONFIG_HOME"]; ok {
		t.Fatalf("ambient XDG_CONFIG_HOME leaked beside explicit HOME: %#v", env)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("command cleanup removed run-scoped home: %v", err)
	}
}

func TestSubprocessPackagesDoNotInheritFullEnvironment(t *testing.T) {
	needle := "os." + "Environ()"
	for _, root := range []string{".", filepath.Join("..", "policy")} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
				return err
			}
			if root == "." && filepath.Clean(path) == "exec.go" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), needle) {
				t.Errorf("%s directly inherits the controller environment; use CommandEnv", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", root, err)
		}
	}
}

func envMap(items []string) map[string]string {
	out := make(map[string]string, len(items))
	for _, item := range items {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

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
