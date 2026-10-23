package nodeconfig

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func mtprotoCompilerFixture() Input {
	secret := hex.EncodeToString(append(append([]byte{0xee}, bytes.Repeat([]byte{0x11}, 16)...), []byte("www.example.com")...))
	return Input{NodeID: uuid.NewString(), Inbounds: []Inbound{{Instance: core.Instance{ID: 1, Kind: "mtproto", Listen: "127.0.0.1", Port: 14443, Enable: true, Settings: `{"fakeTlsDomain":"www.example.com","clients":[{"secret":"unassigned-secret"}]}`}, TargetID: 1, TargetTag: "node-telegram", Clients: []Client{{ClientID: uuid.NewString(), User: core.User{Email: "mutable-label", Enable: true, Credentials: map[string]any{core.CredSecret: secret, core.CredAdTag: "00112233445566778899aabbccddeeff"}}}}}}}
}

func TestMTProtoCompilerUsesOnlyAssignedSecretsAndExplicitProxyTrust(t *testing.T) {
	input := mtprotoCompilerFixture()
	input.Inbounds[0].Instance.Settings = `{"proxyProtocolListener":true,"clients":[{"secret":"unassigned-secret"}]}`
	if _, err := CompileMTProto(input); !errors.Is(err, ErrDependency) {
		t.Fatalf("implicit proxy trust accepted: %v", err)
	}
	input.Inbounds[0].MTProto = &MTProtoBinding{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}
	result, err := CompileMTProto(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Runtimes) != 1 || bytes.Contains(result.Runtimes[0].Configuration, []byte("unassigned-secret")) {
		t.Fatal("unscoped credential projection")
	}
	var wire configuration.MTProto
	if err := json.Unmarshal(result.Runtimes[0].Configuration, &wire); err != nil {
		t.Fatal(err)
	}
	client := input.Inbounds[0].Clients[0]
	if len(wire.Inbounds) != 1 || len(wire.Inbounds[0].Clients) != 1 || wire.Inbounds[0].Clients[client.ClientID].Secret != client.User.Credentials[core.CredSecret] || len(wire.Inbounds[0].Listeners[0].TrustedProxies) != 1 {
		t.Fatal("lost client identity or proxy scope")
	}
	second := client
	second.ClientID = uuid.NewString()
	input.Inbounds[0].Clients = append(input.Inbounds[0].Clients, second)
	if _, err := CompileMTProto(input); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous authentication key accepted: %v", err)
	}
}

func TestMTProtoCompilerRequiresExplicitWEBIngress(t *testing.T) {
	input := mtprotoCompilerFixture()
	input.Inbounds[0].Instance.Settings = `{"webEnable":true,"webHost":"compiler.test","webBindPort":12345,"webCoverUpstream":"https://cover.example.com"}`
	if _, err := CompileMTProto(input); !errors.Is(err, ErrDependency) {
		t.Fatalf("master backend port reused as public ingress: %v", err)
	}
	input.Inbounds[0].MTProto = &MTProtoBinding{WEB: &MTProtoWEBBinding{Listen: "127.0.0.1", Port: 18443, PublicPort: 24443}}
	if _, err := CompileMTProto(input); !errors.Is(err, ErrDependency) {
		t.Fatalf("WEB ingress missing identity: %v", err)
	}
	certificate, key := compilerTestCertificate(t)
	input.Assets = []Asset{{ID: "web-certificate", NodeID: input.NodeID, Kind: AssetCertificate, Data: []byte(certificate)}, {ID: "web-key", NodeID: input.NodeID, Kind: AssetPrivateKey, Data: []byte(key)}}
	input.Inbounds[0].Assets = map[string]string{"webCertificate": "web-certificate", "webPrivateKey": "web-key"}
	compiled, err := CompileMTProto(input)
	if err != nil || len(compiled.Runtimes) != 1 || len(compiled.Listeners) != 2 {
		t.Fatalf("explicit WEB ingress: %d %d %v", len(compiled.Runtimes), len(compiled.Listeners), err)
	}
	var wire configuration.MTProto
	if err := json.Unmarshal(compiled.Runtimes[0].Configuration, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Inbounds[0].WEB.Bind != "127.0.0.1:18443" || wire.Inbounds[0].WEB.PublicPort != 24443 || wire.Inbounds[0].WEB.ServerCert != certificate {
		t.Fatal("WEB binding or identity was not preserved")
	}
	input.Inbounds[0].Instance.Settings = `{"webEnable":true,"webHost":"compiler.test","webCoverDir":"/master-only/cover"}`
	if _, err := CompileMTProto(input); !errors.Is(err, ErrDependency) {
		t.Fatalf("master cover filesystem copied to node: %v", err)
	}
}
