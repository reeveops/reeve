package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Migrator applies per-config_type version bumps. Each registered
// migration converts version N→N+1 for a single file. It also applies
// same-version rewrites: spelling modernisations (deprecated aliases) that
// do not change the schema version.
type Migrator struct {
	registry map[migrationKey]migrationFn
	rewrites map[string]rewriteFn
}

type migrationKey struct {
	ConfigType string
	From       int
}

type migrationFn func(*yaml.Node) error

// rewriteFn modernises a file in place without a version bump. It reports
// whether it changed anything.
type rewriteFn func(*yaml.Node) (bool, error)

// NewMigrator returns a Migrator with the built-in migrations registered.
func NewMigrator() *Migrator {
	m := &Migrator{registry: map[migrationKey]migrationFn{}, rewrites: map[string]rewriteFn{}}
	m.registry[migrationKey{ConfigType: "notifications", From: 1}] = migrateLegacySlackToChannels
	m.rewrites["drift"] = rewriteDriftSinksToChannels
	return m
}

// rewriteDriftSinksToChannels renames drift.yaml's retired `sinks:` key
// (shipped in v0.2.0) to `channels:`. The loader hard-errors on `sinks:`
// with a pointer at this command, so a file still using it must be
// migrated (or hand-renamed) before it loads again. A file that already
// uses `channels:` (or has neither key) is untouched; a file with both
// keys is left for the loader to reject.
func rewriteDriftSinksToChannels(root *yaml.Node) (bool, error) {
	m := docMapping(root)
	if m == nil {
		return false, fmt.Errorf("drift: expected a mapping document")
	}
	sinksIdx, channelsIdx := -1, -1
	for i := 0; i < len(m.Content); i += 2 {
		switch m.Content[i].Value {
		case "channels":
			channelsIdx = i
		case "sinks":
			sinksIdx = i
		}
	}
	if sinksIdx < 0 || channelsIdx >= 0 {
		// Nothing to rename, or channels: already present (both-keys is a
		// loader error; renaming would create a duplicate key).
		return false, nil
	}
	key := m.Content[sinksIdx]
	renamed := scalarNode("channels")
	renamed.HeadComment = key.HeadComment
	renamed.LineComment = key.LineComment
	renamed.FootComment = key.FootComment
	m.Content[sinksIdx] = renamed
	return true, nil
}

// migrateLegacySlackToChannels rewrites the legacy `slack:` block into an
// entry of the generic `channels:` list:
//
//	slack:                      channels:
//	  enabled: true               - type: slack
//	  channel: "#x"        →        enabled: true
//	  events: [plan]                channel: "#x"
//	                                on: [plan]
//
// All other keys (auth_token, trigger, icons, rules) carry over verbatim;
// `events` is renamed to `on`. Value nodes are reused so comments and
// styles survive. A file without a `slack:` block only gets its version
// bumped. The loader hard-errors on the legacy `slack:` block with a
// pointer at this command, so a file still using it must be migrated
// before it loads again.
func migrateLegacySlackToChannels(root *yaml.Node) error {
	m := docMapping(root)
	if m == nil {
		return fmt.Errorf("notifications: expected a mapping document")
	}
	slackIdx := -1
	channelsIdx := -1
	for i := 0; i < len(m.Content); i += 2 {
		switch m.Content[i].Value {
		case "slack":
			slackIdx = i
		case "channels":
			channelsIdx = i
		}
	}
	if slackIdx < 0 {
		return nil // nothing to rewrite; version bump only
	}
	slackVal := m.Content[slackIdx+1]
	if slackVal.Kind != yaml.MappingNode {
		return fmt.Errorf("notifications: slack: expected a mapping")
	}

	channel := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	channel.Content = append(channel.Content, scalarNode("type"), scalarNode("slack"))
	for i := 0; i < len(slackVal.Content); i += 2 {
		k, v := slackVal.Content[i], slackVal.Content[i+1]
		if k.Value == "events" {
			k = scalarNode("on")
			k.HeadComment = slackVal.Content[i].HeadComment
			k.LineComment = slackVal.Content[i].LineComment
		}
		channel.Content = append(channel.Content, k, v)
	}

	if channelsIdx >= 0 {
		// A channels list already exists (mixed legacy file): append the converted
		// slack entry and drop the slack key.
		seq := m.Content[channelsIdx+1]
		if seq.Kind != yaml.SequenceNode {
			return fmt.Errorf("notifications: channels: expected a sequence")
		}
		seq.Content = append(seq.Content, channel)
		m.Content = append(m.Content[:slackIdx], m.Content[slackIdx+2:]...)
		return nil
	}

	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{channel}}
	m.Content[slackIdx] = scalarNode("channels")
	m.Content[slackIdx].HeadComment = slackVal.HeadComment
	m.Content[slackIdx+1] = seq
	return nil
}

