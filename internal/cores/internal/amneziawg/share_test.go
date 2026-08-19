package amneziawg

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/core"
)

func inbound(t *testing.T, params awg.Params) core.Instance {
	t.Helper()
	settings, err := json.Marshal(map[string]any{
		"secretKey": "QFn9tYyPqSMYPO1jN0OFHqjJnJRvJhLj0kZ7Cw+CVWM=",
		"dns":       "9.9.9.9",
		"awg":       params,
	})
	if err != nil {
		t.Fatal(err)
	}
	return core.Instance{ID: 3, Kind: Kind, Tag: "awg-in", Port: 51820, Settings: string(settings)}
}

func client() core.User {
	return core.User{
		Email: "alice@example.com",
		Credentials: map[string]any{
			core.CredPrivateKey: "clientPrivateKey=",
			core.CredAllowedIPs: []any{"10.8.0.4/32"},
		},
	}
}

/*
The obfuscation must land inside [Interface].

A key written after [Peer] belongs to the peer, so the client either rejects the
file or quietly ignores the parameter -- and ignoring it is the failure that
looks like nothing at all: the tunnel handshakes against a server using
different junk and carries no traffic, with no error on either side.
*/
func TestParametersLandInTheInterfaceSection(t *testing.T) {
	share, err := New().RenderClient(inbound(t, awg.Params{
		Jc: 4, Jmin: 40, Jmax: 70,
		H1: awg.HeaderRange(10, 19), H2: awg.HeaderRange(20, 20),
		I1: "b0xdeadbeef", RandomTrailers: true,
		RekeyAfterTime: awg.TimerRange(100, 140),
	}), client(), "vpn.example.com")
	if err != nil {
		t.Fatalf("RenderClient: %v", err)
	}

	body := share.Body
	peerAt := strings.Index(body, "[Peer]")
	if peerAt < 0 {
		t.Fatalf("no [Peer] section:\n%s", body)
	}
	iface := body[:peerAt]

	for _, want := range []string{
		"Jc = 4", "Jmin = 40", "Jmax = 70",
		// A range prints as lo-hi, and collapses to a bare number when the
		// bounds match -- which awg-tools parses back as [n, n].
		"H1 = 10-19", "H2 = 20",
		"I1 = b0xdeadbeef", "RandomTrailers = on",
		"RekeyAfterTime = 100-140",
	} {
		if !strings.Contains(iface, want) {
			t.Errorf("%q is not in [Interface]:\n%s", want, iface)
		}
	}
}

// An inbound carrying no obfuscation renders the plain WireGuard config, since
// AmneziaWG with every parameter unset IS WireGuard.
func TestNoObfuscationRendersAPlainConfig(t *testing.T) {
	share, err := New().RenderClient(inbound(t, awg.Params{}), client(), "vpn.example.com")
	if err != nil {
		t.Fatalf("RenderClient: %v", err)
	}
	for _, absent := range []string{"Jc =", "H1 =", "RandomTrailers"} {
		if strings.Contains(share.Body, absent) {
			t.Errorf("%q was written for an inbound with no obfuscation:\n%s", absent, share.Body)
		}
	}
	if !strings.Contains(share.Body, "[Interface]") || !strings.Contains(share.Body, "[Peer]") {
		t.Errorf("the plain config is malformed:\n%s", share.Body)
	}
}

// Unreadable settings must refuse rather than hand out a config missing the
// obfuscation, which would connect to nothing.
func TestUnreadableSettingsRefuse(t *testing.T) {
	_, err := ParamsOf(core.Instance{ID: 3, Settings: "{not json"})
	if err == nil {
		t.Fatal("unreadable settings must be an error, not an empty parameter set")
	}
}
