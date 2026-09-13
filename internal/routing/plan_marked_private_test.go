package routing

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

func uplinkExit(id int) ResolvedExit {
	return ResolvedExit{ID: id, Label: "uplink", Kind: core.ExitDevice, Handle: core.ExitHandle{Device: "pux" + strconv.Itoa(id), Source: core.SourceOwnerDaemon}}
}

/*
A bridged SSH forward reaches the router as a SOCKS request naming a host, and
the routing guard matches addresses only. The shipped direct outbound resolves a
name before its own private block; the marked outbound of a device exit must
too, or "localhost:62789" routed through an uplink lands on this host's Xray API.
*/
func TestAMarkedOutboundBlocksPrivateDestinationsByName(t *testing.T) {
	got := Plan(Input{
		Subjects: []Subject{internalSubject(1, "a-in")},
		Rules:    []Rule{{ID: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestExit, ExitID: 4}}},
		Exits:    []ResolvedExit{uplinkExit(4)},
	})
	var marked map[string]any
	for _, raw := range got.Outbounds {
		var out struct {
			Protocol string `json:"protocol"`
			Settings map[string]any
			Stream   struct {
				Sockopt map[string]any `json:"sockopt"`
			} `json:"streamSettings"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if out.Protocol == "freedom" && out.Stream.Sockopt["mark"] != nil {
			marked = out.Settings
		}
	}
	if marked == nil {
		t.Fatalf("no marked freedom outbound in %s", got.Outbounds)
	}
	rules, _ := marked["finalRules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("finalRules = %v; want a private block then allow", marked["finalRules"])
	}
	block, _ := rules[0].(map[string]any)
	ips, _ := block["ip"].([]any)
	if block["action"] != "block" || len(ips) != 1 || ips[0] != "geoip:private" {
		t.Fatalf("first finalRule = %v; want block geoip:private", rules[0])
	}
	if allow, _ := rules[1].(map[string]any); allow["action"] != "allow" {
		t.Fatalf("second finalRule = %v; want allow", rules[1])
	}
}
