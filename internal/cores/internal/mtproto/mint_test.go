package mtproto

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

/*
The minter gives a stored mtproto row its shape: the inbound-level secret and
ad tag older rows carry are dropped — a stale inbound secret once leaked into
links mtg rejected — and every client secret is rebuilt to front a domain.
*/
func TestMintInstanceGivesTheRowTheShapeMtgReads(t *testing.T) {
	c := &Core{}
	got, err := c.MintInstance(Kind, `{"fakeTlsDomain":"a.com","secret":"eedeadbeef","adTag":"00","clients":[{"email":"x","secret":""}]}`, "")
	if err != nil {
		t.Fatalf("MintInstance: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("minted settings are not JSON: %v", err)
	}
	for _, dead := range []string{"secret", "adTag"} {
		if _, ok := parsed[dead]; ok {
			t.Errorf("the inbound-level %q survived the mint: %s", dead, got)
		}
	}
	clients := parsed["clients"].([]any)
	secret, _ := clients[0].(map[string]any)["secret"].(string)
	if !strings.HasPrefix(secret, "ee") || !strings.HasSuffix(secret, hex.EncodeToString([]byte("a.com"))) {
		t.Errorf("client secret = %q, want a FakeTLS secret fronting the inbound's domain", secret)
	}

	// Another kind's settings are not this core's to touch.
	if other, err := c.MintInstance("vless", `{"secret":"keep"}`, ""); err != nil || other != `{"secret":"keep"}` {
		t.Errorf("MintInstance(vless) = %q, %v; want the settings untouched", other, err)
	}
}
