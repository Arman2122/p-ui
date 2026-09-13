package ocserv

import (
	"encoding/json"
	"strings"
	"testing"
)

/*
The form could not take a CA-signed pair, so the Cisco Secure Client path
(which verifies against a CA, never a pin) had no way in; and a pasted
certificate with no key, or with another identity's key, was silently
re-minted over. An operator's pair is kept, and checked as a pair on save.
*/
func TestAnOperatorsOwnIdentityIsKeptAndCheckedAsAPair(t *testing.T) {
	c := New(Deps{})
	identity := func(t *testing.T) (string, string) {
		t.Helper()
		minted, err := c.MintInstance(Kind, `{"subnet":"10.11.0.0/24"}`, "")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct{ ServerCert, ServerKey string }
		if err := json.Unmarshal([]byte(minted), &doc); err != nil {
			t.Fatal(err)
		}
		return doc.ServerCert, doc.ServerKey
	}
	cert, key := identity(t)
	own, err := json.Marshal(map[string]any{"subnet": "10.11.0.0/24", "serverCert": cert, "serverKey": key})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := c.MintInstance(Kind, string(own), "")
	if err != nil || !strings.Contains(kept, strings.Split(cert, "\n")[1]) {
		t.Fatalf("an operator's own pair was not kept: %v", err)
	}
	half, _ := json.Marshal(map[string]any{"subnet": "10.11.0.0/24", "serverCert": cert})
	if _, err := c.MintInstance(Kind, string(half), ""); err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("a certificate without its key was accepted: %v", err)
	}
	_, otherKey := identity(t)
	mismatch, _ := json.Marshal(map[string]any{"subnet": "10.11.0.0/24", "serverCert": cert, "serverKey": otherKey})
	if _, err := c.MintInstance(Kind, string(mismatch), ""); err == nil || !strings.Contains(err.Error(), "not a pair") {
		t.Fatalf("a certificate with another identity's key was accepted: %v", err)
	}
}
