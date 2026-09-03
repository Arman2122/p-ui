package cores

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
The subscription decides between a file URL and a bare link by asking
SharesFile, which took every LinkRenderer for a file: a tuic:// link is the
whole share, and the page fetched a file that did not exist for it.
*/
func TestSharesFileHonoursTheDeclarer(t *testing.T) {
	for kind, want := range map[core.Kind]bool{"ssh": true, "tuic": false, "anytls": false, "vless": false} {
		if got := SharesFile(kind); got != want {
			t.Errorf("SharesFile(%s) = %t, want %t", kind, got, want)
		}
	}
}
