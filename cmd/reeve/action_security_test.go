package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestActionRunBlocksContainNoExpressions(t *testing.T) {
	data := readRepoFile(t, ".github", "actions", "reeve", "action.yml")
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
	paths := []string{
		repoPath(t, "action.yml"),
		repoPath(t, ".github", "actions", "reeve", "action.yml"),
	}
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
			// Self-repository refs resolve to the workflow's exact commit.
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "$/") || strings.HasPrefix(ref, "docker://") {
				continue
			}
			at := strings.LastIndexByte(ref, '@')
			if at < 0 || !sha.MatchString(ref[at+1:]) {
				t.Errorf("%s has mutable action ref %q", path, ref)
			}
		}
	}
}

func TestReusableWorkflowContract(t *testing.T) {
	t.Parallel()
	workflow := readRepoFile(t, ".github", "workflows", "reeve.yml")
	for _, want := range []string{
		"workflow_call:",
		"inputs.mode == 'gitops'",
		"inputs.mode == 'drift'",
		"uses: $/.github/actions/reeve",
		"github.event.action == 'created'",
		"contains(github.event.comment.body, '/reeve')",
		"cancel-in-progress:",
		"opentofu-version:",
		"terraform-version:",
		"pulumi_access_token:",
		"reeve_token:",
		"name: Reeve",
		"REEVE_SELF_CHECK_NAMES:",
		"default: reeve / Reeve,reeve / gitops",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("reusable workflow is missing %q", want)
		}
	}
	if strings.Contains(workflow, "secrets: inherit") {
		t.Fatal("reusable workflow must map named secrets")
	}
	if strings.Contains(workflow, "pull-requests: write") {
		t.Fatal("reusable workflow must inherit mode-specific caller permissions")
	}
	implementation := readRepoFile(t, ".github", "actions", "reeve", "action.yml")
	if !strings.Contains(implementation, "persist-credentials: false") {
		t.Fatal("composite action checkout must not persist credentials")
	}
	action := implementation
	for _, want := range []string{"Install Pulumi CLI", "Install OpenTofu CLI", "Install Terraform CLI"} {
		if !strings.Contains(action, want) {
			t.Errorf("composite action is missing %q", want)
		}
		if strings.Index(action, "name: Classify event") > strings.Index(action, "name: "+want) {
			t.Errorf("%s must run after event classification", want)
		}
	}
	for _, want := range []string{"tofu_wrapper: false", "terraform_wrapper: false"} {
		if !strings.Contains(action, want) {
			t.Errorf("composite action is missing %q", want)
		}
	}
}

func TestPublicActionForwardsInputs(t *testing.T) {
	t.Parallel()
	wrapper := readRepoFile(t, "action.yml")
	implementation := readRepoFile(t, ".github", "actions", "reeve", "action.yml")
	if !strings.Contains(wrapper, "uses: $/.github/actions/reeve") {
		t.Fatal("public action must invoke the pinned internal implementation")
	}
	for _, input := range []string{
		"command", "root", "pulumi-version", "opentofu-version", "terraform-version",
		"github-token", "slack-token", "gcp-workload-identity-provider", "gcp-service-account",
		"extra-args", "allowed-associations", "command-prefix", "run-on-approval", "log-level",
	} {
		if !strings.Contains(implementation, "  "+input+":") {
			t.Errorf("internal action is missing input %q", input)
		}
		if !strings.Contains(wrapper, input+": ${{ inputs."+input+" }}") {
			t.Errorf("public action does not forward input %q", input)
		}
	}
}

