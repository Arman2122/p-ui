package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// storedCreds reads a client's credentials for one inbound straight from the table.
func storedCreds(t *testing.T, clientId, inboundId int) map[string]string {
	t.Helper()
	rows, err := readClientCredentials(nil, []int{clientId}, inboundId)
	if err != nil {
		t.Fatalf("read client_credentials(client=%d, inbound=%d): %v", clientId, inboundId, err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out
}

func TestSyncInbound_UpdatesMtprotoSecretAndAdTag(t *testing.T) {
	initTestDB(t)

	db := database.GetDB()

	mtproto := &model.Inbound{Tag: "mtproto-in", Enable: true, Port: 10004, Protocol: model.MTProto}
	if err := db.Create(mtproto).Error; err != nil {
		t.Fatalf("create mtproto inbound: %v", err)
	}

	svc := ClientService{}
	const email = "tg@example.com"
	const firstSecret = "ee0123456789abcdef0123456789abcdef6578616d706c652e636f6d"
	const rekeyedSecret = "eefedcba9876543210fedcba98765432106578616d706c652e636f6d"
	const firstTag = "0123456789abcdef0123456789abcdef"
	const retaggedTag = "fedcba9876543210fedcba9876543210"

	first := model.Client{Email: email, Secret: firstSecret, AdTag: firstTag, Enable: true}
	if err := svc.SyncInbound(nil, mtproto.Id, []model.Client{first}); err != nil {
		t.Fatalf("SyncInbound (create): %v", err)
	}

	var row model.ClientRecord
	if err := db.Where("email = ?", email).First(&row).Error; err != nil {
		t.Fatalf("lookup client row: %v", err)
	}
	got := storedCreds(t, row.Id, mtproto.Id)
	if got[model.CredentialSecret] != firstSecret || got[model.CredentialAdTag] != firstTag {
		t.Fatalf("create must store secret and ad tag: got %v", got)
	}

	rekeyed := model.Client{Email: email, Secret: rekeyedSecret, AdTag: retaggedTag, Enable: true}
	if err := svc.SyncInbound(nil, mtproto.Id, []model.Client{rekeyed}); err != nil {
		t.Fatalf("SyncInbound (rekey): %v", err)
	}
	got = storedCreds(t, row.Id, mtproto.Id)
	if got[model.CredentialSecret] != rekeyedSecret {
		t.Errorf("a re-keyed secret must reach client_credentials (sub links and the clients page read it), got %q", got[model.CredentialSecret])
	}
	if got[model.CredentialAdTag] != retaggedTag {
		t.Errorf("a changed ad tag must reach client_credentials, got %q", got[model.CredentialAdTag])
	}

	secretless := model.Client{Email: email, Enable: true}
	if err := svc.SyncInbound(nil, mtproto.Id, []model.Client{secretless}); err != nil {
		t.Fatalf("SyncInbound (secretless): %v", err)
	}
	got = storedCreds(t, row.Id, mtproto.Id)
	if got[model.CredentialSecret] != rekeyedSecret || got[model.CredentialAdTag] != retaggedTag {
		t.Errorf("a payload without mtproto fields must not wipe them: got %v", got)
	}

	listed, err := svc.ListForInbound(nil, mtproto.Id)
	if err != nil {
		t.Fatalf("ListForInbound: %v", err)
	}
	if len(listed) != 1 || listed[0].Secret != rekeyedSecret || listed[0].AdTag != retaggedTag {
		t.Errorf("ListForInbound must re-attach the stored credentials, got %+v", listed)
	}
}

// TestSyncInbound_SecretIsPerInbound is why the table is keyed by inbound:
// GenerateFakeTLSSecret embeds the inbound's FakeTLS domain in the secret.
func TestSyncInbound_SecretIsPerInbound(t *testing.T) {
	initTestDB(t)

	db := database.GetDB()
	svc := ClientService{}
	const email = "dual@example.com"
	secrets := map[string]string{
		"mtproto-a": model.GenerateFakeTLSSecret("a.example.com"),
		"mtproto-b": model.GenerateFakeTLSSecret("b.example.com"),
	}
	if secrets["mtproto-a"] == secrets["mtproto-b"] {
		t.Fatal("test setup is vacuous: both domains produced the same secret")
	}

	ids := map[string]int{}
	for i, tag := range []string{"mtproto-a", "mtproto-b"} {
		ib := &model.Inbound{Tag: tag, Enable: true, Port: 10010 + i, Protocol: model.MTProto}
		if err := db.Create(ib).Error; err != nil {
			t.Fatalf("create inbound %s: %v", tag, err)
		}
		ids[tag] = ib.Id
		client := model.Client{Email: email, Secret: secrets[tag], Enable: true}
		if err := svc.SyncInbound(nil, ib.Id, []model.Client{client}); err != nil {
			t.Fatalf("SyncInbound(%s): %v", tag, err)
		}
	}

	for tag, want := range secrets {
		listed, err := svc.ListForInbound(nil, ids[tag])
		if err != nil {
			t.Fatalf("ListForInbound(%s): %v", tag, err)
		}
		if len(listed) != 1 {
			t.Fatalf("ListForInbound(%s) returned %d clients, want 1", tag, len(listed))
		}
		if listed[0].Secret != want {
			t.Errorf("inbound %s served secret %q, want its own %q", tag, listed[0].Secret, want)
		}
	}

	// The credential a detached inbound leaves behind must not win the fallback
	// that keeps a credential-less update from rotating the live secret.
	if err := svc.SyncInbound(nil, ids["mtproto-a"], nil); err != nil {
		t.Fatalf("SyncInbound(detach mtproto-a): %v", err)
	}
	var rec model.ClientRecord
	if err := db.Where("email = ?", email).First(&rec).Error; err != nil {
		t.Fatalf("lookup client row: %v", err)
	}
	stored, err := storedClientCredentials(nil, rec.Id)
	if err != nil {
		t.Fatalf("storedClientCredentials: %v", err)
	}
	if stored[model.CredentialSecret] != secrets["mtproto-b"] {
		t.Errorf("fallback served %q, want the still-attached inbound's %q",
			stored[model.CredentialSecret], secrets["mtproto-b"])
	}
}
