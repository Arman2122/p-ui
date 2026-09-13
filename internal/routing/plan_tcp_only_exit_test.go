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
An SSH forward carries TCP only. Nothing said so, so a rule routing "everything"
to it would have sent UDP into a SOCKS port that drops it, and the route looked
healthy. The exit now declares TCPOnly and the compiler narrows: a rule with no network
or asking for both is pinned to TCP and warned — the UDP it covered takes the
next rule, which the operator must hear — and UDP alone is refused.
*/
func TestTCPOnlyExitNarrowsRulesToTCP(t *testing.T) {
	for _, tt := range []struct {
		name        string
		criteria    map[string]json.RawMessage
		wantNetwork string
		wantRule    bool
		wantSev     string
		wantKey     string
	}{
		{"no network", nil, "tcp", true, SeverityWarn, KeyExitTCPNarrowed},
		{"tcp only", map[string]json.RawMessage{"network": json.RawMessage(`"tcp"`)}, "tcp", true, SeverityOK, ""},
		{"both", map[string]json.RawMessage{"network": json.RawMessage(`"tcp,udp"`)}, "tcp", true, SeverityWarn, KeyExitTCPNarrowed},
		{"udp alone", map[string]json.RawMessage{"network": json.RawMessage(`"udp"`)}, "", false, SeverityInert, KeyExitTCPOnly},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := Plan(Input{
				Subjects: []Subject{internalSubject(1, "a-in")},
				Rules:    []Rule{{ID: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestExit, ExitID: 4}, Criteria: tt.criteria}},
				Exits:    []ResolvedExit{tcpOnlyExit(4)},
			})
			if len(got.XrayRules) != map[bool]int{true: 1, false: 0}[tt.wantRule] {
				t.Fatalf("rules = %d, want %v", len(got.XrayRules), tt.wantRule)
			}
			if tt.wantRule {
				if network, _ := decode(t, got.XrayRules[0])["network"].(string); network != tt.wantNetwork {
					t.Fatalf("network = %q, want %q", network, tt.wantNetwork)
				}
			}
			if len(got.Diags) != 1 || got.Diags[0].Severity != tt.wantSev || got.Diags[0].MessageKey != tt.wantKey {
				t.Fatalf("diags = %+v, want one with severity %s and key %q", got.Diags, tt.wantSev, tt.wantKey)
			}
		})
	}
}
