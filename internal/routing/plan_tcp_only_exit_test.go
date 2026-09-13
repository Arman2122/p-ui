package routing

import (
	"encoding/json"
	"testing"
)

func tcpOnlyExit(id int) ResolvedExit {
	exit := socksExit(id)
	exit.Label = "ssh-forward"
	exit.Handle.TCPOnly = true
	return exit
}

/*
An SSH forward carries TCP only. A rule routing "everything" to it is pinned to
TCP and warned, and the UDP it matched is dropped by a companion rule right
behind it: left to fall through, that UDP took the next rule or the default
route and left with the server's own address, which is the leak an exit rule
exists to prevent. UDP alone is refused; a rule that said tcp is untouched.
*/
func TestTCPOnlyExitNarrowsRulesToTCP(t *testing.T) {
	for _, tt := range []struct {
		name      string
		criteria  map[string]json.RawMessage
		wantRules int
		wantSev   string
		wantKey   string
	}{
		{"no network", nil, 2, SeverityWarn, KeyExitTCPNarrowed},
		{"tcp only", map[string]json.RawMessage{"network": json.RawMessage(`"tcp"`)}, 1, SeverityOK, ""},
		{"both", map[string]json.RawMessage{"network": json.RawMessage(`"tcp,udp"`)}, 2, SeverityWarn, KeyExitTCPNarrowed},
		{"udp alone", map[string]json.RawMessage{"network": json.RawMessage(`"udp"`)}, 0, SeverityInert, KeyExitTCPOnly},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := Plan(Input{
				Subjects:  []Subject{internalSubject(1, "a-in")},
				Rules:     []Rule{{ID: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestExit, ExitID: 4}, Criteria: tt.criteria}},
				Exits:     []ResolvedExit{tcpOnlyExit(4)},
				Blackhole: "blocked",
			})
			if len(got.XrayRules) != tt.wantRules {
				t.Fatalf("rules = %d, want %d", len(got.XrayRules), tt.wantRules)
			}
			if tt.wantRules > 0 {
				first := decode(t, got.XrayRules[0])
				if first["network"] != "tcp" || first["outboundTag"] == "blocked" {
					t.Fatalf("first rule = %v; want TCP toward the exit", first)
				}
			}
			if tt.wantRules == 2 {
				second := decode(t, got.XrayRules[1])
				if second["network"] != "udp" || second["outboundTag"] != "blocked" || second["inboundTag"].([]any)[0] != "a-in" {
					t.Fatalf("companion = %v; want the same inbound's UDP dropped", second)
				}
			}
			if len(got.Diags) != 1 || got.Diags[0].Severity != tt.wantSev || got.Diags[0].MessageKey != tt.wantKey {
				t.Fatalf("diags = %+v, want one with severity %s and key %q", got.Diags, tt.wantSev, tt.wantKey)
			}
		})
	}
}