// docMapping unwraps a document node to its top-level mapping.
func docMapping(root *yaml.Node) *yaml.Node {
	n := root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

func scalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// MigrateDirectory walks .reeve/ and migrates each file to the latest
// supported version. dryRun=true skips writes; diffs are printed.
func (m *Migrator) MigrateDirectory(dir string, dryRun bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	// Scoped to dir: these files come from the checked-out tree, where an
	// entry can be a symlink pointing outside it. Rewriting a config in
	// place must not follow one out.
	dirRoot, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer dirRoot.Close()

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml") {
			continue
		}
		path := filepath.Join(dir, n) // for messages only
		if err := m.migrateOne(dirRoot, n, path, dryRun); err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
	}
	return nil
}

// migrateOne migrates the file named name inside dirRoot. path is the same
// file's display path, used only in messages.
func (m *Migrator) migrateOne(dirRoot *os.Root, name, path string, dryRun bool) error {
	data, err := dirRoot.ReadFile(name)
	if err != nil {
		return err
	}
	// Extract header without strict unmarshal so old files still parse.
	// Match the two keys independently so any order and interleaved comments
	// or blank lines are tolerated (the loader accepts them, so migrate must
	// too).
	vm := versionRE.FindSubmatch(data)
	ctm := configTypeRE.FindSubmatch(data)
	if vm == nil || ctm == nil {
		return fmt.Errorf("cannot find version + config_type header")
	}
	fromVer := 0
	if _, err := fmt.Sscanf(string(vm[1]), "%d", &fromVer); err != nil {
		return err
	}
	configType := string(ctm[1])

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}

	changed := false
	cur := fromVer
	for {
		fn, ok := m.registry[migrationKey{ConfigType: configType, From: cur}]
		if !ok {
			break
		}
		if err := fn(&root); err != nil {
			return err
		}
		if err := setScalarField(&root, "version", fmt.Sprintf("%d", cur+1)); err != nil {
			return err
		}
		cur++
		changed = true
	}

	if rw, ok := m.rewrites[configType]; ok {
		did, err := rw(&root)
		if err != nil {
			return err
		}
		changed = changed || did
	}

	if !changed {
		return nil
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Printf("--- would migrate %s (v%d → v%d) ---\n%s\n", path, fromVer, cur, string(out))
		return nil
	}
	if err := dirRoot.WriteFile(name+".bak", data, 0o600); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return dirRoot.WriteFile(name, out, 0o600)
}

// Header matchers tolerate what the strict loader accepts: optional quotes
// around the value and a trailing line comment. Without this, a valid header
// like `version: 1  # schema` or `config_type: "drift"` failed the migrator
// (which then aborted the whole directory), so files the loader was happy to
// read could not be migrated.
var (
	versionRE    = regexp.MustCompile(`(?m)^version:\s*["']?(\d+)["']?\s*(?:#.*)?$`)
	configTypeRE = regexp.MustCompile(`(?m)^config_type:\s*["']?([a-z_]+)["']?\s*(?:#.*)?$`)
)

func setScalarField(root *yaml.Node, key, value string) error {
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		return setScalarField(root.Content[0], key, value)
	}
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping, got %v", root.Kind)
	}
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content[i+1].Value = value
			return nil
		}
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value},
	)
	return nil
}
