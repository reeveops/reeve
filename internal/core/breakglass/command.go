package breakglass

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Usage is the canonical command syntax, echoed in every parse error.
const Usage = `/reeve breakglass "<justification>" apply`

// Command is a parsed break-glass comment command.
type Command struct {
	Justification string
	// Force mirrors `/reeve apply --force`: re-apply an already-applied
	// commit. Accepted only as a trailing token after the verb.
	Force bool
}

// quote pairs accepted for the justification. Mobile/desktop GitHub can
// autocorrect ASCII quotes to typographic ones; rejecting those would make
// the emergency path fail exactly when someone types it on a phone.
var openQuotes = map[rune]rune{'"': '"', '“': '”'}

// ParseCommand strictly parses the first line of a PR comment as
//
//	/reeve breakglass "<justification>" apply [--force]
//
// The justification MUST be a non-empty double-quoted string and MUST be
// followed by the verb `apply`. Anything else is a descriptive error (the
// caller posts it back to the PR as a helpful comment; no run happens).
// Escape sequences are not supported - the justification is everything
// between the opening quote and the next closing quote.
func ParseCommand(comment string) (Command, error) {
	line, _, _ := strings.Cut(strings.ReplaceAll(comment, "\r\n", "\n"), "\n")
	line = strings.TrimSpace(line)

	rest, ok := cutToken(line, "/reeve")
	if !ok {
		return Command{}, fmt.Errorf("not a /reeve command (usage: %s)", Usage)
	}
	rest, ok = cutToken(rest, "breakglass")
	if !ok {
		return Command{}, fmt.Errorf("expected `breakglass` after /reeve (usage: %s)", Usage)
	}

	rest = strings.TrimSpace(rest)
	open, size := utf8.DecodeRuneInString(rest)
	closing, quoted := openQuotes[open]
	if rest == "" || !quoted {
		return Command{}, fmt.Errorf("the justification must be a double-quoted string immediately after `breakglass` (usage: %s)", Usage)
	}
	body := rest[size:]
	end := strings.IndexAny(body, string(closing)+`"`)
	if end < 0 {
		return Command{}, fmt.Errorf("unterminated justification: missing closing quote (usage: %s)", Usage)
	}
	justification := strings.TrimSpace(body[:end])
	if justification == "" {
		return Command{}, fmt.Errorf("the justification must not be empty (usage: %s)", Usage)
	}
	_, closeSize := utf8.DecodeRuneInString(body[end:])
	tail := strings.Fields(body[end+closeSize:])

	if len(tail) == 0 {
		return Command{}, fmt.Errorf("missing verb after the justification: expected `apply` (usage: %s)", Usage)
	}
	if tail[0] != "apply" {
		return Command{}, fmt.Errorf("unsupported break-glass verb %q: only `apply` is supported (usage: %s)", tail[0], Usage)
	}
	cmd := Command{Justification: justification}
	switch {
	case len(tail) == 1:
	case len(tail) == 2 && (tail[1] == "--force" || tail[1] == "force"):
		cmd.Force = true
	default:
		return Command{}, fmt.Errorf("unexpected trailing input %q after `apply` (usage: %s)", strings.Join(tail[1:], " "), Usage)
	}
	return cmd, nil
}

// cutToken consumes tok at the start of s (followed by whitespace or EOL)
// and returns the remainder.
func cutToken(s, tok string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == tok {
		return "", true
	}
	if strings.HasPrefix(s, tok+" ") || strings.HasPrefix(s, tok+"\t") {
		return s[len(tok):], true
	}
	return "", false
}

// MalformedComment renders the helpful PR comment posted when a
// break-glass command fails to parse. Pure markdown; no run has happened.
func MalformedComment(parseErr error) string {
	var b strings.Builder
	b.WriteString("### ⛔ break-glass command not run\n\n")
	fmt.Fprintf(&b, "Could not parse the break-glass command: %s\n\n", parseErr.Error())
	b.WriteString("Usage:\n\n")
	fmt.Fprintf(&b, "```\n%s\n```\n\n", Usage)
	b.WriteString("The justification is mandatory, must be non-empty, and must be wrapped in double quotes. Example:\n\n")
	b.WriteString("```\n/reeve breakglass \"prod is down, hotfixing the LB target group\" apply\n```\n")
	return b.String()
}
