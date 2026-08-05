package pulumi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/FynxLabs/reeve/internal/core/summary"
)

// previewJSON is the subset of `pulumi preview --json` output we consume.
// Pulumi emits a top-level object with "steps" (plan steps) and
// "changeSummary" (totals by op). We use changeSummary when present and
// fall back to counting steps.
type previewJSON struct {
	ChangeSummary map[string]int `json:"changeSummary"`
	Steps         []previewStep  `json:"steps"`
	Diagnostics   []diag         `json:"diagnostics"`
}

type previewStep struct {
	Op       string    `json:"op"`
	URN      string    `json:"urn"`
	Type     string    `json:"type"`
	Provider string    `json:"provider"`
	OldState stepState `json:"oldState"`
	NewState stepState `json:"newState"`
	// Keys lists the input properties that force a replacement.
	Keys []string `json:"keys"`
	// DetailedDiff maps property paths ("config.rules[3].expression") to
	// the kind of change. Pulumi emits it for updates/replaces; null for
	// creates and deletes.
	DetailedDiff map[string]propertyDiff `json:"detailedDiff"`
}

// stepState is the subset of a step's old/new resource state we render.
type stepState struct {
	Type   string         `json:"type"`
	Inputs map[string]any `json:"inputs"`
}

type propertyDiff struct {
	Kind string `json:"kind"` // add | delete | update | *-replace
}

type diag struct {
	Severity string `json:"severity"` // "error" | "warning" | "info"
	URN      string `json:"urn"`
	Message  string `json:"message"`
}

// parsePreview converts a `pulumi preview --json` stdout blob into counts
// and a short summary. Errors from diagnostics float into the returned
// error string (caller decides whether that's fatal).
func parsePreview(stdout []byte) (summary.Counts, string, string, error) {
	var p previewJSON
	if err := json.Unmarshal(stdout, &p); err != nil {
		return summary.Counts{}, "", "", fmt.Errorf("parse pulumi preview json: %w", err)
	}

	counts := countsFromSummary(p.ChangeSummary)
	if counts.Total() == 0 && len(p.Steps) > 0 {
		counts = countsFromSteps(p.Steps)
	}

	short := shortSummary(p.Steps, 10)
	var diagMsg string
	for _, d := range p.Diagnostics {
		if d.Severity == "error" {
			if diagMsg != "" {
				diagMsg += "\n"
			}
			diagMsg += d.Message
		}
	}
	return counts, short, diagMsg, nil
}

func countsFromSummary(cs map[string]int) summary.Counts {
	var c summary.Counts
	// Pulumi uses "create", "update", "delete", "replace", "same", "read",
	// "import", "discard", "create-replacement", "delete-replaced", etc.
	c.Add += cs["create"] + cs["import"]
	c.Change += cs["update"]
	c.Delete += cs["delete"]
	c.Replace += cs["replace"] + cs["create-replacement"]
	return c
}

func countsFromSteps(steps []previewStep) summary.Counts {
	var c summary.Counts
	for _, s := range steps {
		switch s.Op {
		case "create", "import":
			c.Add++
		case "update":
			c.Change++
		case "delete":
			c.Delete++
		case "replace", "create-replacement":
			c.Replace++
		}
	}
	return c
}

// shortSummary renders one block per changed resource: a header line with
// the op symbol, resource name and type, then indented property lines
// showing what actually changes. Lines keep +/-/~ prefixes so GitHub's
// ```diff fence colors them.
func shortSummary(steps []previewStep, limit int) string {
	if len(steps) == 0 {
		return ""
	}
	const propLimit = 10
	var b strings.Builder
	shown, changed := 0, 0
	for _, s := range steps {
		if opPrefix(s.Op) != "" {
			changed++
		}
	}
	for _, s := range steps {
		prefix := opPrefix(s.Op)
		if prefix == "" {
			continue
		}
		if shown >= limit {
			fmt.Fprintf(&b, "...and %d more resource(s)\n", changed-shown)
			break
		}
		fmt.Fprintf(&b, "%s %s  (%s)\n", prefix, displayName(s.URN), shortType(s))
		for _, line := range propertyLines(s, propLimit) {
			b.WriteString(line)
			b.WriteString("\n")
		}
		shown++
	}
	return strings.TrimRight(b.String(), "\n")
}

