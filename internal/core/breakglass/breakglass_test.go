package breakglass

import (
	"errors"
	"strings"
	"testing"
)

func cfgWith(mutate func(*Config)) Config {
	c := Config{Configured: true, OverrideFreeze: true}
	if mutate != nil {
		mutate(&c)
	}
	return c
}

func TestAuthorize(t *testing.T) {
	teams := map[string][]string{
		"acme/sre": {"carol", "dave"},
	}
	owned := map[string][]string{
		"infra/prod/main.ts": {"@alice", "@acme/sre"},
		"docs/x.md":          {"@bob"},
	}

	cases := []struct {
		name       string
		cfg        Config
		in         Inputs
		authorized bool
		source     string
		wantErr    string
	}{
		{
			name:    "unconfigured errors politely",
			cfg:     Config{},
			in:      Inputs{Actor: "alice"},
			wantErr: "not configured",
		},
		{
			name:    "configured but no sources",
			cfg:     cfgWith(nil),
			in:      Inputs{Actor: "alice"},
			wantErr: "no sources",
		},
		{
			name:       "internal_list direct login",
			cfg:        cfgWith(func(c *Config) { c.InternalList = []string{"alice"} }),
			in:         Inputs{Actor: "alice"},
			authorized: true,
			source:     SourceInternalList,
		},
		{
			name:       "internal_list is case-insensitive and @-tolerant",
			cfg:        cfgWith(func(c *Config) { c.InternalList = []string{"@Alice"} }),
			in:         Inputs{Actor: "alice"},
			authorized: true,
			source:     SourceInternalList,
		},
		{
			name:       "internal_list team slug via expansion",
			cfg:        cfgWith(func(c *Config) { c.InternalList = []string{"acme/sre"} }),
			in:         Inputs{Actor: "dave", TeamMembers: teams},
			authorized: true,
			source:     SourceInternalList,
		},
		{
			name:       "internal_list unknown team fails closed",
			cfg:        cfgWith(func(c *Config) { c.InternalList = []string{"acme/ghosts"} }),
			in:         Inputs{Actor: "dave", TeamMembers: teams},
			authorized: false,
		},
		{
			name:       "codeowners direct owner of changed path",
			cfg:        cfgWith(func(c *Config) { c.Codeowners = true }),
			in:         Inputs{Actor: "alice", OwnedPaths: owned},
			authorized: true,
			source:     SourceCodeowners,
		},
		{
			name:       "codeowners via team ownership",
			cfg:        cfgWith(func(c *Config) { c.Codeowners = true }),
			in:         Inputs{Actor: "carol", OwnedPaths: owned, TeamMembers: teams},
			authorized: true,
			source:     SourceCodeowners,
		},
		{
			name:       "codeowners non-owner denied",
			cfg:        cfgWith(func(c *Config) { c.Codeowners = true }),
			in:         Inputs{Actor: "mallory", OwnedPaths: owned, TeamMembers: teams},
			authorized: false,
		},
		{
			name:       "codeowners with no owned paths denied",
			cfg:        cfgWith(func(c *Config) { c.Codeowners = true }),
			in:         Inputs{Actor: "alice"},
			authorized: false,
		},
		{
			name:       "anyone grants anyone",
			cfg:        cfgWith(func(c *Config) { c.Anyone = true }),
			in:         Inputs{Actor: "mallory"},
			authorized: true,
			source:     SourceAnyone,
		},
		{
			name: "union: narrower source wins the audit label",
			cfg: cfgWith(func(c *Config) {
				c.InternalList = []string{"alice"}
				c.Anyone = true
			}),
			in:         Inputs{Actor: "alice"},
			authorized: true,
			source:     SourceInternalList,
		},
		{
			name: "union: fallthrough to anyone",
			cfg: cfgWith(func(c *Config) {
				c.InternalList = []string{"alice"}
				c.Anyone = true
			}),
			in:         Inputs{Actor: "mallory"},
			authorized: true,
			source:     SourceAnyone,
		},
		{
			name: "union: deny when no source matches",
			cfg: cfgWith(func(c *Config) {
				c.InternalList = []string{"alice"}
				c.Codeowners = true
			}),
			in:         Inputs{Actor: "mallory", OwnedPaths: owned},
			authorized: false,
		},
		{
			name:    "empty actor denied",
			cfg:     cfgWith(func(c *Config) { c.Anyone = true }),
			in:      Inputs{Actor: "  "},
			wantErr: "",
		},
		{
			name:    "vcs_bypass is a hard not-yet-supported error",
			cfg:     cfgWith(func(c *Config) { c.VCSBypass = true; c.InternalList = []string{"alice"} }),
			in:      Inputs{Actor: "alice"},
			wantErr: "vcs_bypass is not yet supported",
		},
		{
			name:    "groups are a hard phase-2 error even when another source matches",
			cfg:     cfgWith(func(c *Config) { c.Groups = []string{"group:aws_iam:oncall"}; c.Anyone = true }),
			in:      Inputs{Actor: "alice"},
			wantErr: "phase 2",
		},
		{
			name:    "malformed group reference gets its own error",
			cfg:     cfgWith(func(c *Config) { c.Groups = []string{"oncall"} }),
			in:      Inputs{Actor: "alice"},
			wantErr: "malformed group reference",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Authorize(tc.cfg, tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d.Authorized != tc.authorized {
				t.Fatalf("authorized = %v, want %v (trace: %v)", d.Authorized, tc.authorized, d.Trace)
			}
			if tc.authorized && d.Source != tc.source {
				t.Fatalf("source = %q, want %q", d.Source, tc.source)
			}
			if len(d.Trace) == 0 {
				t.Fatal("decision carries no trace")
			}
		})
	}
}

