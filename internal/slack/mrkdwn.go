package slack

import (
	"strings"
	"unicode/utf8"
)

// Escape sanitizes text interpolated into mrkdwn per Slack's escaping rules
// (https://api.slack.com/reference/surfaces/formatting#escaping): only
// &, <, > are control characters. Apply to any externally-controlled string
// (PR titles, error messages) before it lands in a mrkdwn field, or a title
// like "<!channel>" would ping the workspace.
func Escape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// FenceSafe makes s safe to embed inside a ``` code fence: any ``` run in
// the payload would terminate the fence early and let the remainder render
// as markup. A zero-width space is inserted between the backticks - visually
// identical, but no longer a fence terminator.
func FenceSafe(s string) string {
	return strings.ReplaceAll(s, "```", "`\u200b`\u200b`")
}

// Truncate caps s at max bytes without splitting a UTF-8 rune, appending an
// ellipsis when anything was cut.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 0 {
		max = 0
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
