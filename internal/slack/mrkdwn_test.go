package slack

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEscape(t *testing.T) {
	got := Escape(`fix <script> & "tags" > now`)
	want := `fix &lt;script&gt; &amp; "tags" &gt; now`
	if got != want {
		t.Fatalf("Escape: %q", got)
	}
	// A crafted mention must not survive as markup.
	if out := Escape("<!channel>"); strings.ContainsAny(out, "<>") {
		t.Fatalf("mention not neutralized: %q", out)
	}
}

func TestFenceSafe(t *testing.T) {
	out := FenceSafe("before ``` after")
	if strings.Contains(out, "```") {
		t.Fatalf("fence run survived: %q", out)
	}
	// Non-fence backticks pass through untouched.
	if FenceSafe("a `code` span") != "a `code` span" {
		t.Fatal("single backticks must be preserved")
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	s := "héllo wörld" // multibyte at various offsets
	for max := 0; max <= len(s); max++ {
		got := Truncate(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("max=%d produced invalid UTF-8: %q", max, got)
		}
	}
	if Truncate("short", 100) != "short" {
		t.Fatal("no-op truncation changed the string")
	}
	// Cutting inside the é (bytes 1-2) must back up to byte 1, not split it.
	if got := Truncate("héllo", 2); got != "h…" {
		t.Fatalf("mid-rune cut: %q", got)
	}
	if got := Truncate("hello world", 5); got != "hello…" {
		t.Fatalf("ascii cut: %q", got)
	}
}