func TestAuthorizeUnconfiguredIsErrNotConfigured(t *testing.T) {
	_, err := Authorize(Config{}, Inputs{Actor: "a"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

func TestParseCommand(t *testing.T) {
	valid := []struct {
		line          string
		justification string
		force         bool
	}{
		{`/reeve breakglass "prod is down" apply`, "prod is down", false},
		{`/reeve breakglass "prod is down" apply --force`, "prod is down", true},
		{`/reeve breakglass "prod is down" apply force`, "prod is down", true},
		{`  /reeve   breakglass   " padded justification "   apply  `, "padded justification", false},
		{"/reeve breakglass “mobile quotes” apply", "mobile quotes", false},
		{"/reeve breakglass \"first line\" apply\nsecond line is ignored", "first line", false},
	}
	for _, tc := range valid {
		got, err := ParseCommand(tc.line)
		if err != nil {
			t.Fatalf("ParseCommand(%q): %v", tc.line, err)
		}
		if got.Justification != tc.justification || got.Force != tc.force {
			t.Fatalf("ParseCommand(%q) = %+v, want justification %q force %v", tc.line, got, tc.justification, tc.force)
		}
	}

	malformed := []struct {
		line    string
		wantErr string
	}{
		{``, "not a /reeve command"},
		{`/reeve apply`, "expected `breakglass`"},
		{`/reeve breakglass apply`, "double-quoted"},
		{`/reeve breakglass prod is down apply`, "double-quoted"},
		{`/reeve breakglass "" apply`, "must not be empty"},
		{`/reeve breakglass "   " apply`, "must not be empty"},
		{`/reeve breakglass "no closing quote apply`, "closing quote"},
		{`/reeve breakglass "why"`, "missing verb"},
		{`/reeve breakglass "why" preview`, "unsupported break-glass verb"},
		{`/reeve breakglass "why" apply now please`, "unexpected trailing input"},
		{`/reevebreakglass "why" apply`, "not a /reeve command"},
	}
	for _, tc := range malformed {
		_, err := ParseCommand(tc.line)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("ParseCommand(%q): want error containing %q, got %v", tc.line, tc.wantErr, err)
		}
		if !strings.Contains(err.Error(), Usage) {
			t.Fatalf("ParseCommand(%q): error should echo usage, got %v", tc.line, err)
		}
	}
}

func TestMalformedComment(t *testing.T) {
	_, err := ParseCommand(`/reeve breakglass "" apply`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	body := MalformedComment(err)
	for _, want := range []string{"⛔", "not run", Usage, err.Error()} {
		if !strings.Contains(body, want) {
			t.Fatalf("MalformedComment missing %q:\n%s", want, body)
		}
	}
}

func TestAuthorizingPathsTouched(t *testing.T) {
	changed := []string{
		"infra/main.ts",
		".reeve/shared.yaml",
		".reeve/notifications.yml",
		".reeve/README.md",
		".github/CODEOWNERS",
		"CODEOWNERS",
		"docs/CODEOWNERS",
		"docs/CODEOWNERS.md",
		"sub/.reeve/shared.yaml",
	}
	got := AuthorizingPathsTouched(changed)
	want := []string{".reeve/shared.yaml", ".reeve/notifications.yml", ".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if AuthorizingPathsTouched([]string{"app/main.go"}) != nil {
		t.Fatal("unrelated changes must not flag")
	}
}

func TestAuthorize_RejectSelfAuthorization(t *testing.T) {
	// A source that would otherwise grant (internal_list) must be denied
	// when reject_self_authorization is set and the PR touched an
	// authorizing file.
	cfg := Config{
		Configured:              true,
		InternalList:            []string{"alice"},
		RejectSelfAuthorization: true,
	}
	touched := Inputs{
		Actor:                   "alice",
		AuthorizingPathsTouched: []string{".reeve/shared.yaml"},
	}
	d, err := Authorize(cfg, touched)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if d.Authorized {
		t.Fatalf("self-authorization must be denied when reject_self_authorization is set: %+v", d)
	}
	if len(d.Trace) == 0 || !strings.Contains(strings.Join(d.Trace, " "), "reject_self_authorization") {
		t.Fatalf("denial trace should name reject_self_authorization: %v", d.Trace)
	}

	// Same actor, same config, but no authorizing file touched -> granted.
	clean := Inputs{Actor: "alice"}
	d, err = Authorize(cfg, clean)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !d.Authorized || d.Source != SourceInternalList {
		t.Fatalf("clean PR should be authorized via internal_list: %+v", d)
	}
}

func TestAuthorize_SelfAuthorizationAllowedByDefault(t *testing.T) {
	// Default (RejectSelfAuthorization false): self-add is allowed by design
	// and only audited by the caller.
	cfg := Config{Configured: true, InternalList: []string{"alice"}}
	in := Inputs{Actor: "alice", AuthorizingPathsTouched: []string{".reeve/shared.yaml"}}
	d, err := Authorize(cfg, in)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !d.Authorized {
		t.Fatalf("default behavior must still authorize self-add (flag-and-audit): %+v", d)
	}
}