func TestActionInputsCannotExecuteShellSyntax(t *testing.T) {
	script := extractRunReeveScript(t, readRepoFile(t, ".github", "actions", "reeve", "action.yml"))
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
		"REEVE_ALLOWED_ASSOCIATIONS": "OWNER",
		"REEVE_COMMAND_PREFIXES":     "/reeve",
		"REEVE_DISPATCH_COMMAND":     "lint",
		"REEVE_DISPATCH_AUTO_ARGS":   "[]",
		"REEVE_DISPATCH_UNLOCK_REF":  "",
		"REEVE_DISPATCH_BREAK_GLASS": "false",
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

func TestActionClassifier(t *testing.T) {
	tests := []struct {
		name          string
		eventName     string
		eventJSON     string
		command       string
		runOnApproval string
		wantRun       bool
		wantCommand   string
		wantArgs      []string
		wantUnlockRef string
	}{
		{
			name:      "ordinary comment",
			eventName: "issue_comment",
			eventJSON: `{"action":"created","issue":{"pull_request":{}},"comment":{"body":"looks good","author_association":"OWNER","user":{"type":"User","login":"operator"}}}`,
		},
		{
			name:      "plain issue command",
			eventName: "issue_comment",
			eventJSON: `{"action":"created","issue":{"number":42},"comment":{"body":"/reeve apply","author_association":"OWNER","user":{"type":"User","login":"operator"}}}`,
		},
		{
			name:      "bot command",
			eventName: "issue_comment",
			eventJSON: `{"action":"created","issue":{"pull_request":{}},"comment":{"body":"/reeve apply","author_association":"OWNER","user":{"type":"Bot","login":"reeve[bot]"}}}`,
		},
		{
			name:      "unauthorized command",
			eventName: "issue_comment",
			eventJSON: `{"action":"created","issue":{"pull_request":{}},"comment":{"body":"/reeve apply","author_association":"NONE","user":{"type":"User","login":"stranger"}}}`,
		},
		{
			name:      "unknown command",
			eventName: "issue_comment",
			eventJSON: `{"action":"created","issue":{"pull_request":{}},"comment":{"body":"/reeve deploy","author_association":"OWNER","user":{"type":"User","login":"operator"}}}`,
		},
		{
			name:      "irrelevant pull request action",
			eventName: "pull_request",
			eventJSON: `{"action":"labeled","pull_request":{"number":42}}`,
		},
		{
			name:          "review disabled",
			eventName:     "pull_request_review",
			eventJSON:     `{"action":"submitted","review":{"state":"approved","author_association":"OWNER"}}`,
			runOnApproval: "false",
		},
		{
			name:          "authorized review",
			eventName:     "pull_request_review",
			eventJSON:     `{"action":"submitted","review":{"state":"approved","author_association":"OWNER"}}`,
			runOnApproval: "true",
			wantRun:       true,
			wantCommand:   "approved",
		},
		{
			name:      "edited command",
			eventName: "issue_comment",
			eventJSON: `{"action":"edited","issue":{"pull_request":{}},"comment":{"body":"/reeve apply","author_association":"OWNER","user":{"type":"User","login":"operator"}}}`,
		},
		{
			name:        "authorized apply flags",
			eventName:   "issue_comment",
			eventJSON:   `{"action":"created","issue":{"pull_request":{}},"comment":{"body":"/reeve apply --force --refresh","author_association":"OWNER","user":{"type":"User","login":"operator"}}}`,
			wantRun:     true,
			wantCommand: "apply",
			wantArgs:    []string{"--trigger-source", "comment", "--force", "--refresh"},
		},
		{
			name:        "refresh flags",
			eventName:   "issue_comment",
			eventJSON:   `{"action":"created","issue":{"pull_request":{}},"comment":{"body":"/reeve refresh --dry-run --all","author_association":"OWNER","user":{"type":"User","login":"operator"}}}`,
			wantRun:     true,
			wantCommand: "refresh",
			wantArgs:    []string{"--dry-run", "--all"},
		},
		{
			name:          "scoped unlock",
			eventName:     "issue_comment",
			eventJSON:     `{"action":"created","issue":{"pull_request":{}},"comment":{"body":"/reeve unlock project/prod --force","author_association":"OWNER","user":{"type":"User","login":"operator"}}}`,
			wantRun:       true,
			wantCommand:   "unlock",
			wantArgs:      []string{"--force"},
			wantUnlockRef: "project/prod",
		},
		{
			name:        "scoped explain",
			eventName:   "issue_comment",
			eventJSON:   `{"action":"created","issue":{"pull_request":{}},"comment":{"body":"/reeve explain project/prod","author_association":"OWNER","user":{"type":"User","login":"operator"}}}`,
			wantRun:     true,
			wantCommand: "run explain",
			wantArgs:    []string{"--stack", "project/prod"},
		},
		{
			name:        "break glass reaches cli parser",
			eventName:   "issue_comment",
			eventJSON:   `{"action":"created","issue":{"pull_request":{}},"comment":{"body":"/reeve breakglass \"incident 42\" apply","author_association":"OWNER","user":{"type":"User","login":"operator"}}}`,
			wantRun:     true,
			wantCommand: "apply",
			wantArgs:    []string{"--break-glass"},
		},
		{
			name:      "malformed explain",
			eventName: "issue_comment",
			eventJSON: `{"action":"created","issue":{"pull_request":{}},"comment":{"body":"/reeve explain one two","author_association":"OWNER","user":{"type":"User","login":"operator"}}}`,
		},
		{
			name:        "merged pull request",
			eventName:   "pull_request",
			eventJSON:   `{"action":"closed","pull_request":{"number":42,"merged":true}}`,
			wantRun:     true,
			wantCommand: "apply",
			wantArgs:    []string{"--trigger-source", "merge"},
		},
		{
			name:        "explicit scheduled command",
			eventName:   "schedule",
			eventJSON:   `{}`,
			command:     "drift run",
			wantRun:     true,
			wantCommand: "drift run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dispatch := classifyActionEvent(t, tt.eventName, tt.eventJSON, tt.command, tt.runOnApproval)
			if dispatch.Run != tt.wantRun || dispatch.Command != tt.wantCommand {
				t.Fatalf("dispatch = %+v, want run=%v command=%q", dispatch, tt.wantRun, tt.wantCommand)
			}
			if strings.Join(dispatch.Args, " ") != strings.Join(tt.wantArgs, " ") {
				t.Fatalf("args = %q, want %q", dispatch.Args, tt.wantArgs)
			}
			if dispatch.UnlockRef != tt.wantUnlockRef {
				t.Fatalf("unlock ref = %q, want %q", dispatch.UnlockRef, tt.wantUnlockRef)
			}
		})
	}
}

