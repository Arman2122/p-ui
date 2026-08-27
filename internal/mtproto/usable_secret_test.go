package mtproto

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
One malformed client secret took the whole MTProto inbound down.

The sidecar's PUT /secrets parses every entry before applying any of them and
returns 400 on the first bad one, so nobody's edit hot-applies; and at cold
start Config.Validate walks the same secrets and refuses to run. The blast
radius of a single bad field is the entire inbound, not the client holding it.

So a secret the sidecar cannot parse now drops that one client, exactly the way
an empty secret already did. The predicate mirrors mtglib.Secret.Set rather than
guessing: hex OR raw-URL base64, first byte 0xee, at least 16 key bytes after
it, and a non-empty hostname remainder.

Mirroring BOTH decode branches is the point. A hex-only check would look
tighter and would drop a base64url secret the sidecar serves today -- a fresh
outage introduced by the fix for an outage. F172.
*/

func fakeTLSHex(domain string) string {
	return "ee" + "00112233445566778899aabbccddeeff" + hex.EncodeToString([]byte(domain))
}

func TestUsableSecretMirrorsWhatTheSidecarAccepts(t *testing.T) {
	valid := fakeTLSHex("www.cloudflare.com")
	rawBytes, err := hex.DecodeString(valid)
	if err != nil {
		t.Fatalf("fixture is not hex: %v", err)
	}
	validBase64 := base64.RawURLEncoding.EncodeToString(rawBytes)

	cases := []struct {
		name   string
		secret string
		usable bool
		why    string
	}{
		{"a FakeTLS secret in hex", valid, true, "the shape the panel mints"},
		{"the same secret in raw-URL base64", validBase64, true, "Secret.Set falls back to base64 when hex fails; dropping this would be a new outage"},
		{"not hex at all", "not-hex", false, "decodes as neither hex nor base64"},
		{"a bare 32-hex secret", "00112233445566778899aabbccddeeff", false, "no 0xee first byte, so the sidecar refuses it however lenient the panel's own parsers are"},
		{"a dd-prefixed obfuscated2 secret", "dd00112233445566778899aabbccddeeff", false, "first byte is 0xdd; this build of the sidecar is FakeTLS-only"},
		{"FakeTLS with no hostname", "ee00112233445566778899aabbccddeeff", false, "Secret.Set refuses an empty hostname"},
		{"truncated key", "ee0011", false, "fewer than 16 key bytes"},
		{"empty", "", false, "already dropped before this predicate existed"},
		{"padded with whitespace", "  " + valid + "  ", true, "stored values pick up whitespace; trim rather than drop"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := UsableSecret(c.secret)
			if c.usable && got == "" {
				t.Fatalf("dropped a secret the sidecar accepts (%s)", c.why)
			}
			if !c.usable && got != "" {
				t.Fatalf("kept a secret the sidecar 400s, which fails the whole PUT /secrets (%s)", c.why)
			}
		})
	}
}

// And the drop is per-client: a sibling with a good secret must still be served.
func TestAMalformedSecretDropsOnlyItsOwnClient(t *testing.T) {
	good := fakeTLSHex("good.example")
	ib := &model.Inbound{
		Id: 7, Tag: "mt", Listen: "0.0.0.0", Port: 8443, Protocol: model.MTProto,
		Settings: `{"fakeTlsDomain":"good.example","clients":[
			{"email":"broken@e","secret":"not-hex","enable":true},
			{"email":"fine@e","secret":"` + good + `","enable":true}
		]}`,
	}

	inst, ok := InstanceFromInbound(ib)
	if !ok {
		t.Fatal("the inbound produced no instance at all, so one bad secret took every client down with it")
	}
	if len(inst.Secrets) != 1 {
		t.Fatalf("want the one usable client, got %d: %+v", len(inst.Secrets), inst.Secrets)
	}
	if inst.Secrets[0].Name != "fine@e" {
		t.Fatalf("the wrong client survived: %q", inst.Secrets[0].Name)
	}
}
