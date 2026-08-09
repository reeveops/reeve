package blob

import "strings"

// SlugComponent makes an arbitrary name safe to use as a single path
// component in a blob key, replacing anything outside [A-Za-z0-9-] with an
// underscore. fallback is returned when the input slugs to nothing.
//
// Names reaching key builders are not always ours: a Pulumi project name
// comes from Pulumi.yaml and a stack name from a filename, both of which
// live in the PR under review. S3-style keys are flat so a slash cannot
// traverse, and the filesystem store rejects "../" outright, but building
// keys from unsanitised input invites collisions and odd objects for no
// benefit.
//
// The transform is idempotent: slugging an already-slugged value returns it
// unchanged, so a key can be parsed back into components and re-derived.
func SlugComponent(s, fallback string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if strings.Trim(out, "_") == "" {
		return fallback
	}
	return out
}
