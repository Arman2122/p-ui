package nodeconfig

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"maps"
	"net/netip"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
)

func nodeSourceClaims(t *testing.T, inbound Inbound, client Client) []core.SourceCredentialClaim {
	t.Helper()
	values := map[string]string{}
	for key, value := range client.User.Credentials {
		if text, ok := value.(string); ok {
			values[key] = text
		}
	}
	authority, ok := cores.SourceClientCredentialAuthority(inbound.Instance.Kind)
	if !ok {
		t.Fatal("source authority unavailable")
	}
	claims, err := authority.SourceClientCredentialClaims(inbound.Instance.Kind, inbound.Instance.Settings, client.User.Email, values)
	if err != nil || len(claims) != 1 {
		t.Fatalf("node source claim: count=%d err=%v", len(claims), err)
	}
	return claims
}

func TestSourceClaimsMatchNodeAuthenticationCollisions(t *testing.T) {
	for _, kind := range []core.Kind{"http", "mixed", "ssh", "mtproto", "wgkernel", "awgkernel", "openvpn", "ocserv"} {
		t.Run(string(kind), func(t *testing.T) {
			var input Input
			want := ErrInvalid
			switch kind {
			case "http", "mixed":
				input = xrayCompilerFixture(t)
				input.Inbounds[0].Instance.Kind, input.Inbounds[0].Instance.Settings = kind, `{}`
				input.Inbounds[0].Clients[0].User.Email = "alice"
				input.Inbounds[0].Clients[0].User.Credentials = map[string]any{core.CredPassword: "first-password"}
				want = ErrConflict
			case "ssh":
				input = sshCompilerFixture(t, "tcp")
			case "mtproto":
				input = mtprotoCompilerFixture()
				want = ErrConflict
			default:
				input = nativeCompilerFixture(t, kind)
			}
			if _, err := Compile(input); err != nil {
				t.Fatalf("single identity fixture does not compile: %v", err)
			}
			first := input.Inbounds[0].Clients[0]
			second := first
			second.ClientID, second.User.Email = uuid.NewString(), "different-config-label"
			second.User.Credentials = maps.Clone(first.User.Credentials)
			switch kind {
			case "http", "mixed":
				second.User.Credentials[core.CredUsername] = first.User.Email
				second.User.Credentials[core.CredPassword] = "different-valid-password"
			case "mtproto":
				secret, err := hex.DecodeString(core.CredString(first.User.Credentials, core.CredSecret))
				if err != nil {
					t.Fatal(err)
				}
				second.User.Credentials[core.CredSecret] = hex.EncodeToString(append(secret[:17:17], []byte("other.example")...))
			case "wgkernel", "awgkernel":
				second.User.Credentials[core.CredAllowedIPs] = []string{"10.0.0.3/32", "fd44::3/128"}
				second.User.Credentials[core.CredPreSharedKey] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32))
			default:
				second.User.Credentials[core.CredPassword] = "different-valid-password"
				switch kind {
				case "openvpn":
					second.Native.Addresses = []netip.Addr{netip.MustParseAddr("10.9.0.4"), netip.MustParseAddr("10.9.0.5")}
				case "ocserv":
					second.Native.Subnet = netip.MustParsePrefix("10.9.0.16/29")
				}
			}
			if !reflect.DeepEqual(nodeSourceClaims(t, input.Inbounds[0], first), nodeSourceClaims(t, input.Inbounds[0], second)) {
				t.Fatal("same node login identity acquired different source claims")
			}
			input.Inbounds[0].Clients = append(input.Inbounds[0].Clients, second)
			if _, err := Compile(input); !errors.Is(err, want) {
				t.Fatalf("node did not reject the shared authentication identity: got=%v want=%v", err, want)
			}
		})
	}
}
