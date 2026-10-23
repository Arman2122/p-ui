package cores

import (
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

func TestSourceCredentialClaimsSharedConformanceFixtures(t *testing.T) {
	data, err := os.ReadFile("../core/testdata/source_credential_claims.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name, Kind, Settings, Email, Scheme, Digest string
		Credentials                                 map[string]string
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 7 {
		t.Fatalf("conformance fixture count=%d", len(fixtures))
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			have := maps.Clone(fixture.Credentials)
			claims, err := sourceAuthority(t, core.Kind(fixture.Kind)).SourceClientCredentialClaims(core.Kind(fixture.Kind), fixture.Settings, fixture.Email, have)
			if err != nil || len(claims) != 1 || claims[0].Scheme != fixture.Scheme || hex.EncodeToString(claims[0].Digest[:]) != fixture.Digest || !maps.Equal(have, fixture.Credentials) {
				t.Fatalf("authentication claim differs from pinned conformance fixture: count=%d err=%v", len(claims), err)
			}
		})
	}
}

func TestSourceCredentialClaimsValidateBeforeReservingIdentity(t *testing.T) {
	for _, kind := range []core.Kind{"http", "mixed", "ssh", "openvpn", "ocserv", "tuic", "anytls", "mtproto", "vless", "vmess", "trojan", "shadowsocks", "hysteria", "wgkernel", "awgkernel"} {
		t.Run(string(kind), func(t *testing.T) {
			authority := sourceAuthority(t, kind)
			have := map[string]string{core.CredUsername: "alice"}
			want := authority.ValidateSourceClientCredentials(kind, `{}`, "claim@example.test", have)
			if want == nil {
				t.Fatal("invalid credential fixture is valid")
			}
			claims, err := authority.SourceClientCredentialClaims(kind, `{}`, "claim@example.test", have)
			if err == nil || err.Error() != want.Error() || len(claims) != 0 {
				t.Fatalf("invalid credential reserved an identity: count=%d err=%v want=%v", len(claims), err, want)
			}
		})
	}
}

func TestSourceCredentialClaimsUseLoginNamesWithoutPasswordOrConfigEmail(t *testing.T) {
	for _, kind := range []core.Kind{"http", "mixed", "ssh", "openvpn", "ocserv"} {
		t.Run(string(kind), func(t *testing.T) {
			authority := sourceAuthority(t, kind)
			original := map[string]string{core.CredUsername: "alice", core.CredPassword: "first-password"}
			first, err := authority.SourceClientCredentialClaims(kind, "", "first-config-name", original)
			if err != nil || len(first) != 1 {
				t.Fatalf("first claim: %v", err)
			}
			changed := maps.Clone(original)
			changed[core.CredPassword] = "different-password"
			second, err := authority.SourceClientCredentialClaims(kind, "", "other-config-name", changed)
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatalf("same login acquired a second identity: %v", err)
			}
			changed[core.CredUsername] = "bob"
			third, err := authority.SourceClientCredentialClaims(kind, "", "first-config-name", changed)
			if err != nil || reflect.DeepEqual(first, third) {
				t.Fatalf("different login collided: %v", err)
			}
		})
	}
}
