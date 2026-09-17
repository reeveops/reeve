package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"

	"github.com/reeveops/reeve/internal/auth"
	authfac "github.com/reeveops/reeve/internal/auth/factory"
	"github.com/reeveops/reeve/internal/config"
)

func documentationRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate documentation test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
}

func documentationRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Check the maintained docs, not historical proposal snapshots or rendered goldens.
func documentationFiles(t *testing.T) []string {
	t.Helper()
	root := documentationRoot(t)
	files := []string{filepath.Join(root, "README.md"), filepath.Join(root, "CONTRIBUTING.md"), filepath.Join(root, "SECURITY.md")}
	for _, dir := range []string{"docs", "examples", "openspec/specs"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && (entry.Name() == "node_modules" || entry.Name() == ".terraform") {
				return filepath.SkipDir
			}
			if !entry.IsDir() && strings.HasSuffix(path, ".md") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

var documentationLink = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

func documentationAnchors(body string) map[string]bool {
	anchors := map[string]bool{}
	counts := map[string]int{}
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced || !strings.HasPrefix(line, "#") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
		heading = documentationLink.ReplaceAllStringFunc(heading, func(link string) string {
			return link[1:strings.Index(link, "]")]
		})
		var slug strings.Builder
		for _, r := range strings.ToLower(heading) {
			switch {
			case unicode.IsLetter(r), unicode.IsNumber(r), r == '_', r == '-':
				slug.WriteRune(r)
			case r == ' ':
				slug.WriteByte('-')
			}
		}
		base := slug.String()
		anchor := base
		if counts[base] > 0 {
			anchor = fmt.Sprintf("%s-%d", base, counts[base])
		}
		counts[base]++
		anchors[anchor] = true
	}
	return anchors
}

func TestDocumentationLinks(t *testing.T) {
	for _, path := range documentationFiles(t) {
		fenced := false
		for n, line := range strings.Split(documentationRead(t, path), "\n") {
			if strings.HasPrefix(line, "```") {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			for _, match := range documentationLink.FindAllStringSubmatch(line, -1) {
				target := match[1]
				if strings.Contains(target, ":") {
					continue // Remote URLs are not fetched in unit tests.
				}
				rel, anchor, _ := strings.Cut(target, "#")
				resolved := path
				if rel != "" {
					resolved = filepath.Join(filepath.Dir(path), rel)
				}
				if _, err := os.Stat(resolved); err != nil {
					t.Errorf("%s:%d: missing target %s", path, n+1, target)
					continue
				}
				if anchor != "" && strings.HasSuffix(resolved, ".md") && !documentationAnchors(documentationRead(t, resolved))[anchor] {
					t.Errorf("%s:%d: missing heading %s", path, n+1, target)
				}
			}
		}
	}
}

func TestDocumentationExamples(t *testing.T) {
	root := documentationRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "examples"))
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{"api/dev", "api/prod", "payments/prod", "auth/prod", "random-name/dev", "random-name/prod"}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			cfg, err := config.Load(filepath.Join(root, "examples", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			for pattern := range cfg.Shared.Approvals.Stacks {
				matched := false
				for _, ref := range refs {
					ok, matchErr := doublestar.Match(pattern, ref)
					if matchErr != nil {
						t.Fatal(matchErr)
					}
					matched = matched || ok
				}
				if !matched {
					t.Errorf("approval selector %q matches none of the representative project/stack references", pattern)
				}
			}
			if cfg.Auth == nil {
				return
			}
			if err := authfac.ValidateLint(cfg.Auth, refs); err != nil {
				t.Fatal(err)
			}
			var bindings []auth.Binding
			decls := map[string]auth.ProviderDecl{}
			for name, provider := range cfg.Auth.Providers {
				decls[name] = auth.ProviderDecl{Name: name, Type: provider.Type}
			}
			for _, binding := range cfg.Auth.Bindings {
				bindings = append(bindings, auth.Binding{StackPattern: binding.Match.Stack, Mode: auth.Mode(binding.Match.Mode), Providers: binding.Providers, Override: binding.Override, Local: binding.Local})
			}
			wantDrift := map[string][]string{
				"aws-oidc":    {"aws-prod-readonly"},
				"gcp-wif":     {"gcp-prod-readonly"},
				"multi-cloud": {"aws-prod-readonly", "gcp-prod-readonly"},
			}[entry.Name()]
			got := auth.ResolveWithDecls(bindings, decls, "payments/prod", auth.ModeDrift)
			slices.Sort(got)
			slices.Sort(wantDrift)
			if !slices.Equal(got, wantDrift) {
				t.Errorf("payments/prod drift providers = %v, want %v", got, wantDrift)
			}
			if entry.Name() == "multi-cloud" {
				got = auth.ResolveWithDecls(bindings, decls, "payments/prod", auth.ModeApply)
				if !slices.Contains(got, "aws-payments-strict") || slices.Contains(got, "aws-prod") {
					t.Errorf("payments override did not replace the AWS role: %v", got)
				}
			}
		})
	}
}

// Validate snippet syntax and reusable-workflow inputs against the actual contract.
func TestDocumentationWorkflows(t *testing.T) {
	root := documentationRoot(t)
	var contract struct {
		On struct {
			Call struct {
				Inputs  map[string]any `yaml:"inputs"`
				Secrets map[string]any `yaml:"secrets"`
			} `yaml:"workflow_call"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal([]byte(documentationRead(t, filepath.Join(root, ".github/workflows/reeve.yml"))), &contract); err != nil {
		t.Fatal(err)
	}
	check := func(label, body string) {
		t.Helper()
		var document yaml.Node
		if err := yaml.Unmarshal([]byte(body), &document); err != nil {
			t.Errorf("%s: invalid YAML: %v", label, err)
			return
		}
		if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
			return // Configuration fragments may be lists rather than workflows.
		}
		var node struct {
			Jobs map[string]struct {
				Uses    string         `yaml:"uses"`
				With    map[string]any `yaml:"with"`
				Secrets map[string]any `yaml:"secrets"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(body), &node); err != nil {
			t.Errorf("%s: invalid YAML: %v", label, err)
			return
		}
		for name, job := range node.Jobs {
			if !strings.Contains(job.Uses, "reeveops/reeve/.github/workflows/reeve.yml@") {
				continue
			}
			for input := range job.With {
				if _, ok := contract.On.Call.Inputs[input]; !ok {
					t.Errorf("%s: job %s uses unknown workflow input %s", label, name, input)
				}
			}
			for secret := range job.Secrets {
				if _, ok := contract.On.Call.Secrets[secret]; !ok {
					t.Errorf("%s: job %s uses unknown workflow secret %s", label, name, secret)
				}
			}
		}
	}
	for _, path := range documentationFiles(t) {
		blocks := strings.Split(documentationRead(t, path), "```")
		for i := 1; i < len(blocks); i += 2 {
			if strings.HasPrefix(blocks[i], "yaml\n") {
				check(fmt.Sprintf("%s block %d", path, i), strings.TrimPrefix(blocks[i], "yaml\n"))
			}
		}
	}
	paths, err := filepath.Glob(filepath.Join(root, "examples/*/.github/workflows/*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		check(path, documentationRead(t, path))
	}
}
