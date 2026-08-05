package hcl

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

// planJSON is the subset of `terraform show -json <planfile>` output we
// consume (plan representation, format_version 1.x). resource_changes
// carries the planned operations; resource_drift (refresh-only plans)
// carries state-vs-reality differences.
type planJSON struct {
	FormatVersion   string           `json:"format_version"`
	ResourceChanges []resourceChange `json:"resource_changes"`
	ResourceDrift   []resourceChange `json:"resource_drift"`
	Errored         bool             `json:"errored"`
}

type resourceChange struct {
	Address string       `json:"address"`
	Type    string       `json:"type"`
	Name    string       `json:"name"`
	Change  changeDetail `json:"change"`
	// ActionReason (e.g. "replace_because_cannot_update") is informational.
	ActionReason string `json:"action_reason"`
}

type changeDetail struct {
	// Actions: ["no-op"] | ["read"] | ["create"] | ["update"] | ["delete"] |
	// ["delete","create"] / ["create","delete"] (replace).
	Actions []string `json:"actions"`
	Before  any      `json:"before"`
	After   any      `json:"after"`
	// AfterUnknown mirrors After's structure with true at paths whose values
	// are only known after apply.
	AfterUnknown any `json:"after_unknown"`
	// BeforeSensitive/AfterSensitive mirror Before/After with true (or a
	// nested structure containing true) at sensitive paths. These drive
	// masking - sensitive values never reach rendered output.
	BeforeSensitive any `json:"before_sensitive"`
	AfterSensitive  any `json:"after_sensitive"`
	// ReplacePaths lists attribute paths that force the replacement.
	ReplacePaths [][]any `json:"replace_paths"`
}

// Op classification derived from change.actions.
const (
	opNoop    = "no-op"
	opRead    = "read"
	opCreate  = "create"
	opUpdate  = "update"
	opDelete  = "delete"
	opReplace = "replace"
)

// opOf maps a change.actions list to one op keyword.
func opOf(actions []string) string {
	switch len(actions) {
	case 1:
		switch actions[0] {
		case "create":
			return opCreate
		case "update":
			return opUpdate
		case "delete":
			return opDelete
		case "read":
			return opRead
		}
		return opNoop
	case 2:
		a, b := actions[0], actions[1]
		if (a == "delete" && b == "create") || (a == "create" && b == "delete") {
			return opReplace
		}
	}
	return opNoop
}

func opPrefix(op string) string {
	switch op {
	case opCreate:
		return "+"
	case opUpdate:
		return "~"
	case opDelete:
		return "-"
	case opReplace:
		return "±"
	}
	return ""
}

// parsePlan decodes a `show -json` blob. An unparseable blob is an error -
// callers treat that as a failed operation, never as "no changes".
func parsePlan(raw []byte) (*planJSON, error) {
	var p planJSON
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse terraform plan json: %w", err)
	}
	if p.FormatVersion == "" {
		return nil, fmt.Errorf("terraform plan json missing format_version (not a plan representation?)")
	}
	return &p, nil
}

// countsFrom tallies per-op counts over a change list.
func countsFrom(changes []resourceChange) summary.Counts {
	var c summary.Counts
	for _, rc := range changes {
		switch opOf(rc.Change.Actions) {
		case opCreate:
			c.Add++
		case opUpdate:
			c.Change++
		case opDelete:
			c.Delete++
		case opReplace:
			c.Replace++
		}
	}
	return c
}

// changedAddresses returns the addresses of resources that actually change
// (excluding no-op/read). The drift runner fingerprints this set - address
// strings play the role Pulumi URNs do.
func changedAddresses(changes []resourceChange) []string {
	var out []string
	for _, rc := range changes {
		if opPrefix(opOf(rc.Change.Actions)) != "" && rc.Address != "" {
			out = append(out, rc.Address)
		}
	}
	return out
}

