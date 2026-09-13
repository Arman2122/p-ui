package singbox

import (
	"bytes"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

// AddUser appends the edited user; the next supervise pass renders the row's
// order. Different bytes, different hash, a second reload ten seconds later.
func TestRenderConfigDoesNotDependOnUserOrder(t *testing.T) {
	settings, err := parseSettings(KindTUIC, `{"certFile":"/c","keyFile":"/k"}`)
	if err != nil {
		t.Fatal(err)
	}
	ctl := control{statsPort: 1, apiPort: 2, secret: "s"}
	first := core.Instance{ID: 1, Kind: KindTUIC, Port: 443, Users: []core.User{userN(KindTUIC, 1), userN(KindTUIC, 2), userN(KindTUIC, 3)}}
	second := first
	second.Users = []core.User{userN(KindTUIC, 3), userN(KindTUIC, 1), userN(KindTUIC, 2)}
	a, err := renderConfig(first, settings, ctl, "/c", "/k")
	if err != nil {
		t.Fatal(err)
	}
	b, err := renderConfig(second, settings, ctl, "/c", "/k")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("the same users in another order render differently:\n%s\n---\n%s", a, b)
	}
}
