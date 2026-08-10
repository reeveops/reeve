package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// slugHashLen is how much of the disambiguating digest is kept. 8 hex chars
// is 32 bits: ample for distinguishing the stack names in one repo, and
// short enough to keep keys readable.
const slugHashLen = 8

// SlugComponent makes an arbitrary name safe to use as a single path
// component in a blob key. fallback is used when the input has no usable
// characters at all.
//
// Names reaching key builders are not always ours: a Pulumi project name
// comes from Pulumi.yaml and a stack name from a filename, both of which
// live in the PR under review. S3-style keys are flat so a slash cannot
// traverse, and the filesystem store rejects "../" outright, but building
// keys from unsanitised input invites collisions and odd objects for no
// benefit.
//
// Replacement alone is NOT enough: mapping every unsafe rune to "_" makes
// "a/b" and "a_b" the same component, which would point two distinct stacks
// at one lock object and let one stack's plan artifact overwrite another's.
// So whenever a rune is replaced, a short digest of the ORIGINAL input is
// appended, which keeps distinct inputs distinct.
//
// The transform is idempotent, which lock keys require: parseLockKey feeds
// its result back into Get, which re-derives the key. Output contains only
// characters this function preserves, so re-slugging is identity.
func SlugComponent(s, fallback string) string {
	safeFallback, _ := sanitizeComponent(fallback)
	if strings.Trim(safeFallback, "_") == "" {
		// A caller passed a fallback that is itself unusable. Refuse to
		// propagate it into a key rather than inventing a path segment.
		safeFallback = "key"
	}
	if s == "" {
		return safeFallback
	}

	out, replaced := sanitizeComponent(s)
	if strings.Trim(out, "_") == "" {
		// Nothing survived (e.g. "///"). Fall back, but still disambiguate:
		// otherwise every degenerate name collapses onto one key.
		out = safeFallback
		replaced = true
	}
	if replaced {
		out += "-" + shortHash(s)
	}
	return out
}

// sanitizeComponent maps every rune outside the safe set to "_", reporting
// whether anything was replaced. "_" itself is in the safe set: it is the
// replacement character, and excluding it would make the transform
// non-idempotent.
func sanitizeComponent(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	replaced := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
			replaced = true
		}
	}
	return b.String(), replaced
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:slugHashLen]
}
