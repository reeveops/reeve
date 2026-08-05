package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/reeveops/reeve/internal/core/discovery"
)

// WriteClusteredStacks updates the `engine.stacks:` block of the given
// engine config file with the supplied clustered declarations. Comments
// on sibling keys (engine.filters, engine.change_mapping, etc.) are
// preserved via yaml.v3 node manipulation.
//
// Behavior:
//   - If `stacks:` exists, its child content is replaced with the new list.
//   - If `stacks:` is absent, it's inserted as the last key under `engine:`.
//   - A `.bak` copy of the original file is written alongside.
//   - dryRun=true returns the new YAML bytes without touching disk.
func WriteClusteredStacks(path string, decls []discovery.Declaration, dryRun bool) ([]byte, error) {
	// Scoped to the engine config's own directory so the file name cannot
	// resolve through a symlink to somewhere else. This protects the final
	// component, which is the realistic case (a committed
	// `.reeve/pulumi.yaml -> ...`); a symlinked .reeve/ itself is still
	// followed, since that is the directory the operator pointed us at.
	dirRoot, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer dirRoot.Close()
	name := filepath.Base(path)

	data, err := dirRoot.ReadFile(name)
	if err != nil {
		return nil, err
	}
	out, err := ClusteredStacksBytes(data, decls)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if dryRun {
		return out, nil
	}
	if err := dirRoot.WriteFile(name+".bak", data, 0o600); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	if err := dirRoot.WriteFile(name, out, 0o600); err != nil {
		return nil, err
	}
	return out, nil
}

// ClusteredStacksBytes is the in-memory core of WriteClusteredStacks: it
// replaces (or inserts) the `engine.stacks:` block inside the given YAML
// document and returns the re-encoded bytes. Comments on sibling keys are
// preserved via yaml.v3 node manipulation. Used by `stacks discover --write`
// and by `reeve init` to pre-fill a freshly scaffolded engine config.
func ClusteredStacksBytes(data []byte, decls []discovery.Declaration) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	engine := findMapValue(&root, "engine")
	if engine == nil {
		return nil, fmt.Errorf("missing top-level engine: block")
	}
	stacksNode := buildStacksNode(decls)

	replaced := replaceMapChild(engine, "stacks", stacksNode)
	if !replaced {
		engine.Content = append(engine.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "stacks"},
			stacksNode,
		)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}

// buildStacksNode constructs a sequence node matching the schema:
//
//	stacks:
//	  - pattern: "projects/*"
//	    stacks: [dev, prod]
//	  - project: api
//	    path: projects/api
//	    stacks: [dev, prod]
func buildStacksNode(decls []discovery.Declaration) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, d := range decls {
		entry := &yaml.Node{Kind: yaml.MappingNode}
		if d.Pattern != "" {
			entry.Content = append(entry.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "pattern"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: d.Pattern, Style: yaml.DoubleQuotedStyle},
			)
		}
		if d.Project != "" {
			entry.Content = append(entry.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "project"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: d.Project},
			)
		}
		if d.Path != "" {
			entry.Content = append(entry.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "path"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: d.Path},
			)
		}
		// stacks: [a, b, c] flow-style.
		stacksSeq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, n := range d.Stacks {
			stacksSeq.Content = append(stacksSeq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: n})
		}
		entry.Content = append(entry.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "stacks"},
			stacksSeq,
		)
		seq.Content = append(seq.Content, entry)
	}
	return seq
}

// findMapValue returns the *yaml.Node value for a given mapping key,
// descending through the document node.
func findMapValue(root *yaml.Node, key string) *yaml.Node {
	if root == nil {
		return nil
	}
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		return findMapValue(root.Content[0], key)
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}

// replaceMapChild swaps the value of key inside a mapping node.
func replaceMapChild(m *yaml.Node, key string, newVal *yaml.Node) bool {
	if m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = newVal
			return true
		}
	}
	return false
}

// DryRunDiff produces a unified diff between the current and proposed
// contents. Small shim used by `reeve stacks discover --diff`.
func DryRunDiff(path string, proposed []byte) (string, error) {
	dirRoot, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer dirRoot.Close()
	cur, err := dirRoot.ReadFile(filepath.Base(path))
	if err != nil {
		return "", err
	}
	return unifiedDiff(filepath.Base(path), cur, proposed), nil
}

// unifiedDiff is a minimal line-by-line diff renderer. Third-party libs
// like pmezard/go-difflib would produce nicer output; this keeps the
// dependency footprint tight.
func unifiedDiff(name string, a, b []byte) string {
	if bytes.Equal(a, b) {
		return ""
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--- %s\n+++ %s (proposed)\n", name, name)
	aLines := bytes.Split(a, []byte("\n"))
	bLines := bytes.Split(b, []byte("\n"))
	i, j := 0, 0
	for i < len(aLines) || j < len(bLines) {
		switch {
		case i < len(aLines) && j < len(bLines) && bytes.Equal(aLines[i], bLines[j]):
			fmt.Fprintf(&buf, " %s\n", aLines[i])
			i++
			j++
		case j >= len(bLines) || (i < len(aLines) && (j == len(bLines) || !bytes.Equal(aLines[i], bLines[j]))):
			fmt.Fprintf(&buf, "-%s\n", aLines[i])
			i++
		default:
			fmt.Fprintf(&buf, "+%s\n", bLines[j])
			j++
		}
	}
	return buf.String()
}