// driftResources converts a resource-change list into the normalized
// iac.ResourceChange shape the drift runner filters over. No-op/read
// entries are excluded. Property paths (dotted, for ignore_properties) are
// computed for update/replace ops from the before/after attribute diff.
func driftResources(changes []resourceChange) []iac.ResourceChange {
	var out []iac.ResourceChange
	for _, rc := range changes {
		op := opOf(rc.Change.Actions)
		if opPrefix(op) == "" {
			continue
		}
		var paths []string
		if op == opUpdate || op == opReplace {
			paths = changedPaths(rc.Change.Before, rc.Change.After)
		}
		out = append(out, iac.ResourceChange{
			Address:  rc.Address,
			Type:     rc.Type,
			Op:       op,
			Paths:    paths,
			Category: terraformCategory(op),
		})
	}
	return out
}

// terraformCategory classifies a refresh-only drift op for treat_as_drift.
// In `resource_drift`, a delete means the resource vanished from the cloud
// while state still tracks it (orphaned); a create means the cloud has a
// resource state does not (missing). Updates/replaces are property changes.
func terraformCategory(op string) string {
	switch op {
	case opDelete:
		return iac.DriftOrphaned
	case opCreate:
		return iac.DriftMissing
	default:
		return iac.DriftChanged
	}
}

// changedPaths returns the dotted property paths whose scalar values differ
// between before and after ("tags.LastScanned", "ingress[0].cidr"). Nested
// maps and lists are walked recursively so ignore_properties can target a
// single nested attribute. Mirrors the Pulumi detailedDiff path style.
func changedPaths(before, after any) []string {
	var out []string
	var walk func(prefix string, b, a any)
	walk = func(prefix string, b, a any) {
		if reflect.DeepEqual(b, a) {
			return
		}
		bm, bok := b.(map[string]any)
		am, aok := a.(map[string]any)
		if bok || aok {
			seen := map[string]bool{}
			for k := range bm {
				seen[k] = true
			}
			for k := range am {
				seen[k] = true
			}
			keys := make([]string, 0, len(seen))
			for k := range seen {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				child := k
				if prefix != "" {
					child = prefix + "." + k
				}
				walk(child, bm[k], am[k])
			}
			return
		}
		bl, blok := b.([]any)
		al, alok := a.([]any)
		if blok || alok {
			n := len(bl)
			if len(al) > n {
				n = len(al)
			}
			for i := 0; i < n; i++ {
				var bv, av any
				if i < len(bl) {
					bv = bl[i]
				}
				if i < len(al) {
					av = al[i]
				}
				walk(fmt.Sprintf("%s[%d]", prefix, i), bv, av)
			}
			return
		}
		// Scalar leaf that differs.
		if prefix != "" {
			out = append(out, prefix)
		}
	}
	walk("", before, after)
	return out
}

// shortSummary renders one block per changed resource: a header line with
// the op symbol and address, then indented property lines. Lines keep
// +/-/~/± prefixes so GitHub's ```diff fence colors them (same shape as
// the pulumi adapter's summary).
func shortSummary(changes []resourceChange, limit int) string {
	if len(changes) == 0 {
		return ""
	}
	const propLimit = 10
	changed := 0
	for _, rc := range changes {
		if opPrefix(opOf(rc.Change.Actions)) != "" {
			changed++
		}
	}
	var b strings.Builder
	shown := 0
	for _, rc := range changes {
		op := opOf(rc.Change.Actions)
		prefix := opPrefix(op)
		if prefix == "" {
			continue
		}
		if shown >= limit {
			fmt.Fprintf(&b, "...and %d more resource(s)\n", changed-shown)
			break
		}
		fmt.Fprintf(&b, "%s %s\n", prefix, rc.Address)
		for _, line := range propertyLines(rc, op, propLimit) {
			b.WriteString(line)
			b.WriteString("\n")
		}
		shown++
	}
	return strings.TrimRight(b.String(), "\n")
}

