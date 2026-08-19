package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestActionRunBlocksContainNoExpressions(t *testing.T) {
	data := readRepoFile(t, "action.yml")
	scanner := bufio.NewScanner(strings.NewReader(data))
	inRun := false
	runIndent := 0
	for scanner.Scan() {
		line := scanner.Text()
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.TrimSpace(line) == "run: |" {
			inRun = true
			runIndent = indent
			continue
		}
		if inRun && strings.TrimSpace(line) != "" && indent <= runIndent {
			inRun = false
		}
		if inRun && strings.Contains(line, "${{") {
			t.Errorf("GitHub expression embedded in run script: %s", strings.TrimSpace(line))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowActionsArePinned(t *testing.T) {
	sha := regexp.MustCompile(`^[0-9a-f]{40}$`)
	paths := []string{repoPath(t, "action.yml")}
	workflows, err := filepath.Glob(repoPath(t, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	yamlWorkflows, err := filepath.Glob(repoPath(t, ".github", "workflows", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	workflows = append(workflows, yamlWorkflows...)
	paths = append(paths, workflows...)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "-"))
			if !strings.HasPrefix(line, "uses:") {
				continue
			}
			ref := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "uses:")))[0]
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
				continue
			}
			at := strings.LastIndexByte(ref, '@')
			if at < 0 || !sha.MatchString(ref[at+1:]) {
				t.Errorf("%s has mutable action ref %q", path, ref)
			}
		}
	}
}

func TestActionInputsCannotExecuteShellSyntax(t *testing.T) {
	script := extractRunReeveScript(t, readRepoFile(t, "action.yml"))
	dir := t.TempDir()
	marker := filepath.Join(dir, "injected")
	argsPath := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "reeve")
	fakeBody := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_OUT\"\n"
	if err := os.WriteFile(fake, []byte(fakeBody), 0o700); err != nil {
		t.Fatal(err)
	}

	// #nosec G204 -- bash executes the repository-owned action script; hostile text is supplied only through env.
	cmd := exec.Command("bash", "-c", script)
	for key, value := range map[string]string{
		"GITHUB_WORKSPACE":           dir,
		"GITHUB_EVENT_NAME":          "workflow_dispatch",
		"REEVE_BIN":                  fake,
		"REEVE_INPUT_ROOT":           dir,
		"REEVE_INPUT_COMMAND":        "lint",
		"REEVE_INPUT_EXTRA_ARGS":     "$(touch " + marker + ") ; touch " + marker,
		"REEVE_RUN_ON_APPROVAL":      "false",
		"REEVE_ALLOWED_ASSOCIATIONS": "OWNER",
		"REEVE_COMMAND_PREFIXES":     "/reeve",
		"ARGS_OUT":                   argsPath,
	} {
		t.Setenv(key, value)
	}
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("action script failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("input text executed as shell syntax: %v", err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "$(touch") {
		t.Fatalf("hostile text was not passed as inert argv: %q", args)
	}
}

func TestActionPlanCommentMarksExplicitRequest(t *testing.T) {
	got := runActionPreview(t, "issue_comment", `{
		"issue":{"number":42,"pull_request":{}},
		"comment":{"body":"/reeve plan","author_association":"OWNER","user":{"type":"User","login":"operator"}}
	}`)
	want := []string{"run", "preview", "--pr", "42", "--run-url", "https://github.com/org/repo/actions/runs/123", "--plan-requested"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("plan command args = %q, want %q", got, want)
	}
}

func TestActionPushPreviewOmitsExplicitRequest(t *testing.T) {
	got := runActionPreview(t, "pull_request", `{
		"action":"synchronize",
		"pull_request":{"number":42}
	}`)
	want := []string{"run", "preview", "--pr", "42", "--run-url", "https://github.com/org/repo/actions/runs/123"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("push preview args = %q, want %q", got, want)
	}
}

func TestPlanRequestedFlagIsPreviewOnly(t *testing.T) {
	runCmd := newRunCmd()
	preview, _, err := runCmd.Find([]string{"preview"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Flags().Lookup("plan-requested") == nil {
		t.Fatal("run preview is missing --plan-requested")
	}
	for _, name := range []string{"apply", "refresh"} {
		cmd, _, err := runCmd.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if cmd.Flags().Lookup("plan-requested") != nil {
			t.Fatalf("run %s unexpectedly accepts --plan-requested", name)
		}
	}
}

func runActionPreview(t *testing.T, eventName, eventJSON string) []string {
	t.Helper()
	script := extractRunReeveScript(t, readRepoFile(t, "action.yml"))
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	argsPath := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "reeve")
	if err := os.WriteFile(eventPath, []byte(eventJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_OUT\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	// #nosec G204 -- bash executes the repository-owned action script with a fixed event fixture.
	cmd := exec.Command("bash", "-c", script)
	for key, value := range map[string]string{
		"GITHUB_WORKSPACE":           dir,
		"GITHUB_EVENT_NAME":          eventName,
		"GITHUB_EVENT_PATH":          eventPath,
		"GITHUB_SERVER_URL":          "https://github.com",
		"GITHUB_REPOSITORY":          "org/repo",
		"GITHUB_RUN_ID":              "123",
		"REEVE_BIN":                  fake,
		"REEVE_INPUT_ROOT":           dir,
		"REEVE_INPUT_COMMAND":        "",
		"REEVE_INPUT_EXTRA_ARGS":     "",
		"REEVE_RUN_ON_APPROVAL":      "false",
		"REEVE_ALLOWED_ASSOCIATIONS": "OWNER",
		"REEVE_COMMAND_PREFIXES":     "/reeve",
		"ARGS_OUT":                   argsPath,
	} {
		t.Setenv(key, value)
	}
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("action script failed: %v\n%s", err, output)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(args))
}

func TestBinaryFetchRejectsMissingSignatureVerifier(t *testing.T) {
	script := repoPath(t, ".github", "scripts", "fetch-binary.sh")
	sums := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(sums, []byte("unused"), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- bash sources the repository-owned helper with fixed test arguments.
	cmd := exec.Command("bash", "-c", `source "$1"; if verify_signature "$2" "$3"; then exit 9; fi`,
		"test", script, sums, filepath.Join(t.TempDir(), "missing.bundle"))
	cmd.Env = []string{"PATH=/nonexistent"}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("missing verifier must be a handled verification failure: %v\n%s", err, output)
	}
}

func extractRunReeveScript(t *testing.T, action string) string {
	t.Helper()
	lines := strings.Split(action, "\n")
	seenStep := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "- name: Run reeve" {
			seenStep = true
			continue
		}
		if !seenStep || strings.TrimSpace(line) != "run: |" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		var body []string
		for _, candidate := range lines[i+1:] {
			candidateIndent := len(candidate) - len(strings.TrimLeft(candidate, " "))
			if strings.TrimSpace(candidate) != "" && candidateIndent <= indent {
				break
			}
			if len(candidate) >= indent+2 {
				candidate = candidate[indent+2:]
			}
			body = append(body, candidate)
		}
		return strings.Join(body, "\n")
	}
	t.Fatal("Run reeve script not found")
	return ""
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(repoPath(t, parts...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	root := filepath.Join("..", "..")
	return filepath.Join(append([]string{root}, parts...)...)
}
