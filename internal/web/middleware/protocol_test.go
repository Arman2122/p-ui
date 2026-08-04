package middleware

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The rule is registered in init() and read through a struct tag, so nothing else
in the suite would notice it silently accepting everything or nothing.

It drives model.Inbound rather than a local struct: the tag under test is the one
the panel ships, and a `oneof=` creeping back would pass a local copy.
*/
func TestProtocolRuleAcceptsExactlyWhatACoreServes(t *testing.T) {
	kinds := cores.Kinds()
	if len(kinds) == 0 {
		t.Fatal("no kind is registered; this test would certify nothing")
	}
	for _, kind := range kinds {
		ib := model.Inbound{Protocol: model.Protocol(kind), Port: 443, SubSortIndex: 1}
		if err := validate.StructPartial(&ib, "Protocol"); err != nil {
			t.Errorf("protocol %q is served by a core but validation rejected it: %v", kind, err)
		}
	}

	for _, rejected := range []string{
		"",        // required still applies
		"openvpn", // a core that is not ported yet
		"VLESS",   // the wire value is lower-case
		"vless ",  // no trimming
		"vless,vmess",
	} {
		ib := model.Inbound{Protocol: model.Protocol(rejected), Port: 443, SubSortIndex: 1}
		if err := validate.StructPartial(&ib, "Protocol"); err == nil {
			t.Errorf("protocol %q was accepted; no core serves it, so the panel would store an inbound it can never apply", rejected)
		}
	}
}
