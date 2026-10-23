package cores

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/core"
)

func sourceAuthority(t *testing.T, kind core.Kind) core.SourceCredentialDeclarer {
	t.Helper()
	authority, ok := SourceClientCredentialAuthority(kind)
	if !ok {
		t.Fatalf("source authority unavailable for %s", kind)
	}
	return authority
}

func TestSourceCredentialsMintValidateAndReplayWithoutReplacing(t *testing.T) {
	private, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []core.Kind{"vmess", "vless", "trojan", "shadowsocks", "hysteria", "http", "mixed", "tuic", "anytls", "ssh", "mtproto", "openvpn", "ocserv", "wgkernel", "awgkernel"} {
		t.Run(string(kind), func(t *testing.T) {
			authority := sourceAuthority(t, kind)
			settings := `{"method":"2022-blake3-aes-128-gcm"}`
			have := map[string]string{}
			if kind == "wgkernel" || kind == "awgkernel" {
				have[core.CredPublicKey], have[core.CredPrivateKey] = private.PublicKey().String(), private.String()
			}
			original := maps.Clone(have)
			result, err := authority.MintSourceClientCredentials(kind, settings, have)
			if err != nil || !maps.Equal(have, original) {
				t.Fatalf("mint failed or mutated source: %v", err)
			}
			maps.Copy(have, result.Values)
			if err := authority.ValidateSourceClientCredentials(kind, settings, "source@example.test", have); err != nil {
				t.Fatal(err)
			}
			replay, err := authority.MintSourceClientCredentials(kind, settings, have)
			if err != nil || len(replay.Values) != 0 {
				t.Fatalf("retry reminted a credential: additions=%d err=%v", len(replay.Values), err)
			}
			if kind == "openvpn" {
				if len(result.NodeDerived) != 1 || result.NodeDerived[0] != core.CredTLSCryptV2 || len(replay.NodeDerived) != 1 {
					t.Fatalf("missing OpenVPN node dependency: %v / %v", result.NodeDerived, replay.NodeDerived)
				}
			} else if len(result.NodeDerived) != 0 {
				t.Fatalf("unexpected dependency: %v", result.NodeDerived)
			}
		})
	}
}

func TestSourceCredentialsRejectSuppliedValuesThatLegacyWouldRepair(t *testing.T) {
	for _, item := range []struct {
		kind core.Kind
		have map[string]string
	}{
		{"shadowsocks", map[string]string{core.CredPassword: "explicit-invalid-secret"}},
		{"tuic", map[string]string{core.CredUUID: "explicit-invalid-uuid"}},
		{"anytls", map[string]string{core.CredPassword: "short"}},
		{"ssh", map[string]string{core.CredUsername: "Invalid@Login"}},
		{"openvpn", map[string]string{core.CredPassword: "short"}},
		{"ocserv", map[string]string{core.CredPassword: "short"}},
	} {
		t.Run(string(item.kind), func(t *testing.T) {
			original := maps.Clone(item.have)
			result, err := sourceAuthority(t, item.kind).MintSourceClientCredentials(item.kind, `{"method":"2022-blake3-aes-128-gcm"}`, item.have)
			if !errors.Is(err, core.ErrSourceCredentialConflict) || len(result.Values) != 0 || !maps.Equal(item.have, original) {
				t.Fatalf("supplied credential replaced: additions=%d err=%v", len(result.Values), err)
			}
			for _, secret := range item.have {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("credential leaked into refusal")
				}
			}
		})
	}
}

