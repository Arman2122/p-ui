package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An ad tag is stored per (client, inbound), but clearing one used to delete every
row the client had, keyed on client_id alone. Two callers hit that: a
single-inbound update (PUT /panel/api/clients/:email?inboundIds=…), and any
update of a client whose form could not show the field at all — a client on no
inbound, where the tag survives only so re-attaching restores it.
*/
func TestUpdate_ClearingAdTagStaysWithinTheUpdatedInbounds(t *testing.T) {
	t.Run("a single-inbound update leaves the other inbound's tag", func(t *testing.T) {
		setupBulkDB(t)
		svc := &ClientService{}
		inboundSvc := &InboundService{}

		const email = "scoped-adtag@x"
		secretA := model.GenerateFakeTLSSecret("a.example.com")
		secretB := model.GenerateFakeTLSSecret("b.example.com")
		const tagA = "0123456789abcdef0123456789abcdef"
		const tagB = "fedcba9876543210fedcba9876543210"

		onA := []model.Client{{Email: email, Secret: secretA, AdTag: tagA, Enable: true}}
		onB := []model.Client{{Email: email, Secret: secretB, AdTag: tagB, Enable: true}}
		ibA := mkInbound(t, 21401, model.MTProto, clientsSettings(t, onA))
		ibB := mkInbound(t, 21402, model.MTProto, clientsSettings(t, onB))
		if err := svc.SyncInbound(nil, ibA.Id, onA); err != nil {
			t.Fatalf("seed inbound A: %v", err)
		}
		if err := svc.SyncInbound(nil, ibB.Id, onB); err != nil {
			t.Fatalf("seed inbound B: %v", err)
		}

		var rec model.ClientRecord
		if err := database.GetDB().Where("email = ?", email).First(&rec).Error; err != nil {
			t.Fatalf("lookup client row: %v", err)
		}

		cleared := rec.ToClient()
		cleared.Secret = secretA
		cleared.AdTag = ""
		if _, err := svc.Update(inboundSvc, rec.Id, *cleared, ibA.Id); err != nil {
			t.Fatalf("Update(inboundFilter=%d): %v", ibA.Id, err)
		}

		if got := storedCreds(t, rec.Id, ibA.Id)[model.CredentialAdTag]; got != "" {
			t.Errorf("ad tag not cleared on the inbound that was edited: got %q, want %q", got, "")
		}
		if got := storedCreds(t, rec.Id, ibB.Id)[model.CredentialAdTag]; got != tagB {
			t.Errorf("ad tag cleared on an inbound the update never touched: got %q, want %q", got, tagB)
		}
	})

	t.Run("a client on no inbound keeps the tag its detached inbound stored", func(t *testing.T) {
		setupBulkDB(t)
		svc := &ClientService{}
		inboundSvc := &InboundService{}

		const email = "detached-adtag@x"
		secret := model.GenerateFakeTLSSecret("gone.example.com")
		const tag = "abcdefabcdefabcdefabcdefabcdefab"

		source := []model.Client{{Email: email, Secret: secret, AdTag: tag, Enable: true}}
		ib := mkInbound(t, 21403, model.MTProto, clientsSettings(t, source))
		if err := svc.SyncInbound(nil, ib.Id, source); err != nil {
			t.Fatalf("seed linkage: %v", err)
		}
		var rec model.ClientRecord
		if err := database.GetDB().Where("email = ?", email).First(&rec).Error; err != nil {
			t.Fatalf("lookup client row: %v", err)
		}
		if err := svc.SyncInbound(nil, ib.Id, nil); err != nil {
			t.Fatalf("detach from inbound: %v", err)
		}

		renamed := rec.ToClient()
		renamed.Comment = "edited while attached to nothing"
		if _, err := svc.Update(inboundSvc, rec.Id, *renamed); err != nil {
			t.Fatalf("Update: %v", err)
		}

		stored, err := storedCredentialsFor(nil, rec.Id, ib.Id)
		if err != nil {
			t.Fatalf("storedCredentialsFor(client=%d, inbound=%d): %v", rec.Id, ib.Id, err)
		}
		if stored[model.CredentialAdTag] != tag {
			t.Errorf("ad tag destroyed by an update that could not show the field: got %q, want %q",
				stored[model.CredentialAdTag], tag)
		}
	})
}
