package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// A complete REALITY block: a fallback host that needs no certificate file on
// disk, so these cases turn on the encryption rule and nothing else.
const realityKeypair = `{"target":"example.com:443","serverNames":["example.com"],"shortIds":["aa"],"privateKey":"YIrCPHOtBRQVvz-x2ZaLRD3vwUEwOHZYZKUmDlM8UUA"}`

const vlessAuthDecryption = "mlkem768x25519plus.native.600s.QEn4fVtsjlysseKSxOXb9Ce9iyLqAIuaDGWLGcWMSlU"

func seedFallbackMaster(t *testing.T, tag string, port int, decryption string) *model.Inbound {
	t.Helper()
	settings := `{"clients":[],"decryption":"` + decryption + `"}`
	seedInboundConflict(t, tag, "0.0.0.0", port, model.VLESS,
		`{"network":"tcp","security":"reality","realitySettings":`+realityKeypair+`}`,
		settings)
	return loadInboundByTag(t, tag)
}

/*
xray-core answers a VLESS inbound carrying both decryption and fallbacks with
`"fallbacks" can not be used together with "decryption"`, which fails the whole
config — every inbound on the box, not just this one. The inbound form offers
"generate VLESS auth" and the Fallbacks card in the same modal, on the same
transport, so the pairing is two clicks apart.
*/
func TestFallbacksRefusedOnAnEncryptedVlessInbound(t *testing.T) {
	setupConflictDB(t)
	master := seedFallbackMaster(t, "fb-encrypted", 25101, vlessAuthDecryption)

	err := (&FallbackService{}).SetByMaster(master.Id, []FallbackInput{{Dest: "8080"}})
	if err == nil {
		t.Fatal("fallbacks were attached to an inbound whose config xray-core then refuses whole")
	}
	if !strings.Contains(err.Error(), "VLESS encryption") {
		t.Fatalf("error %q does not name the reason", err)
	}

	rows, gErr := (&FallbackService{}).GetByMaster(master.Id)
	if gErr != nil {
		t.Fatalf("GetByMaster: %v", gErr)
	}
	if len(rows) != 0 {
		t.Fatalf("the refused fallback was stored anyway: %d row(s)", len(rows))
	}
}

// The same inbound without encryption is the ordinary fallback host, so the
// guard cannot be satisfied by refusing fallbacks generally.
func TestFallbacksAcceptedOnAPlainVlessInbound(t *testing.T) {
	setupConflictDB(t)
	master := seedFallbackMaster(t, "fb-plain", 25102, "none")

	if err := (&FallbackService{}).SetByMaster(master.Id, []FallbackInput{{Dest: "8080"}}); err != nil {
		t.Fatalf("SetByMaster on a plain vless/tcp/tls inbound: %v", err)
	}
	rows, err := (&FallbackService{}).GetByMaster(master.Id)
	if err != nil {
		t.Fatalf("GetByMaster: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored %d fallback(s), want 1", len(rows))
	}
}

// The other direction, and the one an operator actually reaches: the fallbacks
// are already attached and the edit turns encryption on. Dropping them silently
// would leave the child inbounds listening with nothing arriving.
func TestUpdateInboundRefusesEncryptionWhileFallbacksAreAttached(t *testing.T) {
	setupConflictDB(t)
	master := seedFallbackMaster(t, "fb-edit", 25103, "none")
	if err := (&FallbackService{}).SetByMaster(master.Id, []FallbackInput{{Dest: "8080"}}); err != nil {
		t.Fatalf("SetByMaster: %v", err)
	}

	update := *loadInboundByTag(t, "fb-edit")
	update.Settings = `{"clients":[],"decryption":"` + vlessAuthDecryption + `"}`
	_, _, err := (&InboundService{}).UpdateInbound(&update)
	if err == nil {
		t.Fatal("encryption was turned on under attached fallbacks, which stops them being emitted with no trace")
	}
	if !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("error %q does not name the fallbacks", err)
	}

	reloaded := loadInboundByTag(t, "fb-edit")
	if strings.Contains(reloaded.Settings, vlessAuthDecryption) {
		t.Fatal("the refused edit was committed anyway")
	}
}

// An edit that leaves the inbound able to host them still goes through.
func TestUpdateInboundAllowsAnEditThatKeepsFallbacksHostable(t *testing.T) {
	setupConflictDB(t)
	master := seedFallbackMaster(t, "fb-keep", 25104, "none")
	if err := (&FallbackService{}).SetByMaster(master.Id, []FallbackInput{{Dest: "8080"}}); err != nil {
		t.Fatalf("SetByMaster: %v", err)
	}

	update := *loadInboundByTag(t, "fb-keep")
	update.Remark = "renamed"
	if _, _, err := (&InboundService{}).UpdateInbound(&update); err != nil {
		t.Fatalf("an ordinary edit must still save: %v", err)
	}
}