func TestSourceCredentialsValidateActualNodeFormats(t *testing.T) {
	validKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	validSecret := hex.EncodeToString(append(append([]byte{0xee}, bytes.Repeat([]byte{1}, 16)...), []byte("example.test")...))
	expected := map[string]string{
		"uuid":                   "xray source client requires a canonical nonzero UUID",
		"nil UUID":               "xray source client requires a canonical nonzero UUID",
		"http password":          "xray source account requires a username up to 256 bytes and a password",
		"mixed username":         "xray source account requires a username up to 256 bytes and a password",
		"ss2022 size":            "shadowsocks source client key does not match the inbound method",
		"FakeTLS prefix":         "mtproto source client requires a valid FakeTLS secret",
		"FakeTLS zero key":       "mtproto source client requires a valid FakeTLS secret",
		"FakeTLS whitespace":     "mtproto source client requires a valid FakeTLS secret",
		"WG public key":          "wireguard source client requires a canonical nonzero publicKey",
		"AWG zero key":           "wireguard source client requires a canonical nonzero publicKey",
		"WG shared key":          "wireguard source client requires a canonical nonzero preSharedKey",
		"OpenVPN username":       "openvpn: client source@example.test: username \"bad/name\" may hold only letters, digits, _ . @ -",
		"OpenVPN short password": "openvpn: client source@example.test: password shorter than 8 characters",
		"OpenVPN newline":        "openvpn source password must be at most 512 bytes without line breaks or NUL",
		"OpenVPN wrapped key":    "openvpn source wrapped key has invalid PEM format",
		"ocserv long password":   "ocserv source password must be at most 512 bytes without line breaks or NUL",
		"ocserv NUL":             "ocserv source password must be at most 512 bytes without line breaks or NUL",
		"SSH username":           "ssh: client x: username \"BadUser\" must be 3-32 lowercase letters, digits, dots, dashes or underscores",
		"TUIC UUID":              "singbox: client source@example.test: uuid: invalid UUID length: 3",
	}
	for _, item := range []struct {
		name, field, value string
		kind               core.Kind
		have               map[string]string
	}{
		{"uuid", core.CredUUID, "bad", "vless", map[string]string{core.CredUUID: uuid.NewString()}},
		{"nil UUID", core.CredUUID, uuid.Nil.String(), "vmess", map[string]string{core.CredUUID: uuid.NewString()}},
		{"http password", core.CredPassword, "", "http", map[string]string{core.CredUsername: "alice", core.CredPassword: "password"}},
		{"mixed username", core.CredUsername, strings.Repeat("a", 257), "mixed", map[string]string{core.CredPassword: "password"}},
		{"ss2022 size", core.CredPassword, validKey, "shadowsocks", map[string]string{}},
		{"FakeTLS prefix", core.CredSecret, "dd" + validSecret[2:], "mtproto", map[string]string{core.CredSecret: validSecret}},
		{"FakeTLS zero key", core.CredSecret, "ee" + strings.Repeat("00", 16) + "61", "mtproto", map[string]string{core.CredSecret: validSecret}},
		{"FakeTLS whitespace", core.CredSecret, " " + validSecret, "mtproto", map[string]string{core.CredSecret: validSecret}},
		{"WG public key", core.CredPublicKey, "not-a-key", "wgkernel", map[string]string{}},
		{"AWG zero key", core.CredPublicKey, base64.StdEncoding.EncodeToString(make([]byte, 32)), "awgkernel", map[string]string{}},
		{"WG shared key", core.CredPreSharedKey, "bad", "wgkernel", map[string]string{core.CredPublicKey: validKey}},
		{"OpenVPN username", core.CredUsername, "bad/name", "openvpn", map[string]string{core.CredPassword: "password"}},
		{"OpenVPN short password", core.CredPassword, "short", "openvpn", map[string]string{core.CredUsername: "alice"}},
		{"OpenVPN newline", core.CredPassword, "password\n", "openvpn", map[string]string{core.CredUsername: "alice"}},
		{"OpenVPN wrapped key", core.CredTLSCryptV2, "not-PEM", "openvpn", map[string]string{core.CredUsername: "alice", core.CredPassword: "password"}},
		{"ocserv long password", core.CredPassword, strings.Repeat("p", 513), "ocserv", map[string]string{core.CredUsername: "alice"}},
		{"ocserv NUL", core.CredPassword, "password\x00", "ocserv", map[string]string{core.CredUsername: "alice"}},
		{"SSH username", core.CredUsername, "BadUser", "ssh", map[string]string{core.CredPassword: "password"}},
		{"TUIC UUID", core.CredUUID, "bad", "tuic", map[string]string{core.CredPassword: "password"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			item.have[item.field] = item.value
			err := sourceAuthority(t, item.kind).ValidateSourceClientCredentials(item.kind, `{"method":"2022-blake3-aes-128-gcm"}`, "source@example.test", item.have)
			if err == nil || err.Error() != expected[item.name] {
				t.Fatalf("invalid supplied credential refusal=%v, want %q", err, expected[item.name])
			}
		})
	}
}

func TestSourceCredentialsKeepSS2022BindingsIndependent(t *testing.T) {
	authority := sourceAuthority(t, "shadowsocks")
	settings128, settings256 := `{"method":"2022-blake3-aes-128-gcm"}`, `{"method":"2022-blake3-aes-256-gcm"}`
	first, err := authority.MintSourceClientCredentials("shadowsocks", settings128, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.MintSourceClientCredentials("shadowsocks", settings256, first.Values); !errors.Is(err, core.ErrSourceCredentialConflict) {
		t.Fatalf("shared explicit key silently rotated for a different cipher: %v", err)
	}
	second, err := authority.MintSourceClientCredentials("shadowsocks", settings256, nil)
	if err != nil {
		t.Fatal(err)
	}
	for settings, values := range map[string]map[string]string{settings128: first.Values, settings256: second.Values} {
		if err := authority.ValidateSourceClientCredentials("shadowsocks", settings, "source", values); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []core.Kind{"unknown", "socks", "tunnel", "tun", "wireguard"} {
		if _, ok := SourceClientCredentialAuthority(kind); ok {
			t.Fatalf("unsupported source kind %s fell back to legacy credentials", kind)
		}
	}
}