// propertyLines renders per-attribute changes for one resource, capped at
// limit lines. Sensitive values are masked, unknown values render as
// "(known after apply)".
func propertyLines(rc resourceChange, op string, limit int) []string {
	before, _ := rc.Change.Before.(map[string]any)
	after, _ := rc.Change.After.(map[string]any)
	beforeSens := rc.Change.BeforeSensitive
	afterSens := rc.Change.AfterSensitive
	afterUnknown := rc.Change.AfterUnknown

	replaceForced := map[string]bool{}
	for _, p := range rc.Change.ReplacePaths {
		if len(p) > 0 {
			if root, ok := p[0].(string); ok {
				replaceForced[root] = true
			}
		}
	}

	renderBefore := func(k string) string {
		return renderVal(maskValue(before[k], subMarker(beforeSens, k)))
	}
	renderAfter := func(k string) string {
		if isFullyUnknown(subMarker(afterUnknown, k)) {
			return "(known after apply)"
		}
		v := maskValue(after[k], subMarker(afterSens, k))
		v = annotateUnknown(v, subMarker(afterUnknown, k))
		return renderVal(v)
	}

	var out []string
	add := func(prefix, text string) {
		out = append(out, prefix+"     "+text)
	}

	switch op {
	case opDelete:
		keys := sortedKeys(before)
		for i, k := range keys {
			if i >= limit {
				add("-", fmt.Sprintf("...and %d more attribute(s)", len(keys)-i))
				break
			}
			add("-", k+": "+renderBefore(k))
		}
	case opCreate:
		keys := sortedKeys(after)
		shown := 0
		for _, k := range keys {
			// Skip attributes that are entirely unknown-and-null: noise.
			if after[k] == nil && !isFullyUnknown(subMarker(afterUnknown, k)) {
				continue
			}
			if shown >= limit {
				add("+", "...and more attribute(s)")
				break
			}
			add("+", k+": "+renderAfter(k))
			shown++
		}
	default: // update / replace
		keys := diffKeys(before, after, afterUnknown, beforeSens, afterSens)
		for i, k := range keys {
			if i >= limit {
				add("~", fmt.Sprintf("...and %d more attribute(s)", len(keys)-i))
				break
			}
			note := ""
			if op == opReplace && replaceForced[k] {
				note = "  (forces replacement)"
			}
			_, inBefore := before[k]
			_, inAfter := after[k]
			unknown := !isNoMarker(subMarker(afterUnknown, k))
			switch {
			case !inBefore && (inAfter || unknown):
				add("+", k+": "+renderAfter(k)+note)
			case inBefore && !inAfter && !unknown:
				add("-", k+": "+renderBefore(k)+note)
			default:
				add("~", k+": "+renderBefore(k)+" => "+renderAfter(k)+note)
			}
		}
	}
	return out
}

