package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A traffic reset re-enables a disabled client by round-tripping its own record
through Update. Once secret and adTag left the clients row, ToClient stopped
carrying them, so that synthesised payload arrived with both fields empty —
indistinguishable from an admin clearing the field, and the ad tag was destroyed
in the credentials table AND in the settings blob mtg reads.

Both halves are asserted: fixing only the table would still lose the blob, and a
client whose blob lost its adTag is one whose sponsored channel silently stops
paying out.
*/
func TestTrafficResetKeepsMtprotoCredentials(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}

	const email = "adtag@x"
	secret := model.GenerateFakeTLSSecret("keep.example.com")
	const adTag = "0123456789abcdef0123456789abcdef"

	source := []model.Client{{Email: email, Secret: secret, AdTag: adTag, Enable: true}}
	ib := mkInbound(t, 21301, model.MTProto, clientsSettings(t, source))
	if err := svc.SyncInbound(nil, ib.Id, source); err != nil {
		t.Fatalf("seed linkage: %v", err)
	}

	var rec model.ClientRecord
	if err := database.GetDB().Where("email = ?", email).First(&rec).Error; err != nil {
		t.Fatalf("lookup client row: %v", err)
	}
	// Disabled is the branch that reaches Update; an enabled client never does.
	if err := database.GetDB().Model(&model.ClientRecord{}).
		Where("id = ?", rec.Id).UpdateColumn("enable", false).Error; err != nil {
		t.Fatalf("disable client: %v", err)
	}
	rec.Enable = false
	mkTraffic(t, ib.Id, email, 10, 20, 0, 0, false)

	for _, tt := range []struct {
		name  string
		reset func() error
	}{
		{"ResetTrafficByEmail", func() error {
			_, err := svc.ResetTrafficByEmail(inboundSvc, email)
			return err
		}},
		{"BulkResetTraffic", func() error {
			_, err := svc.BulkResetTraffic(inboundSvc, []string{email})
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := database.GetDB().Model(&model.ClientRecord{}).
				Where("id = ?", rec.Id).UpdateColumn("enable", false).Error; err != nil {
				t.Fatalf("re-disable client: %v", err)
			}
			if err := tt.reset(); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}

			stored := storedCreds(t, rec.Id, ib.Id)
			if stored[model.CredentialAdTag] != adTag {
				t.Errorf("ad tag destroyed in client_credentials: got %q, want %q",
					stored[model.CredentialAdTag], adTag)
			}
			if stored[model.CredentialSecret] != secret {
				t.Errorf("secret destroyed in client_credentials: got %q, want %q",
					stored[model.CredentialSecret], secret)
			}

			if got := blobAdTag(t, ib.Id, email); got != adTag {
				t.Errorf("ad tag destroyed in the inbound settings blob mtg reads: got %q, want %q", got, adTag)
			}
		})
	}
}

// blobAdTag reads one client's adTag out of an inbound's stored settings JSON.
func blobAdTag(t *testing.T, inboundId int, email string) string {
	t.Helper()
	var ib model.Inbound
	if err := database.GetDB().Where("id = ?", inboundId).First(&ib).Error; err != nil {
		t.Fatalf("load inbound %d: %v", inboundId, err)
	}
	var parsed struct {
		Clients []model.Client `json:"clients"`
	}
	if err := json.Unmarshal([]byte(ib.Settings), &parsed); err != nil {
		t.Fatalf("parse inbound settings: %v", err)
	}
	for _, c := range parsed.Clients {
		if c.Email == email {
			return c.AdTag
		}
	}
	t.Fatalf("client %s missing from the inbound settings blob", email)
	return ""
}
