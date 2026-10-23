package xray

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xtls/xray-core/common/protocol"
	xuuid "github.com/xtls/xray-core/common/uuid"
	hysteria "github.com/xtls/xray-core/proxy/hysteria/account"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"
	"github.com/xtls/xray-core/proxy/vmess/aead"

	"github.com/Arman2122/p-ui/internal/core"
)

func sourceClaim(t *testing.T, kind core.Kind, settings string, values map[string]string) core.SourceCredentialClaim {
	t.Helper()
	claims, err := new(Core).SourceClientCredentialClaims(kind, settings, "source-label", values)
	if err != nil || len(claims) != 1 {
		t.Fatalf("source claim count=%d err=%v", len(claims), err)
	}
	return claims[0]
}

func TestSourceClaimsXrayMaskedUUIDsMatchActualValidators(t *testing.T) {
	id := uuid.MustParse("01234567-89ab-4cde-8123-456789abcdef")
	alias, different := id, id
	alias[6], alias[7], different[0] = 0x4f, 0xaa, 0x98
	for _, kind := range []core.Kind{"vless", "hysteria"} {
		t.Run(string(kind), func(t *testing.T) {
			field := core.CredUUID
			var get func(uuid.UUID) *protocol.MemoryUser
			if kind == "vless" {
				account, err := (&vless.Account{Id: id.String()}).AsAccount()
				if err != nil {
					t.Fatal(err)
				}
				validator := new(vless.MemoryValidator)
				if err := validator.Add(&protocol.MemoryUser{Email: "actual-owner", Account: account}); err != nil {
					t.Fatal(err)
				}
				get = func(value uuid.UUID) *protocol.MemoryUser { return validator.Get(xuuid.UUID(value)) }
			} else {
				field = core.CredAuth
				account, err := (&hysteria.Account{Auth: id.String()}).AsAccount()
				if err != nil {
					t.Fatal(err)
				}
				validator := hysteria.NewValidator()
				if err := validator.Add(&protocol.MemoryUser{Email: "actual-owner", Account: account}); err != nil {
					t.Fatal(err)
				}
				get = func(value uuid.UUID) *protocol.MemoryUser { return validator.Get(value.String()) }
			}
			if user := get(alias); user == nil || user.Email != "actual-owner" || get(different) != nil {
				t.Fatal("pinned validator no longer shares the masked UUID lookup")
			}
			original := sourceClaim(t, kind, "", map[string]string{field: id.String()})
			if sourceClaim(t, kind, "", map[string]string{field: alias.String()}) != original {
				t.Fatal("actual authentication alias received a distinct claim")
			}
			if sourceClaim(t, kind, "", map[string]string{field: different.String()}) == original {
				t.Fatal("different authentication identity collided")
			}
		})
	}
}

func TestSourceClaimsVMessAndTrojanMatchAuthenticatedLookupKeys(t *testing.T) {
	id := uuid.NewString()
	account, err := (&vmess.Account{Id: id}).AsAccount()
	if err != nil {
		t.Fatal(err)
	}
	owner := &protocol.MemoryUser{Email: "vmess-owner", Account: account}
	validator := vmess.NewTimedUserValidator()
	if err := validator.Add(owner); err != nil {
		t.Fatal(err)
	}
	key := account.(*vmess.MemoryAccount).ID.CmdKey()
	request := aead.CreateAuthID(key, time.Now().Unix())
	if user, authenticated, err := validator.GetAEAD(request[:]); err != nil || !authenticated || user != owner {
		t.Fatalf("VMess lookup did not authenticate: %v", err)
	}
	if sourceClaim(t, "vmess", "", map[string]string{core.CredUUID: id}) != core.NewSourceCredentialClaim("xray-vmess-command-key-v1", key) {
		t.Fatal("VMess claim does not use the actual command key")
	}
	account, err = (&trojan.Account{Password: "conformance-password"}).AsAccount()
	if err != nil {
		t.Fatal(err)
	}
	owner = &protocol.MemoryUser{Email: "trojan-owner", Account: account}
	trojanValidator := new(trojan.Validator)
	if err := trojanValidator.Add(owner); err != nil {
		t.Fatal(err)
	}
	key = account.(*trojan.MemoryAccount).Key
	if trojanValidator.Get(hex.EncodeToString(key)) != owner {
		t.Fatal("Trojan lookup did not authenticate the pinned wire key")
	}
	if sourceClaim(t, "trojan", "", map[string]string{core.CredPassword: "conformance-password"}) != core.NewSourceCredentialClaim("xray-trojan-sha224-v1", key) {
		t.Fatal("Trojan claim does not use its actual wire lookup key")
	}
}

func TestSourceClaimsLegacyShadowsocksUsesEffectiveCipherAndKey(t *testing.T) {
	for _, item := range []struct {
		cipher  shadowsocks.CipherType
		aliases []string
	}{
		{shadowsocks.CipherType_AES_128_GCM, []string{"aes-128-gcm", "aead_aes_128_gcm", "AES-128-GCM"}},
		{shadowsocks.CipherType_AES_256_GCM, []string{"aes-256-gcm", "aead_aes_256_gcm"}},
		{shadowsocks.CipherType_CHACHA20_POLY1305, []string{"chacha20-poly1305", "chacha20-ietf-poly1305", "aead_chacha20_poly1305"}},
		{shadowsocks.CipherType_XCHACHA20_POLY1305, []string{"xchacha20-poly1305", "xchacha20-ietf-poly1305", "aead_xchacha20_poly1305"}},
	} {
		t.Run(item.aliases[0], func(t *testing.T) {
			account, err := (&shadowsocks.Account{CipherType: item.cipher, Password: "conformance-password"}).AsAccount()
			if err != nil {
				t.Fatal(err)
			}
			key := append(binary.BigEndian.AppendUint32(nil, uint32(item.cipher)), account.(*shadowsocks.MemoryAccount).Key...)
			want := core.NewSourceCredentialClaim("xray-ss-aead-identity-v1", key)
			for _, alias := range item.aliases {
				got := sourceClaim(t, "shadowsocks", fmt.Sprintf(`{"method":%q}`, alias), map[string]string{core.CredPassword: "conformance-password"})
				if got != want {
					t.Fatalf("method alias %s changes its actual authentication claim", alias)
				}
			}
		})
	}
	claims, err := new(Core).SourceClientCredentialClaims("shadowsocks", `{"method":"unsupported"}`, "source", map[string]string{core.CredPassword: "conformance-password"})
	if err == nil || err.Error() != "unsupported shadowsocks source cipher" || len(claims) != 0 {
		t.Fatalf("unknown cipher reserved an identity: %v", err)
	}
}
