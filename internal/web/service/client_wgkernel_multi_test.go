package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A WireGuard client on two kernel-WG inbounds must stay editable.

Each inbound gives such a client its own tunnel address and its own keypair, but
the client RECORD carries one column for each, stamped by whichever inbound
synced last. The editor reads the record and echoes every field back, so a save
sent one inbound's address and keys to both — and the far inbound refused it:

	wireguard: allowedIPs entry already used by another client: 10.0.0.2/32

The other client was the same client's own sibling copy. Renaming it, changing
its quota, editing its comment: all refused, on a client that is working. Worse,
the fan-out writes inbound by inbound, so the first could already be saved when
the second failed.

So an unchanged WireGuard credential is no longer treated as an edit — and the
keys are blanked with the address, deliberately. Blanking only allowedIPs stops
the refusal and starts rewriting one inbound's peer keypair to the other's,
which every .conf handed out for that inbound would silently stop
authenticating against. A loud refusal beats that. F175.
*/
func TestAWgkernelClientOnTwoInboundsStaysEditable(t *testing.T) {
	setupBulkDB(t)
	inboundSvc := &InboundService{}
	clientSvc := &ClientService{}

	first, _, _, err := inboundSvc.AddInbound(wgkernelInbound(51971, "10.0.0.1/24"))
	if err != nil {
		t.Fatalf("first AddInbound: %v", err)
	}
	second, _, _, err := inboundSvc.AddInbound(wgkernelInbound(51972, "10.9.0.1/24"))
	if err != nil {
		t.Fatalf("second AddInbound: %v", err)
	}

	if _, err := clientSvc.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "dual@wgk", SubID: "sub-dual-wgk", Enable: true},
		InboundIds: []int{first.Id, second.Id},
	}); err != nil {
		t.Fatalf("create the client on both inbounds: %v", err)
	}

	// The state this is about is only reachable if the two copies really differ.
	addrA, keyA := storedWgPeer(t, inboundSvc, first.Id, "dual@wgk")
	addrB, keyB := storedWgPeer(t, inboundSvc, second.Id, "dual@wgk")
	if addrA == "" || addrB == "" {
		t.Fatalf("a copy has no address: %q / %q", addrA, addrB)
	}
	if addrA == addrB {
		t.Fatalf("both inbounds gave the client the same address %q; this test cannot see the bug", addrA)
	}

	rec, err := clientSvc.GetRecordByEmail(nil, "dual@wgk")
	if err != nil {
		t.Fatalf("read the client record: %v", err)
	}

	// Exactly what the editor sends: the record's own values, echoed back, with
	// one unrelated field changed.
	edit := *rec.ToClient()
	edit.Comment = "renamed laptop"
	if _, err := clientSvc.UpdateByEmail(inboundSvc, "dual@wgk", edit); err != nil {
		t.Fatalf("a metadata-only edit was refused on a client attached to two kernel-WG inbounds: %v", err)
	}

	// And each inbound kept its OWN peer, rather than taking the record's.
	gotAddrA, gotKeyA := storedWgPeer(t, inboundSvc, first.Id, "dual@wgk")
	gotAddrB, gotKeyB := storedWgPeer(t, inboundSvc, second.Id, "dual@wgk")
	if gotAddrA != addrA || gotAddrB != addrB {
		t.Errorf("addresses moved: %q->%q and %q->%q", addrA, gotAddrA, addrB, gotAddrB)
	}
	if gotKeyA != keyA || gotKeyB != keyB {
		t.Errorf("peer keys were rewritten by a metadata edit; every config handed out for that inbound stops authenticating.\n A: %q -> %q\n B: %q -> %q",
			keyA, gotKeyA, keyB, gotKeyB)
	}
	if gotKeyA == gotKeyB {
		t.Errorf("both inbounds now hold the same peer key %q; one of them was overwritten with the other's", gotKeyA)
	}
}

// storedWgPeer reads one inbound's own copy of a client: its tunnel address and
// its public key, which are per inbound even though the record holds one of each.
func storedWgPeer(t *testing.T, inboundSvc *InboundService, inboundID int, email string) (string, string) {
	t.Helper()
	ib, err := inboundSvc.GetInbound(inboundID)
	if err != nil {
		t.Fatalf("read inbound %d: %v", inboundID, err)
	}
	clients, err := inboundSvc.GetClients(ib)
	if err != nil {
		t.Fatalf("read clients of inbound %d: %v", inboundID, err)
	}
	for _, c := range clients {
		if c.Email == email {
			return strings.Join(c.AllowedIPs, ","), c.PublicKey
		}
	}
	t.Fatalf("client %q is not on inbound %d", email, inboundID)
	return "", ""
}
