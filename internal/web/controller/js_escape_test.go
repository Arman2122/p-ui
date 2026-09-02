package controller

import (
	"strings"
	"testing"
)

/*
The base path is written into a JS string literal inside an inline <script>.

Three of the escaper's seven pairs were identity mappings -- `<` to `<`, `>` to
`>`, `&` to `&` -- so a base path carrying `</script>` closed the tag and what
followed ran as markup. The panel's own setting is admin-controlled, which is
why this sat at tier 3, but an escaper that names a character and then leaves
it alone is a claim the code does not keep. F210.
*/
func TestBasePathCannotCloseTheScriptTag(t *testing.T) {
	out := jsStringEscape(`/p</script><script>alert(1)</script>&x=1`)
	for _, forbidden := range []string{"<", ">", "&"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("escaped = %q, still carries %q: a base path can close the inline script tag", out, forbidden)
		}
	}
	// The JS-literal escapes, not HTML entities: this string is read by the JS
	// parser, where an entity is ordinary characters and `<` is `<`.
	for _, want := range []string{"u003c", "u003e", "u0026"} {
		if !strings.Contains(out, want) {
			t.Fatalf("escaped = %q, want a JS unicode escape (%s) so the browser still sees the original character", out, want)
		}
	}
}

// The pairs that were already right stay right. The characters are built from
// their code points so this file carries no backslash literal of its own.
func TestJSStringEscapeKeepsBackslashAndQuoteEscaped(t *testing.T) {
	backslash, quote := string(rune(92)), string(rune(34))
	if got := jsStringEscape("a" + backslash + "b"); got != "a"+backslash+backslash+"b" {
		t.Fatalf("escaped = %q, want the backslash doubled", got)
	}
	if got := jsStringEscape("a" + quote + "b"); got != "a"+backslash+quote+"b" {
		t.Fatalf("escaped = %q, want the quote escaped", got)
	}
	if got := jsStringEscape("a\nb"); strings.Contains(got, "\n") {
		t.Fatalf("escaped = %q, want no raw newline inside a JS string literal", got)
	}
}
