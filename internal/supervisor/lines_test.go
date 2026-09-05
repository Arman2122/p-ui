package supervisor

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// The ring is what an operator reads after a daemon died under exec, so what
// reaches it matters: a line split across two writes is one line, a last line
// with no newline is kept at Flush, and only the newest limit lines remain.
func TestLineRingKeepsWholeStampedLines(t *testing.T) {
	stamp := time.Date(2026, 9, 5, 18, 0, 0, 0, time.UTC)
	r := newLineRing(3)
	r.now = func() time.Time { return stamp }
	for _, chunk := range []string{"first\nsec", "ond\r\n\n", "third\nfourth\nlast words"} {
		if _, err := r.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	const at = "2026-09-05T18:00:00+0000 "
	if got := r.tail(0); !slices.Equal(got, []string{at + "second", at + "third", at + "fourth"}) {
		t.Fatalf("tail before flush = %q", got)
	}
	r.Flush()
	want := []string{at + "third", at + "fourth", at + "last words"}
	if got := r.tail(0); !slices.Equal(got, want) {
		t.Fatalf("tail after flush = %q, want %q", got, want)
	}
	if got := r.tail(1); !slices.Equal(got, want[2:]) {
		t.Fatalf("tail(1) = %q", got)
	}
	if _, err := r.Write([]byte(strings.Repeat("x", ringLineMax+10) + "\n")); err != nil {
		t.Fatal(err)
	}
	if got := r.tail(1)[0]; len(got) != len(at)+ringLineMax+len("…") || !strings.HasSuffix(got, "x…") {
		t.Fatalf("a %d-byte line was kept at %d bytes; want it cut to %d and marked", ringLineMax+10, len(got)-len(at), ringLineMax)
	}
}