func TestActionHeavyStepsUseDispatchGuard(t *testing.T) {
	action := readRepoFile(t, ".github", "actions", "reeve", "action.yml")
	for _, name := range []string{
		"Hash reeve source",
		"Restore reeve binary cache",
		"Install cosign for binary verification",
		"Fetch prebuilt binary",
		"Set up Go (from reeve's go.mod)",
		"Build reeve",
		"Add reeve to PATH",
		"Checkout workload",
		"Authenticate to GCP",
		"Install Pulumi CLI",
		"Install OpenTofu CLI",
		"Install Terraform CLI",
		"Run reeve",
	} {
		marker := "- name: " + name
		start := strings.Index(action, marker)
		if start < 0 {
			t.Fatalf("missing action step %q", name)
		}
		rest := action[start+len(marker):]
		end := strings.Index(rest, "\n    - name:")
		if end >= 0 {
			rest = rest[:end]
		}
		if !strings.Contains(rest, "steps.reeve-dispatch.outputs.run == 'true'") {
			t.Errorf("action step %q is not guarded by early dispatch", name)
		}
	}
}

func TestActionPreviewRouting(t *testing.T) {
	tests := []struct {
		name      string
		eventName string
		eventJSON string
		want      []string
	}{
		{
			name:      "plan comment marks explicit request",
			eventName: "issue_comment",
			eventJSON: `{
				"action":"created",
				"issue":{"number":42,"pull_request":{}},
				"comment":{"body":"/reeve plan","author_association":"OWNER","user":{"type":"User","login":"operator"}}
			}`,
			want: []string{"run", "preview", "--pr", "42", "--run-url", "https://github.com/org/repo/actions/runs/123", "--plan-requested"},
		},
		{
			name:      "push preview omits explicit request",
			eventName: "pull_request",
			eventJSON: `{
				"action":"synchronize",
				"pull_request":{"number":42}
			}`,
			want: []string{"run", "preview", "--pr", "42", "--run-url", "https://github.com/org/repo/actions/runs/123"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runActionPreview(t, tt.eventName, tt.eventJSON)
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Fatalf("preview args = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanRequestedFlagIsPreviewOnly(t *testing.T) {
	t.Parallel()

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
	dispatch := classifyActionEvent(t, eventName, eventJSON, "", "false")
	if !dispatch.Run {
		t.Fatal("preview event was skipped by classifier")
	}
	script := extractRunReeveScript(t, readRepoFile(t, ".github", "actions", "reeve", "action.yml"))
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
		"REEVE_ALLOWED_ASSOCIATIONS": "OWNER",
		"REEVE_COMMAND_PREFIXES":     "/reeve",
		"REEVE_DISPATCH_COMMAND":     dispatch.Command,
		"REEVE_DISPATCH_AUTO_ARGS":   mustJSON(t, dispatch.Args),
		"REEVE_DISPATCH_UNLOCK_REF":  dispatch.UnlockRef,
		"REEVE_DISPATCH_BREAK_GLASS": "false",
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

type actionDispatch struct {
	Run       bool
	Command   string
	Args      []string
	UnlockRef string
}

func classifyActionEvent(t *testing.T, eventName, eventJSON, command, runOnApproval string) actionDispatch {
	t.Helper()
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	outputPath := filepath.Join(dir, "output")
	if err := os.WriteFile(eventPath, []byte(eventJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if runOnApproval == "" {
		runOnApproval = "false"
	}

	script := repoPath(t, ".github", "scripts", "classify-event.sh")
	// #nosec G204 -- the executable is a repository-owned script and fixtures travel through environment variables.
	cmd := exec.Command(script)
	for key, value := range map[string]string{
		"GITHUB_OUTPUT":              outputPath,
		"GITHUB_EVENT_NAME":          eventName,
		"GITHUB_EVENT_PATH":          eventPath,
		"REEVE_INPUT_COMMAND":        command,
		"REEVE_RUN_ON_APPROVAL":      runOnApproval,
		"REEVE_ALLOWED_ASSOCIATIONS": "OWNER,MEMBER,COLLABORATOR",
		"REEVE_COMMAND_PREFIXES":     "/reeve",
	} {
		t.Setenv(key, value)
	}
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("classifier failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	var args []string
	if err := json.Unmarshal([]byte(values["auto_args"]), &args); err != nil {
		t.Fatalf("parse classifier args %q: %v", values["auto_args"], err)
	}
	return actionDispatch{
		Run:       values["run"] == "true",
		Command:   values["command"],
		Args:      args,
		UnlockRef: values["unlock_ref"],
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
