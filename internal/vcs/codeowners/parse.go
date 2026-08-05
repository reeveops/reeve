// Package codeowners holds a format-agnostic CODEOWNERS parser + resolver
// for GitHub-flavored syntax. Adapters in internal/vcs/<platform> fetch
// the file; this package parses it and resolves owners for a set of
// changed paths.
package codeowners

import (
	"bufio"
	"io"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Rule is one line from CODEOWNERS. A rule with no owners is meaningful:
// when it is the last match for a path, the path is un-owned (GitHub's
// carve-out idiom).
type Rule struct {
	Pattern string
	Owners  []string // handles as written, with leading @
}

// Parse returns rules in file order. Comment lines, inline comments
// (an unescaped `#` ends the entry), and blank lines are skipped.
// Pattern-only lines (no owners) are kept: GitHub uses them to un-own paths
// matched by earlier rules.
func Parse(r io.Reader) []Rule {
	var rules []Rule
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := splitLine(sc.Text())
		if len(fields) == 0 {
			continue
		}
		rules = append(rules, Rule{
			Pattern: fields[0],
			Owners:  fields[1:],
		})
	}
	return rules
}

// splitLine tokenizes one CODEOWNERS line per GitHub's format:
//   - tokens are separated by runs of spaces/tabs (leading/trailing
//     whitespace ignored);
//   - a backslash escapes a following space, tab, or `#`, so paths with
//     spaces (`docs/getting\ started.md @team`) stay one token;
//   - any other backslash sequence (e.g. `\*` escaping a glob
//     metacharacter) passes through unchanged for the glob matcher;
//   - an unescaped `#` starts a comment that runs to end of line, whether
//     the line starts with it or it follows an entry.
//
// An empty result means the line holds no entry (blank or comment-only).
func splitLine(line string) []string {
	var fields []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			fields = append(fields, cur.String())
			cur.Reset()
		}
	}
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; {
		case r == '\\' && i+1 < len(rs) && (rs[i+1] == ' ' || rs[i+1] == '\t' || rs[i+1] == '#'):
			cur.WriteRune(rs[i+1])
			i++
		case r == '#': // inline comment: entry ends here
			flush()
			return fields
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return fields
}

// Resolve returns path → owners for every changed file that is owned.
// GitHub semantics: the LAST matching rule wins exclusively - earlier
// matches contribute nothing. A last match with no owners un-owns the
// path, so it is omitted from the result.
func Resolve(rules []Rule, changedPaths []string) map[string][]string {
	out := map[string][]string{}
	for _, p := range changedPaths {
		for i := len(rules) - 1; i >= 0; i-- {
			if !match(rules[i].Pattern, p) {
				continue
			}
			if len(rules[i].Owners) > 0 {
				out[p] = rules[i].Owners
			}
			break
		}
	}
	return out
}

// match implements the gitignore-style glob CODEOWNERS uses:
//   - a pattern containing a non-trailing "/" is anchored to the repo root;
//     otherwise it matches at any depth ("docs/" matches "x/docs/y").
//   - a trailing "/" matches every descendant of a matching directory.
//   - a pattern without trailing "/" whose last segment has no wildcard also
//     owns descendants ("/docs" covers "docs/a/b"), while "docs/*" covers
//     direct children only - both per GitHub's documented examples.
//   - "!", and "[" / "]" ranges are unsupported by GitHub CODEOWNERS; such
//     patterns never match anything (GitHub's behavior).
func match(pattern, p string) bool {
	p = strings.TrimPrefix(p, "/")
	if strings.HasPrefix(pattern, "!") || strings.ContainsAny(pattern, "[]") {
		return false
	}

	dirOnly := strings.HasSuffix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")
	anchored := strings.Contains(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "/")
	if pattern == "" {
		return false
	}
	if !anchored {
		pattern = "**/" + pattern
	}

	if dirOnly {
		return globMatch(pattern+"/**", p)
	}
	if globMatch(pattern, p) {
		return true
	}
	// A non-wildcard final segment may name a directory; a directory owns
	// its contents. Wildcard tails ("docs/*") stay direct-children-only.
	if last := pattern[strings.LastIndex(pattern, "/")+1:]; !strings.ContainsAny(last, "*?") {
		return globMatch(pattern+"/**", p)
	}
	return false
}

func globMatch(pattern, s string) bool {
	ok, err := doublestar.Match(pattern, s)
	return err == nil && ok
}
