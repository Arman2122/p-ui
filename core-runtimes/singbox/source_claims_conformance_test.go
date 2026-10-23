package singbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	anytls "github.com/anytls/sing-anytls"
	"github.com/gofrs/uuid/v5"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/tuic"

	"github.com/Arman2122/p-ui/internal/core"
)

func TestSourceCredentialClaimsMatchPinnedSingboxParsing(t *testing.T) {
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
		if fixture.Kind != "tuic" && fixture.Kind != "anytls" {
			continue
		}
		checked++
		t.Run(fixture.Name, func(t *testing.T) {
			var key []byte
			if fixture.Kind == "tuic" {
				parsed, err := uuid.FromString(fixture.Credentials[core.CredUUID])
				if err != nil {
					t.Fatal(err)
				}
				key = parsed[:]
				users := []*option.TUICUser{{Name: "canonical-owner", UUID: parsed.String(), Password: "first-password"}, {Name: "alias-owner", UUID: fixture.Credentials[core.CredUUID], Password: "second-password"}}
				if err := new(tuic.Inbound).SetAuthenticatedUsers(users); err == nil || err.Error() != "invalid or duplicate authenticated UUID" {
					t.Fatalf("pinned authenticated-user parser allowed duplicate UUID identity: %v", err)
				}
			} else {
				parsed := sha256.Sum256([]byte(fixture.Credentials[core.CredPassword]))
				key = parsed[:]
				service := new(anytls.Service)
				users := []*anytls.User{{Name: "first-owner", Password: fixture.Credentials[core.CredPassword]}, {Name: "second-owner", Password: fixture.Credentials[core.CredPassword]}}
				if err := service.SetAuthenticatedUsers(users); err == nil || err.Error() != "duplicate authenticated password" {
					t.Fatalf("pinned service allowed duplicate password identity: %v", err)
				}
				users[1].Password += "-other"
				if err := service.SetAuthenticatedUsers(users); err != nil || len(service.AuthenticationUsers()) != 2 {
					t.Fatalf("pinned service rejected distinct password identities: %v", err)
				}
			}
			claim := core.NewSourceCredentialClaim(fixture.Scheme, key)
			if hex.EncodeToString(claim.Digest[:]) != fixture.Digest {
				t.Fatal("source claim no longer matches pinned runtime lookup key")
			}
		})
	}
	if checked != 5 {
		t.Fatalf("pinned runtime fixtures checked=%d", checked)
	}
}
