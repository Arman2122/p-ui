package sub

import (
	"strings"
	"testing"
)

// The subscription page embeds the base path the same way the panel does, with
// its own copy of the same escaper and the same three identity pairs. F210.
func TestSubPageBasePathCannotCloseTheScriptTag(t *testing.T) {
	out := jsStringEscape(`/s</script><script>alert(1)</script>&x=1`)
	for _, forbidden := range []string{"<", ">", "&"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("escaped = %q, still carries %q: a base path can close the inline script tag", out, forbidden)
		}
	}
	for _, want := range []string{"u003c", "u003e", "u0026"} {
		if !strings.Contains(out, want) {
			t.Fatalf("escaped = %q, want a JS unicode escape (%s) so the browser still sees the original character", out, want)
		}
	}
	backslash := string(rune(92))
	if got := jsStringEscape("a" + backslash + "b"); got != "a"+backslash+backslash+"b" {
		t.Fatalf("escaped = %q, want the backslash doubled", got)
	}
}
