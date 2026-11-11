package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// slugHashLen is how much of the disambiguating digest is kept: 32 hex
// chars, i.e. the full 128 bits.
//
// Shorter is tempting for readability, but these names come from the repo
// under review. At 32 bits an author needs only ~65k unsafe names sharing
// one sanitised form to have a material birthday-collision chance, and a
// collision points two distinct stacks at one lock object. Keys here are
// machine-read, so length costs nothing worth having.
const slugHashLen = 32

// digestSep separates a sanitised name from its disambiguating digest.
//
// It is deliberately a rune that a pass-through name can never contain: a
// name is returned unchanged only when it is drawn from [A-Za-z0-9-], so
// anything holding "_" took the transforming branch. That disjointness is
// what makes the mapping injective. Without it, a component literally named
// "a_b" + digestSep + shortHash("a/b") would be safe, pass through
// unchanged, and address the same object as "a/b".
const digestSep = "__"

// SlugComponent makes an arbitrary name safe to use as a single path
// component in a blob key. fallback is used when the input is empty.
//
// Names reaching key builders are not always ours: a Pulumi project name
// comes from Pulumi.yaml and a stack name from a filename, both of which
// live in the PR under review. S3-style keys are flat so a slash cannot
// traverse, and the filesystem store rejects "../" outright, but building
// keys from unsanitised input invites collisions and odd objects for no
// benefit.
//
// The mapping is INJECTIVE, which is the property that matters: distinct
// names must never address the same lock object or plan artifact. Merely
// replacing unsafe runes is not enough - it maps "a/b" and "a_b" together -
// so a transformed name carries a digest of the original, in a namespace no
// pass-through name can reach.
//
// It is NOT idempotent, and must not be relied on to be: slugging an
// already-slugged value transforms it again. Derive keys from real names
// only. The lock store reads a lock's project/stack from the object's own
// content rather than parsing them back out of its key.
func SlugComponent(s, fallback string) string {
	// The ONLY inputs returned verbatim are non-empty pass-through names.
	// Everything else - including the empty string - lands in the
	// transformed namespace, which always contains digestSep and which no
	// pass-through name can reach. An earlier version returned the fallback
	// verbatim for empty input, so an empty stack name and a stack
	// literally named "stack" addressed the same lock.
	if s != "" && isPassthrough(s) {
		return s
	}
	base, _ := sanitizeComponent(s)
	if strings.Trim(base, "_") == "" {
		// Nothing usable survived (empty input, or "///"). The fallback
		// only supplies something readable to look at; the digest is what
		// identifies it.
		base = sanitizeFallback(fallback)
	}
	return base + digestSep + shortHash(s)
}

// sanitizeFallback makes a caller-supplied fallback safe to embed. It never
// reaches the pass-through namespace on its own - the digest is appended by
// the caller - so it only has to be free of unsafe runes.
func sanitizeFallback(fb string) string {
	b, _ := sanitizeComponent(fb)
	if strings.Trim(b, "_") == "" {
		return "key"
	}
	return b
}

// isPassthrough reports whether a name is safe to use verbatim. The set
// deliberately excludes "_" so that the transformed namespace (which always
// contains digestSep) can never be produced by a pass-through name.
func isPassthrough(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

// sanitizeComponent maps every rune outside the safe set to "_", reporting
// whether anything was replaced.
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
