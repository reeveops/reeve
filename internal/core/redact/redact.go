// Package redact is the central secret-redaction pipeline. Every
// user-visible output path (PR comment, audit log, run artifacts,
// policy hook stdout, telemetry) funnels through a Redactor. See
// openspec/specs/core/rendering and openspec/specs/iac/policy-hooks.
package redact

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// MinSecretLength is the shortest literal string AddSecret will accept.
// Strings shorter than this are rejected because the false-positive rate
// becomes destructive (a 3-char secret would over-redact across normal
// output). Callers that genuinely need to redact a short token should
// register a regex via AddRule with anchoring context instead.
const MinSecretLength = 8

// Redactor wraps a set of rules. Rules and replacement are set at
// construction; the secrets set may be updated concurrently (the drift
// runner registers credentials from parallel per-stack goroutines while
// other goroutines redact), so access to it is guarded by mu.
type Redactor struct {
	rules       []*regexp.Regexp
	replacement string
	// mu guards secrets. AddSecret writes; sortedSecrets (via Redact) reads.
	mu sync.RWMutex
	// Known string values to replace (exact-match). Populated by
	// AddSecret - used when callers know the secret value (e.g. from
	// Pulumi's --show-secrets=false or from an auth credential).
	secrets map[string]struct{}
}

// New returns a Redactor with no rules - everything passes through.
func New() *Redactor { return &Redactor{replacement: "[redacted]", secrets: map[string]struct{}{}} }

// newDefault returns a Redactor with the common cloud-credential patterns
// every command applies as its floor. The rule list lives here - core -
// because the log handler needs it before any run layer exists: a logger
// with no redactor installed must not be reachable.
func newDefault() *Redactor {
	r := New()
	r.AddRule(`AKIA[0-9A-Z]{16}`)                            // AWS access key id
	r.AddRule(`ASIA[0-9A-Z]{16}`)                            // AWS STS access key id
	r.AddRule(`aws_secret_access_key\s*=\s*[A-Za-z0-9/+=]+`) // crude aws secret kv
	r.AddRule(`ghp_[A-Za-z0-9]{36,}`)                        // GitHub PAT
	r.AddRule(`gho_[A-Za-z0-9]{36,}`)                        // GitHub OAuth
	r.AddRule(`ghs_[A-Za-z0-9]{36,}`)                        // GitHub server-to-server
	r.AddRule(`xox[baprs]-[A-Za-z0-9-]+`)                    // Slack tokens
	return r
}

// NewDefault returns a Redactor with the common cloud-credential patterns
// every output sink applies as its floor. Exported because the log handler
// lives outside core/redact and must never run without a redactor.
func NewDefault() *Redactor { return newDefault() }

// WithReplacement changes the replacement string. Default "[redacted]".
func (r *Redactor) WithReplacement(s string) *Redactor {
	r.replacement = s
	return r
}

// AddRule compiles a regex and adds it. Invalid regexes are skipped with
// no error - use CompileRules for validation.
func (r *Redactor) AddRule(pattern string) *Redactor {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return r
	}
	r.rules = append(r.rules, re)
	return r
}

// CompileRules compiles a list of regex strings. Returns any compile
// errors so callers can surface them at lint time.
func CompileRules(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, nil
}

// NewFromRules returns a Redactor from pre-compiled regexes.
func NewFromRules(rules []*regexp.Regexp) *Redactor {
	return &Redactor{rules: rules, replacement: "[redacted]", secrets: map[string]struct{}{}}
}

// AddSecret registers a literal string to redact. Strings below
// MinSecretLength are silently ignored - their false-positive rate would
// destroy normal output. Callers should register credential values after
// acquiring them so stdout leaks don't expose them.
func (r *Redactor) AddSecret(s string) {
	if len(s) < MinSecretLength {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secrets[s] = struct{}{}
}

// Redact returns s with all rules + known secrets replaced. The replacement
// is deterministic: literal secrets are applied longest-first so a secret
// that is a suffix or prefix of another doesn't leave a partial leak. Regex
// rules are applied in the order they were compiled.
func (r *Redactor) Redact(s string) string {
	if r == nil {
		return s
	}
	out := s
	for _, sec := range r.sortedSecrets() {
		out = strings.ReplaceAll(out, sec, r.replacement)
	}
	for _, re := range r.rules {
		out = re.ReplaceAllString(out, r.replacement)
	}
	// Pulumi [secret] markers - handled idempotently.
	out = pulumiSecretMarker.ReplaceAllString(out, r.replacement)
	return out
}

// sortedSecrets returns the registered secrets in longest-first order. Map
// iteration is otherwise unordered, which made redaction non-deterministic
// (and unsafe when one secret was a substring of another).
func (r *Redactor) sortedSecrets() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.secrets))
	for sec := range r.secrets {
		out = append(out, sec)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j] // stable order for equal-length values
	})
	return out
}

// Redactables passes any struct through Redact field-by-field.
// For now we only need Redact(string) - callers handle nested structs.

// pulumiSecretMarker catches the standard Pulumi marker for secret values
// that escape into engine output: `[secret]` or `<secret>`.
var pulumiSecretMarker = regexp.MustCompile(`\[secret\]|<secret>`)
