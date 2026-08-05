// Package discovery owns the generic stack discovery pipeline: doublestar
// pattern matching, include/exclude filtering, change mapping, and module
// dependency resolution. Engine adapters implement EnumerateStacks; this
// package does everything engine-agnostic around it.
package discovery
