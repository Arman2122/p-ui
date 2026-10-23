package mtproto

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/Arman2122/mtg-proxy/mtglib"

	"github.com/Arman2122/p-ui/internal/core"
)

func TestSourceCredentialClaimsMatchPinnedMTProtoParsing(t *testing.T) {
	data, err := os.ReadFile("../../internal/core/testdata/source_credential_claims.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name, Kind, Scheme, Digest string
		Credentials                map[string]string
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, fixture := range fixtures {
		if fixture.Kind != "mtproto" {
			continue
		}
		checked++
		t.Run(fixture.Name, func(t *testing.T) {
			secret, err := mtglib.ParseSecret(fixture.Credentials[core.CredSecret])
			if err != nil {
				t.Fatal(err)
			}
			claim := core.NewSourceCredentialClaim(fixture.Scheme, secret.Key[:])
			if hex.EncodeToString(claim.Digest[:]) != fixture.Digest {
				t.Fatal("source claim no longer matches pinned FakeTLS secret key")
			}
		})
	}
	if checked != 2 {
		t.Fatalf("pinned runtime fixtures checked=%d", checked)
	}
}
