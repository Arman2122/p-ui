package database

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// addLegacyClientColumns recreates the clients.secret / clients.ad_tag columns a
// shipped release wrote, which this build's model no longer maps.
func addLegacyClientColumns(t *testing.T) {
	t.Helper()
	for _, column := range []string{"secret", "ad_tag"} {
		if err := db.Exec(`ALTER TABLE clients ADD COLUMN IF NOT EXISTS ` + column + ` text DEFAULT ''`).Error; err != nil {
			t.Fatalf("add legacy column %s: %v", column, err)
		}
	}
}

func seedLegacyClient(t *testing.T, email, secret, adTag string, inboundIds ...int) int {
	t.Helper()
	rec := &model.ClientRecord{Email: email, SubID: email, Enable: true}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("create client %s: %v", email, err)
	}
	if err := db.Model(&model.ClientRecord{}).Where("id = ?", rec.Id).
		Updates(map[string]any{"secret": secret, "ad_tag": adTag}).Error; err != nil {
		t.Fatalf("write legacy columns for %s: %v", email, err)
	}
	for _, ibId := range inboundIds {
		if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: ibId}).Error; err != nil {
			t.Fatalf("link %s to inbound %d: %v", email, ibId, err)
		}
	}
	return rec.Id
}

func credentialRows(t *testing.T, clientId int) map[[2]any]string {
	t.Helper()
	var rows []model.ClientCredential
	if err := db.Where("client_id = ?", clientId).
		Order("inbound_id ASC, key ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read client_credentials for client %d: %v", clientId, err)
	}
	out := make(map[[2]any]string, len(rows))
	for _, r := range rows {
		out[[2]any{r.InboundId, r.Key}] = r.Value
	}
	return out
}

// TestBackfillClientCredentialsKeepsOneSecretPerInbound is the case that decided
// the key: GenerateFakeTLSSecret embeds the inbound's own FakeTLS domain.
func TestBackfillClientCredentialsKeepsOneSecretPerInbound(t *testing.T) {
	initTestDB(t)
	addLegacyClientColumns(t)

	secretA := model.GenerateFakeTLSSecret("a.example.com")
	secretB := model.GenerateFakeTLSSecret("b.example.com")
	if secretA == secretB {
		t.Fatal("test setup is vacuous: both domains produced the same secret")
	}
	const email = "dual@example.com"
	inA := createMtprotoInbound(t, "mt-a", 8461, map[string]any{
		"fakeTlsDomain": "a.example.com",
		"clients": []any{
			map[string]any{"email": email, "secret": secretA, "adTag": "0123456789abcdef0123456789abcdef", "enable": true},
		},
	})
	inB := createMtprotoInbound(t, "mt-b", 8462, map[string]any{
		"fakeTlsDomain": "b.example.com",
		"clients": []any{
			map[string]any{"email": email, "secret": secretB, "enable": true},
		},
	})
	clientId := seedLegacyClient(t, email, secretA, "0123456789abcdef0123456789abcdef", inA.Id, inB.Id)

	clearSeederHistory(t, "ClientCredentials")
	if err := backfillClientCredentials(); err != nil {
		t.Fatalf("backfillClientCredentials: %v", err)
	}

	got := credentialRows(t, clientId)
	want := map[[2]any]string{
		{inA.Id, model.CredentialSecret}: secretA,
		{inA.Id, model.CredentialAdTag}:  "0123456789abcdef0123456789abcdef",
		{inB.Id, model.CredentialSecret}: secretB,
		{inB.Id, model.CredentialAdTag}:  "0123456789abcdef0123456789abcdef",
	}
	if len(got) != len(want) {
		t.Fatalf("backfill wrote %d rows, want %d: %v", len(got), len(want), got)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("client_credentials[%v] = %q, want %q", key, got[key], value)
		}
	}
	if got[[2]any{inA.Id, model.CredentialSecret}] == got[[2]any{inB.Id, model.CredentialSecret}] {
		t.Error("both inbounds got the same secret; the shared column, not each inbound's settings, was used")
	}
}

func TestBackfillClientCredentialsIsIdempotent(t *testing.T) {
	initTestDB(t)
	addLegacyClientColumns(t)

	const email = "once@example.com"
	secret := model.GenerateFakeTLSSecret("c.example.com")
	in := createMtprotoInbound(t, "mt-once", 8463, map[string]any{
		"fakeTlsDomain": "c.example.com",
		"clients":       []any{map[string]any{"email": email, "secret": secret, "enable": true}},
	})
	clientId := seedLegacyClient(t, email, secret, "", in.Id)

	clearSeederHistory(t, "ClientCredentials")
	if err := backfillClientCredentials(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := credentialRows(t, clientId)

	clearSeederHistory(t, "ClientCredentials")
	if err := backfillClientCredentials(); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second := credentialRows(t, clientId)

	if len(first) != 1 || first[[2]any{in.Id, model.CredentialSecret}] != secret {
		t.Fatalf("first run must write exactly the secret row, got %v", first)
	}
	if len(second) != len(first) {
		t.Fatalf("re-running the backfill changed the row count: %d then %d", len(first), len(second))
	}
	for key, value := range first {
		if second[key] != value {
			t.Errorf("re-running the backfill changed %v: %q -> %q", key, value, second[key])
		}
	}
}

func TestBackfillClientCredentialsSkipsEmptyValues(t *testing.T) {
	initTestDB(t)
	addLegacyClientColumns(t)

	const email = "bare@example.com"
	in := createMtprotoInbound(t, "mt-bare", 8464, map[string]any{
		"fakeTlsDomain": "d.example.com",
		"clients":       []any{map[string]any{"email": email, "enable": true}},
	})
	clientId := seedLegacyClient(t, email, "", "", in.Id)

	clearSeederHistory(t, "ClientCredentials")
	if err := backfillClientCredentials(); err != nil {
		t.Fatalf("backfillClientCredentials: %v", err)
	}

	if got := credentialRows(t, clientId); len(got) != 0 {
		t.Fatalf("a client with no secret must produce no rows, got %v", got)
	}
}

// TestBackfillClientCredentialsFallsBackToTheSharedColumns covers the rows the
// settings blob lost: the merge rules kept clients.secret when the blob had none.
func TestBackfillClientCredentialsFallsBackToTheSharedColumns(t *testing.T) {
	initTestDB(t)
	addLegacyClientColumns(t)

	const email = "column@example.com"
	secret := model.GenerateFakeTLSSecret("e.example.com")
	const adTag = "fedcba9876543210fedcba9876543210"
	in := createMtprotoInbound(t, "mt-column", 8465, map[string]any{
		"fakeTlsDomain": "e.example.com",
		"clients":       []any{map[string]any{"email": email, "enable": true}},
	})
	clientId := seedLegacyClient(t, email, secret, adTag, in.Id)

	clearSeederHistory(t, "ClientCredentials")
	if err := backfillClientCredentials(); err != nil {
		t.Fatalf("backfillClientCredentials: %v", err)
	}

	got := credentialRows(t, clientId)
	if got[[2]any{in.Id, model.CredentialSecret}] != secret {
		t.Errorf("secret column not carried over, got %q", got[[2]any{in.Id, model.CredentialSecret}])
	}
	if got[[2]any{in.Id, model.CredentialAdTag}] != adTag {
		t.Errorf("ad_tag column not carried over, got %q", got[[2]any{in.Id, model.CredentialAdTag}])
	}
}