// diffKeys returns the sorted union of attribute keys whose values differ
// between before and after, or that are unknown after apply. Sensitive
// attributes still diff (by value equality) - only their rendering is
// masked.
func diffKeys(before, after map[string]any, afterUnknown, beforeSens, afterSens any) []string {
	seen := map[string]bool{}
	for k := range before {
		seen[k] = true
	}
	for k := range after {
		seen[k] = true
	}
	if m, ok := afterUnknown.(map[string]any); ok {
		for k := range m {
			seen[k] = true
		}
	}
	var out []string
	for k := range seen {
		unknown := !isNoMarker(subMarker(afterUnknown, k))
		if !unknown && reflect.DeepEqual(before[k], after[k]) {
			// Identical value; also identical sensitivity -> no change.
			if reflect.DeepEqual(subMarker(beforeSens, k), subMarker(afterSens, k)) {
				continue
			}
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// subMarker descends one key into a sensitive/unknown marker structure.
// A scalar true marker applies to every nested path.
func subMarker(marker any, key string) any {
	switch t := marker.(type) {
	case bool:
		if t {
			return true
		}
		return nil
	case map[string]any:
		return t[key]
	}
	return nil
}

// subMarkerIdx is subMarker for array elements.
func subMarkerIdx(marker any, i int) any {
	switch t := marker.(type) {
	case bool:
		if t {
			return true
		}
		return nil
	case []any:
		if i >= 0 && i < len(t) {
			return t[i]
		}
	}
	return nil
}

// isNoMarker reports whether marker contains no true anywhere.
func isNoMarker(marker any) bool {
	switch t := marker.(type) {
	case nil:
		return true
	case bool:
		return !t
	case map[string]any:
		for _, v := range t {
			if !isNoMarker(v) {
				return false
			}
		}
		return true
	case []any:
		for _, v := range t {
			if !isNoMarker(v) {
				return false
			}
		}
		return true
	}
	return true
}

// isFullyUnknown reports whether marker is exactly true (whole value
// unknown after apply).
func isFullyUnknown(marker any) bool {
	b, ok := marker.(bool)
	return ok && b
}

const sensitivePlaceholder = "[sensitive]"

// maskValue returns a copy of v with every part marked sensitive replaced
// by the placeholder. The marker mirrors v's structure; a scalar true masks
// the whole value.
func maskValue(v any, marker any) any {
	if isNoMarker(marker) {
		return v
	}
	if b, ok := marker.(bool); ok && b {
		return sensitivePlaceholder
	}
	switch tv := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(tv))
		for k, val := range tv {
			out[k] = maskValue(val, subMarker(marker, k))
		}
		return out
	case []any:
		out := make([]any, len(tv))
		for i, val := range tv {
			out[i] = maskValue(val, subMarkerIdx(marker, i))
		}
		return out
	default:
		// Structure mismatch between value and marker: fail closed - a
		// sensitive marker exists somewhere below, so mask the whole value.
		return sensitivePlaceholder
	}
}

// annotateUnknown replaces parts of v that are unknown after apply with the
// "(known after apply)" placeholder.
func annotateUnknown(v any, marker any) any {
	if isNoMarker(marker) {
		return v
	}
	if isFullyUnknown(marker) {
		return "(known after apply)"
	}
	switch tv := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(tv))
		for k, val := range tv {
			out[k] = annotateUnknown(val, subMarker(marker, k))
		}
		// Keys that exist only in the unknown marker are new computed
		// attributes with no after value yet.
		if mm, ok := marker.(map[string]any); ok {
			for k, sub := range mm {
				if _, exists := out[k]; !exists && !isNoMarker(sub) {
					out[k] = "(known after apply)"
				}
			}
		}
		return out
	case []any:
		out := make([]any, len(tv))
		for i, val := range tv {
			out[i] = annotateUnknown(val, subMarkerIdx(marker, i))
		}
		return out
	default:
		return v
	}
}

// scrubPlanJSON masks sensitive values inside a raw plan JSON blob before
// it is stored/rendered. It walks the whole document and, wherever a map
// carries a (value, sensitive-marker) pair - change.before/before_sensitive,
// change.after/after_sensitive, or state values/sensitive_values - replaces
// the sensitive parts with the placeholder. Unparseable input returns an
// error; callers must not fall back to the raw blob.
func scrubPlanJSON(raw []byte) (string, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("scrub terraform plan json: %w", err)
	}
	scrubbed := scrubNode(doc)
	out, err := json.Marshal(scrubbed)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// scrubNode recursively applies sensitive-marker masking to any map that
// declares one of the known value/marker pairs.
func scrubNode(v any) any {
	switch t := v.(type) {
	case map[string]any:
		pairs := [][2]string{
			{"before", "before_sensitive"},
			{"after", "after_sensitive"},
			{"values", "sensitive_values"},
		}
		for _, p := range pairs {
			valKey, markKey := p[0], p[1]
			if marker, ok := t[markKey]; ok {
				if val, ok := t[valKey]; ok {
					t[valKey] = maskValue(val, marker)
				}
			}
		}
		for k, val := range t {
			t[k] = scrubNode(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = scrubNode(val)
		}
		return t
	default:
		return v
	}
}

// Rendering limits, mirroring the pulumi adapter's summary constraints.
const (
	maxValueLen   = 80
	maxListElems  = 3
	maxNestedKeys = 4
	maxDepth      = 3
	maxLineValue  = 200
)

// renderVal renders a property value compactly, clipping total length so a
// deeply nested attribute can't flood the summary.
func renderVal(v any) string {
	s := renderValue(v, 0)
	if len(s) > maxLineValue {
		s = s[:maxLineValue] + "…"
	}
	return s
}

func renderValue(v any, depth int) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		if t == sensitivePlaceholder || t == "(known after apply)" {
			return t
		}
		return truncateQuoted(t)
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case map[string]any:
		if depth >= maxDepth {
			return "{...}"
		}
		keys := sortedKeys(t)
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

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
