package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An AmneziaWG client must carry exactly what a kernel WireGuard one does.

The credential vocabulary is what the panel mints, stores and renders for a
client. A kind missing from it gets a client row with no keypair, so the
subscription has nothing to render and the device has no peer to add -- and
neither reports anything, because an absent credential is indistinguishable
from one the operator has not filled in yet.
*/
func TestAmneziawgClientsCarryWireguardCredentials(t *testing.T) {
	wg := cores.ClientCredentials("wgkernel")
	awg := cores.ClientCredentials("awgkernel")

	if len(awg) == 0 {
		t.Fatal("awgkernel declares no client credentials at all; every client of it would be keyless")
	}
	if len(wg) != len(awg) {
		t.Fatalf("awgkernel credentials %v differ from wgkernel's %v; they are the same client", awg, wg)
	}
	for _, want := range wg {
		found := false
		for _, got := range awg {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("awgkernel is missing the %q credential wgkernel has", want)
		}
	}
	// The one the whole tunnel depends on.
	if !clientCarriesWireguardKeys(model.AWGKernel) {
		t.Error("awgkernel is not recognised as carrying WireGuard keys, so every keyed path skips it")
	}
}

/*
Minting must behave exactly as it does for kernel WireGuard, which means
answering with nothing.

A WireGuard keypair is generated where the private key can stay -- on the client,
or in the browser adding it -- and the panel never learns one it did not make. So
an empty answer is correct, and what is worth pinning is that AmneziaWG gives the
SAME answer: a kind minting something extra, or erroring where the other does
not, has diverged from the client model both share.
*/
func TestAmneziawgMintsExactlyWhatWireguardDoes(t *testing.T) {
	for _, kind := range []core.Kind{"wgkernel", "awgkernel"} {
		authority, ok := cores.ClientCredentialAuthority(kind)
		if !ok {
			t.Fatalf("no core answers for %s credentials", kind)
		}
		minted, err := authority.MintClientCredentials(kind, "", map[string]string{})
		if err != nil {
			t.Fatalf("%s MintClientCredentials: %v", kind, err)
		}
		if len(minted) != 0 {
			t.Errorf("%s minted %v; a WireGuard keypair is generated client-side", kind, minted)
		}
	}
}

// A client with no public key must be refused for both kinds: stored anyway, it
// is a peer the device cannot serve and the tunnel silently has one fewer client.
func TestAmneziawgRefusesAKeylessClient(t *testing.T) {
	for _, kind := range []core.Kind{"wgkernel", "awgkernel"} {
		authority, ok := cores.ClientCredentialAuthority(kind)
		if !ok {
			t.Fatalf("no core answers for %s credentials", kind)
		}
		if err := authority.ValidateClient(kind, "", "", map[string]string{}); err == nil {
			t.Errorf("%s accepted a client with no key", kind)
		}
		if err := authority.ValidateClient(kind, "", "", map[string]string{core.CredPublicKey: "k"}); err != nil {
			t.Errorf("%s refused a client that has a key: %v", kind, err)
		}
	}
}
