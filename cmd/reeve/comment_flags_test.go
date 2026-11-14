package main

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/render"
)

func flagCmd(args ...string) (*cobra.Command, error) {
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	addCommentFlags(cmd)
	return cmd, cmd.Flags().Parse(args)
}

// An untyped flag must not overwrite config with its own default. cobra reports
// a default as a value, so without a Changed check every run would silently
// reset the operator's settings.
func TestCommentFlagsLeaveConfigAloneWhenUnset(t *testing.T) {
	cmd, err := flagCmd()
	if err != nil {
		t.Fatal(err)
	}
	shared := &schemas.Shared{Comments: schemas.CommentsConfig{
		Sort: "alphabetical", StackView: "changed", Style: render.StyleSection,
		ShowGates: true, CollapseThreshold: 25,
		Overflow: schemas.OverflowConfig{Mode: render.OverflowContinue, Split: render.SplitGroup, MaxParts: 4},
	}}
	before := shared.Comments
	if err := applyCommentFlags(cmd, shared); err != nil {
		t.Fatal(err)
	}
	if shared.Comments != before {
		t.Fatalf("unset flags changed config:\n got %+v\nwant %+v", shared.Comments, before)
	}
}

func TestCommentFlagsOverrideConfig(t *testing.T) {
	cmd, err := flagCmd(
		"--comment-sort", "alphabetical",
		"--comment-stack-view", "changed",
		"--comment-style", "section",
		"--comment-show-gates",
		"--comment-collapse-threshold", "7",
		"--comment-overflow", "continue",
		"--comment-overflow-split", "group",
		"--comment-overflow-max-parts", "3",
	)
	if err != nil {
		t.Fatal(err)
	}
	shared := &schemas.Shared{}
	if err := applyCommentFlags(cmd, shared); err != nil {
		t.Fatal(err)
	}
	c := shared.Comments
	if c.Sort != "alphabetical" || c.StackView != "changed" || c.Style != render.StyleSection {
		t.Errorf("board flags not applied: %+v", c)
	}
	if !c.ShowGates || c.CollapseThreshold != 7 {
		t.Errorf("gate/collapse flags not applied: %+v", c)
	}
	if c.Overflow.Mode != render.OverflowContinue || c.Overflow.Split != render.SplitGroup || c.Overflow.MaxParts != 3 {
		t.Errorf("overflow flags not applied: %+v", c.Overflow)
	}
}

// A typo must fail loudly. Silently falling back to the default looks exactly
// like the flag being ignored.
func TestCommentFlagsRejectUnknownValues(t *testing.T) {
	cases := []struct{ flag, value string }{
		{"comment-sort", "by_vibes"},
		{"comment-stack-view", "some"},
		{"comment-style", "sections"},
		{"comment-overflow", "spill"},
		{"comment-overflow-split", "even"},
	}
	for _, c := range cases {
		t.Run(c.flag, func(t *testing.T) {
			cmd, err := flagCmd("--"+c.flag, c.value)
			if err != nil {
				t.Fatal(err)
			}
			err = applyCommentFlags(cmd, &schemas.Shared{})
			if err == nil {
				t.Fatalf("--%s=%s must be rejected", c.flag, c.value)
			}
			// The message has to name the flag and what was allowed, or the
			// operator is left guessing.
			if !strings.Contains(err.Error(), c.flag) {
				t.Errorf("error does not name the flag: %v", err)
			}
			if !strings.Contains(err.Error(), "not one of") {
				t.Errorf("error does not list the allowed values: %v", err)
			}
		})
	}
}

func TestCommentFlagsRejectOutOfRangeNumbers(t *testing.T) {
	for _, c := range []struct{ flag, value string }{
		{"comment-overflow-max-parts", "0"},
		{"comment-overflow-max-parts", "-1"},
		{"comment-collapse-threshold", "-5"},
	} {
		t.Run(c.flag+"="+c.value, func(t *testing.T) {
			cmd, err := flagCmd("--"+c.flag, c.value)
			if err != nil {
				t.Fatal(err)
			}
			if err := applyCommentFlags(cmd, &schemas.Shared{}); err == nil {
				t.Fatalf("--%s=%s must be rejected", c.flag, c.value)
			}
		})
	}
}