// propertyLines renders the per-property changes for one step, capped at
// limit lines (plus a trailing "...and N more").
func propertyLines(s previewStep, limit int) []string {
	var out []string
	add := func(prefix, text string) {
		out = append(out, prefix+"     "+text)
	}

	switch {
	case len(s.DetailedDiff) > 0:
		// Updates / replaces: pulumi tells us exactly which paths changed.
		replaceKeys := map[string]bool{}
		for _, k := range s.Keys {
			replaceKeys[k] = true
		}
		paths := make([]string, 0, len(s.DetailedDiff))
		for p := range s.DetailedDiff {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for i, p := range paths {
			if i >= limit {
				add("~", fmt.Sprintf("...and %d more propert(ies)", len(paths)-i))
				break
			}
			d := s.DetailedDiff[p]
			note := ""
			if strings.HasSuffix(d.Kind, "-replace") || replaceKeys[rootOf(p)] {
				note = "  (requires replacement)"
			}
			oldV, oldOK := lookupPath(s.OldState.Inputs, p)
			newV, newOK := lookupPath(s.NewState.Inputs, p)
			switch {
			case strings.HasPrefix(d.Kind, "add"):
				add("+", fmt.Sprintf("%s: %s%s", p, renderVal(newV), note))
			case strings.HasPrefix(d.Kind, "delete"):
				add("-", fmt.Sprintf("%s: %s%s", p, renderVal(oldV), note))
			case oldOK || newOK:
				add("~", fmt.Sprintf("%s: %s => %s%s", p, renderVal(oldV), renderVal(newV), note))
			default:
				add("~", p+note)
			}
		}
	case s.Op == "delete":
		out = inputLines(s.OldState.Inputs, "-", limit)
	default:
		// Creates (and anything else with no detailed diff): show the
		// inputs being set.
		out = inputLines(s.NewState.Inputs, opPrefix(s.Op), limit)
	}
	return out
}

func inputLines(inputs map[string]any, prefix string, limit int) []string {
	if len(inputs) == 0 || prefix == "" {
		return nil
	}
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for i, k := range keys {
		if i >= limit {
			out = append(out, fmt.Sprintf("%s     ...and %d more propert(ies)", prefix, len(keys)-i))
			break
		}
		out = append(out, fmt.Sprintf("%s     %s: %s", prefix, k, renderVal(inputs[k])))
	}
	return out
}

// rootOf returns the first segment of a property path ("config.rules[3]"
// → "config") for matching against replacement-trigger keys.
func rootOf(path string) string {
	if i := strings.IndexAny(path, ".["); i >= 0 {
		return path[:i]
	}
	return path
}

// lookupPath walks a pulumi property path ("config.ingresses[0].hostname")
// through nested maps/slices. Quoted segments (["a.b"]) are supported.
func lookupPath(v any, path string) (any, bool) {
	if v == nil {
		return nil, false
	}
	rest := path
	for rest != "" {
		switch rest[0] {
		case '.':
			rest = rest[1:]
		case '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, false
			}
			seg := rest[1:end]
			rest = rest[end+1:]
			if len(seg) >= 2 && seg[0] == '"' && seg[len(seg)-1] == '"' {
				m, ok := v.(map[string]any)
				if !ok {
					return nil, false
				}
				v, ok = m[seg[1:len(seg)-1]]
				if !ok {
					return nil, false
				}
				continue
			}
			idx, err := strconv.Atoi(seg)
			if err != nil {
				return nil, false
			}
			arr, ok := v.([]any)
			if !ok || idx < 0 || idx >= len(arr) {
				return nil, false
			}
			v = arr[idx]
		default:
			end := strings.IndexAny(rest, ".[")
			seg := rest
			if end >= 0 {
				seg, rest = rest[:end], rest[end:]
			} else {
				rest = ""
			}
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			v, ok = m[seg]
			if !ok {
				return nil, false
			}
		}
	}
	return v, true
}

// pulumiSigKey marks special-typed property values in pulumi state JSON;
// pulumiSecretSig is the signature value for secrets.
const (
	pulumiSigKey    = "4dabf18193072939515e22adb298388d"
	pulumiSecretSig = "1b47061264138c4ac30d75fd1eb44270"
)

const (
	maxValueLen   = 80
	maxListElems  = 3
	maxNestedKeys = 4
	maxDepth      = 3
	maxLineValue  = 200
)

// renderVal is renderValue plus a total-length clip so one deeply nested
// property can't flood the summary.
func renderVal(v any) string {
	s := renderValue(v, 0)
	if len(s) > maxLineValue {
		s = s[:maxLineValue] + "…"
	}
	return s
}

// renderValue renders a property value compactly for the plan summary.
// Secrets are masked, long strings truncated, nesting depth-limited.
func renderValue(v any, depth int) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return truncateQuoted(t)
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case map[string]any:
		if sig, ok := t[pulumiSigKey]; ok {
			if sig == pulumiSecretSig {
				return "[secret]"
			}
			return "[asset]"
		}
		if depth >= maxDepth {
			return "{...}"
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for i, k := range keys {
			if i >= maxNestedKeys {
				parts = append(parts, "...")
				break
			}
			parts = append(parts, k+": "+renderValue(t[k], depth+1))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		if depth >= maxDepth {
			return "[...]"
		}
		var parts []string
		for i, e := range t {
			if i >= maxListElems {
				parts = append(parts, fmt.Sprintf("...%d more", len(t)-i))
				break
			}
			parts = append(parts, renderValue(e, depth+1))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%v", t)
	}
}

func truncateQuoted(s string) string {
	if len(s) > maxValueLen {
		s = s[:maxValueLen] + "…"
	}
	return strconv.Quote(s)
}

// shortType compresses "cloudflare:index/zeroTrustTunnelCloudflaredConfig:
// ZeroTrustTunnelCloudflaredConfig" to "cloudflare:ZeroTrustTunnelCloudflaredConfig".
// Step-level type may be empty in pulumi's JSON; fall back to the states'.
func shortType(s previewStep) string {
	t := s.Type
	if t == "" {
		t = s.NewState.Type
	}
	if t == "" {
		t = s.OldState.Type
	}
	parts := strings.Split(t, ":")
	if len(parts) == 3 {
		return parts[0] + ":" + parts[2]
	}
	return t
}

func opPrefix(op string) string {
	switch op {
	case "create", "import":
		return "+"
	case "update":
		return "~"
	case "delete":
		return "-"
	case "replace", "create-replacement":
		return "±"
	}
	return ""
}

// displayName returns the resource name portion of a Pulumi URN, or the
// full URN if parsing fails.
func displayName(urn string) string {
	// URN: urn:pulumi:<stack>::<project>::<type>::<name>
	idx := strings.LastIndex(urn, "::")
	if idx < 0 {
		return urn
	}
	return urn[idx+2:]
}
