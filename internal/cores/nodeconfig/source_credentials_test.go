package nodeconfig

import (
	"encoding/json"
	"errors"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	nativeruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/native"
)

func TestSourceCredentialsXrayAccountMatchesNodeCompiler(t *testing.T) {
	for _, kind := range []core.Kind{"http", "mixed"} {
		t.Run(string(kind), func(t *testing.T) {
			authority, ok := cores.SourceClientCredentialAuthority(kind)
			if !ok {
				t.Fatal("source account capability missing")
			}
			for _, have := range []map[string]string{nil, {core.CredPassword: "explicit-password"}, {core.CredUsername: "explicit-user", core.CredPassword: "explicit-password"}} {
				values := maps.Clone(have)
				if values == nil {
					values = map[string]string{}
				}
				minted, err := authority.MintSourceClientCredentials(kind, "{}", values)
				if err != nil {
					t.Fatal(err)
				}
				maps.Copy(values, minted.Values)
				if err := authority.ValidateSourceClientCredentials(kind, "{}", "source-email", values); err != nil {
					t.Fatal(err)
				}
				user := core.User{Email: "source-email", Enable: true, Credentials: map[string]any{core.CredUsername: values[core.CredUsername], core.CredPassword: values[core.CredPassword]}}
				entry, identity, err := xrayClient(Inbound{Instance: core.Instance{Kind: kind}}, Client{ClientID: uuid.NewString(), User: user}, map[string]any{})
				if err != nil || identity != values[core.CredUsername] || entry["user"] != values[core.CredUsername] || entry["pass"] != values[core.CredPassword] {
					t.Fatalf("source credential differs from compiled account: %v", err)
				}
			}
			fallback := map[string]string{core.CredPassword: "explicit-password"}
			if err := authority.ValidateSourceClientCredentials(kind, "{}", "source-email", fallback); err != nil {
				t.Fatalf("node email fallback rejected: %v", err)
			}
		})
	}
}

func TestSourceCredentialsWireGuardAllowedIPsMatchNodeCompiler(t *testing.T) {
	for _, kind := range []core.Kind{"wgkernel", "awgkernel"} {
		t.Run(string(kind), func(t *testing.T) {
			input := nativeCompilerFixture(t, kind)
			client := &input.Inbounds[0].Clients[0]
			have := map[string]string{core.CredAllowedIPs: "10.0.0.2/32,fd44::2/128"}
			for _, field := range []string{core.CredPublicKey, core.CredPrivateKey, core.CredPreSharedKey} {
				have[field] = core.CredString(client.User.Credentials, field)
			}
			authority, ok := cores.SourceClientCredentialAuthority(kind)
			if !ok {
				t.Fatal("source capability missing")
			}
			if err := authority.ValidateSourceClientCredentials(kind, input.Inbounds[0].Instance.Settings, client.User.Email, have); err != nil {
				t.Fatal(err)
			}
			client.User.Credentials[core.CredAllowedIPs] = strings.Split(have[core.CredAllowedIPs], ",")
			result, err := CompileNative(input)
			if err != nil {
				t.Fatal(err)
			}
			var config nativeruntime.Configuration
			if err := json.Unmarshal(result.Runtimes[0].Configuration, &config); err != nil {
				t.Fatal(err)
			}
			var addresses []netip.Addr
			if kind == "wgkernel" {
				addresses = config.WireGuard[0].Clients[0].Addresses
			} else {
				addresses = config.AmneziaWG[0].Clients[0].Addresses
			}
			if !slices.Equal(addresses, []netip.Addr{netip.MustParseAddr("10.0.0.2"), netip.MustParseAddr("fd44::2")}) {
				t.Fatalf("source prefixes changed in node compiler: %v", addresses)
			}
			for _, invalid := range []string{"10.0.0.2", "10.0.0.2/33", "10.0.0.2/32,,fd44::2/128"} {
				have[core.CredAllowedIPs] = invalid
				if err := authority.ValidateSourceClientCredentials(kind, "", client.User.Email, have); err == nil || err.Error() != "wireguard source allowedIPs contains an invalid prefix" {
					t.Fatalf("malformed prefix refusal=%v", err)
				}
			}
			for _, invalid := range []string{strings.Repeat("10.0.0.2/32,", 128) + "10.0.0.3/32", strings.Repeat("a", 4097)} {
				have[core.CredAllowedIPs] = invalid
				if err := authority.ValidateSourceClientCredentials(kind, "", client.User.Email, have); err == nil || err.Error() != "wireguard source allowedIPs exceeds 128 prefixes or 4096 bytes" {
					t.Fatalf("oversize prefix refusal=%v", err)
				}
			}
			have[core.CredAllowedIPs] = ""
			if err := authority.ValidateSourceClientCredentials(kind, "", client.User.Email, have); err != nil {
				t.Fatalf("explicit empty allowedIPs refused: %v", err)
			}
			have[core.CredAllowedIPs] = "10.0.0.0/24"
			if err := authority.ValidateSourceClientCredentials(kind, "", client.User.Email, have); err != nil {
				t.Fatalf("source syntax validation assumed node ownership: %v", err)
			}
			client.User.Credentials[core.CredAllowedIPs] = []string{have[core.CredAllowedIPs]}
			if _, err := CompileNative(input); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("node compiler lost its stricter host-address gate: %v", err)
			}
		})
	}
}