// A flag that cannot be honored must say so rather than be quietly dropped.
func TestCommentFlagsWithoutSharedConfigFail(t *testing.T) {
	cmd, err := flagCmd("--comment-overflow", "continue")
	if err != nil {
		t.Fatal(err)
	}
	err = applyCommentFlags(cmd, nil)
	if err == nil {
		t.Fatal("a flag with no config to override must fail")
	}
	if !strings.Contains(err.Error(), "comment-overflow") {
		t.Errorf("error must name the flag: %v", err)
	}
}

// With no config and no flags there is nothing to reconcile.
func TestCommentFlagsWithoutSharedConfigOrFlagsIsFine(t *testing.T) {
	cmd, err := flagCmd()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyCommentFlags(cmd, nil); err != nil {
		t.Fatalf("no config and no flags must be a no-op: %v", err)
	}
}

// Every registered flag must be listed in commentFlagNames, or the no-config
// path accepts it silently.
func TestCommentFlagNamesCoversEveryFlag(t *testing.T) {
	cmd, err := flagCmd()
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, n := range commentFlagNames {
		listed[n] = true
	}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if !listed[f.Name] {
			t.Errorf("flag --%s is registered but missing from commentFlagNames", f.Name)
		}
	})
}

// Every command that renders a PR board must carry the flags, or a setting
// works on one command and is silently unavailable on another.
//
// pr-help and ready are excluded deliberately: they render the help and ready
// comments, which carry no stack table and honor none of the comments settings.
func TestBoardCommandsCarryCommentFlags(t *testing.T) {
	boards := map[string]bool{"preview": true, "apply": true, "refresh": true, "render": true}

	var check func(prefix string, c *cobra.Command)
	checked := 0
	check = func(prefix string, c *cobra.Command) {
		name := strings.TrimSpace(prefix + " " + c.Name())
		if boards[c.Name()] {
			checked++
			for _, f := range commentFlagNames {
				if c.Flags().Lookup(f) == nil {
					t.Errorf("%s is missing --%s", name, f)
				}
			}
		}
		for _, sub := range c.Commands() {
			check(name, sub)
		}
	}
	for _, c := range []*cobra.Command{newRunCmd(), newApplyCmd(), newRefreshCmd()} {
		check("", c)
	}
	if checked == 0 {
		t.Fatal("no board command was checked; the command names must have changed")
	}
}

// TestCommentAuthorReachesTheClientThroughTheRealWiring pins the full
// author seam: a config comments.author reaches the env the github client
// reads, a --comment-author flag overrides it, and the flag wins when both
// are set. The client-side consumption of REEVE_COMMENT_AUTHOR is covered
// by internal/vcs/github tests; this pins the command-side wiring including
// the precedence.
func TestCommentAuthorReachesTheClientThroughTheRealWiring(t *testing.T) {
	prev, hadPrev := os.LookupEnv("REEVE_COMMENT_AUTHOR")
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv("REEVE_COMMENT_AUTHOR", prev)
		} else {
			_ = os.Unsetenv("REEVE_COMMENT_AUTHOR")
		}
	})

	// Config only: comments.author reaches the client seam.
	cmd, err := flagCmd()
	if err != nil {
		t.Fatal(err)
	}
	shared := &schemas.Shared{Comments: schemas.CommentsConfig{Author: "cfg-author[bot]"}}
	if err := applyCommentFlags(cmd, shared); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("REEVE_COMMENT_AUTHOR"); got != "cfg-author[bot]" {
		t.Fatalf("the config value must reach the client seam, got %q", got)
	}

	// Flag and config together: the flag wins.
	cmd, err = flagCmd("--comment-author", "flag-author[bot]")
	if err != nil {
		t.Fatal(err)
	}
	shared = &schemas.Shared{Comments: schemas.CommentsConfig{Author: "cfg-author[bot]"}}
	if err := applyCommentFlags(cmd, shared); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("REEVE_COMMENT_AUTHOR"); got != "flag-author[bot]" {
		t.Fatalf("the flag must win over the config key, got %q", got)
	}
	if shared.Comments.Author != "flag-author[bot]" {
		t.Fatalf("the flag must also land in the live config: %+v", shared.Comments)
	}
}
