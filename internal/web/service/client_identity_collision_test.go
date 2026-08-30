package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Two clients on one inbound cannot share the credential their core keys on.

The panel enforces sibling uniqueness on email, on subId and on WireGuard's
public key -- and on nothing else. Every other core keys its user table on the
client's credential: vless and vmess on the uuid, trojan on the password,
hysteria on its auth string. Two clients carrying the same value are ONE user to
that core.

So the second silently replaces the first in the running config. Both rows exist
in the panel, both appear in the client list, both have quotas -- and all of the
traffic is attributed to whichever one the core kept, while the other is billed
nothing and, when it is deleted, takes the survivor's account down with it.

Guarded on the field each core DECLARES as its identity rather than on a
hardcoded list, so a new core is covered the day it declares one. The comparison
is byte-exact and case-sensitive on purpose: trojan keys on sha224 of the raw
password, so folding case would refuse pairs the core treats as different. F045.
*/

// vlessSettings is clientsSettings plus the decryption key xray requires,
// without which AddInbound is refused by the pre-flight before the guard runs.
func vlessSettings(t *testing.T, clients []model.Client) string {
	t.Helper()
	return strings.Replace(clientsSettings(t, clients), "{", `{
  "decryption": "none",`, 1)
}

func TestAnInboundCannotBeCreatedWithTwoClientsSharingOneIdentity(t *testing.T) {
	setupBulkDB(t)

	const dupe = "aaaaaaaa-1111-4222-8333-444444444444"
	clients := []model.Client{
		{Email: "first@x", ID: dupe, SubID: "sub-a", Enable: true},
		{Email: "second@x", ID: dupe, SubID: "sub-b", Enable: true},
	}
	ib := &model.Inbound{
		Tag: "dupe-create", Enable: true, Listen: "0.0.0.0", Port: 33100,
		Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`,
		Settings: vlessSettings(t, clients),
	}

	_, _, _, err := (&InboundService{}).AddInbound(ib)
	if err == nil {
		t.Fatal("an inbound was created with two clients sharing a uuid; the core keeps one of them and bills it for both")
	}
	if !strings.Contains(err.Error(), "already used by client") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
	if !strings.Contains(err.Error(), "first@x") {
		t.Errorf("the refusal does not name the client already holding the identity: %v", err)
	}
}

func TestAClientCannotBeAddedWithAnIdentityAnotherClientHolds(t *testing.T) {
	setupBulkDB(t)
	inboundSvc := &InboundService{}
	clientSvc := &ClientService{}

	const held = "bbbbbbbb-1111-4222-8333-555555555555"
	ib, _, _, err := inboundSvc.AddInbound(&model.Inbound{
		Tag: "dupe-add", Enable: true, Listen: "0.0.0.0", Port: 33101,
		Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`,
		Settings: vlessSettings(t, []model.Client{{Email: "held@x", ID: held, SubID: "sub-held", Enable: true}}),
	})
	if err != nil {
		t.Fatalf("seed the inbound: %v", err)
	}

	_, err = clientSvc.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "thief@x", ID: held, SubID: "sub-thief", Enable: true},
		InboundIds: []int{ib.Id},
	})
	if err == nil {
		t.Fatal("a second client took a uuid another client on the same inbound already holds; the core serves one user for both")
	}
	if !strings.Contains(err.Error(), "already used by client") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}

// The other half, and it is the one that makes this safe to ship: an edit that
// does NOT change the identity must still go through, or an inbound already
// carrying a duplicate from an older build becomes uneditable.
func TestAMetadataEditIsNotRefusedByTheIdentityGuard(t *testing.T) {
	setupBulkDB(t)
	inboundSvc := &InboundService{}
	clientSvc := &ClientService{}

	ib, _, _, err := inboundSvc.AddInbound(&model.Inbound{
		Tag: "edit-ok", Enable: true, Listen: "0.0.0.0", Port: 33102,
		Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`,
		Settings: vlessSettings(t, []model.Client{
			{Email: "alice@x", ID: "cccccccc-1111-4222-8333-666666666666", SubID: "sub-alice", Enable: true},
			{Email: "bob@x", ID: "dddddddd-1111-4222-8333-777777777777", SubID: "sub-bob", Enable: true},
		}),
	})
	if err != nil {
		t.Fatalf("seed the inbound: %v", err)
	}
	_ = ib

	rec, err := clientSvc.GetRecordByEmail(nil, "alice@x")
	if err != nil {
		t.Fatalf("read alice: %v", err)
	}
	edit := *rec.ToClient()
	edit.Comment = "renamed"

	if _, err := clientSvc.UpdateByEmail(inboundSvc, "alice@x", edit); err != nil {
		t.Fatalf("a metadata-only edit was refused by the identity guard: %v", err)
	}
}
