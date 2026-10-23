package nodeconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
)

func singBoxCompilerFixture(t *testing.T, kind core.Kind) Input {
	t.Helper()
	certificate, key := compilerTestCertificate(t)
	id, clientID := uuid.NewString(), uuid.NewString()
	return Input{NodeID: id, Assets: []Asset{{ID: "certificate", NodeID: id, Kind: AssetCertificate, Data: []byte(certificate)}, {ID: "key", NodeID: id, Kind: AssetPrivateKey, Data: []byte(key)}}, Inbounds: []Inbound{{Instance: core.Instance{ID: 1, Kind: kind, Listen: "127.0.0.1", Port: 14443, Enable: true, Settings: `{"certFile":"/master-only/server.crt","keyFile":"/master-only/server.key","clients":[{"password":"stray-secret"}]}`}, TargetID: 1, TargetTag: "node-proxy", Assets: map[string]string{"certificate": "certificate", "privateKey": "key"}, Clients: []Client{{ClientID: clientID, User: core.User{Email: "mutable-label", Enable: true, Credentials: map[string]any{core.CredUUID: uuid.NewString(), core.CredPassword: "assigned-password"}}}}}}}
}

func TestSingBoxCompilerRefusesUnqualifiedBaseConfiguration(t *testing.T) {
	for _, base := range []string{
		`{"outbounds":[{"type":"socks","tag":"direct"}],"route":{"final":"direct"}}`,
		`{"outbounds":[{"type":"direct","tag":"direct","detour":"missing"}],"route":{"final":"direct"}}`,
		`{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct","rules":[{"outbound":"missing"}]}}`,
		`{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"},"dns":{"servers":[{"type":"tls","tag":"resolver","server":"127.0.0.1"}]}}`,
	} {
		input := singBoxCompilerFixture(t, "tuic")
		input.Bases = []Base{{RuntimeID: "singbox", Core: "singbox", Configuration: json.RawMessage(base)}}
		if _, err := CompileSingBox(input); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unqualified base accepted: %v", err)
		}
	}
}

func TestSingBoxCompilerOnlyPublishesAssignedCredentialsAndNodeAssets(t *testing.T) {
	for _, kind := range []core.Kind{"tuic", "anytls"} {
		t.Run(string(kind), func(t *testing.T) {
			input := singBoxCompilerFixture(t, kind)
			result, err := CompileSingBox(input)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Runtimes) != 1 || len(result.Listeners) != 1 || len(result.Assets) != 2 {
				t.Fatalf("incomplete runtime claims: %+v", result.Listeners)
			}
			data := result.Runtimes[0].Configuration
			if bytes.Contains(data, []byte("stray-secret")) || bytes.Contains(data, []byte("/master-only/")) || bytes.Contains(data, []byte("mutable-label")) {
				t.Fatal("compiler copied unassigned credentials or master paths")
			}
			var wire struct {
				Inbounds []struct {
					Users []struct{ Name, UUID, Password string }
					TLS   struct{ Certificate, Key []string }
				}
			}
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Inbounds) != 1 || len(wire.Inbounds[0].Users) != 1 || wire.Inbounds[0].Users[0].Name != input.Inbounds[0].Clients[0].ClientID || wire.Inbounds[0].Users[0].Password != "assigned-password" || len(wire.Inbounds[0].TLS.Certificate) == 0 || len(wire.Inbounds[0].TLS.Key) == 0 {
				t.Fatal("lost canonical identity, credentials or node certificate")
			}
			input.Inbounds[0].Clients[0].User.Enable = false
			omitted, err := CompileSingBox(input)
			if err != nil || len(omitted.Runtimes) != 0 || len(omitted.Listeners) != 0 || len(omitted.Omissions) != 2 {
				t.Fatalf("empty workload exposed listener: %d %d %v", len(omitted.Runtimes), len(omitted.Listeners), err)
			}
			input.Assets = nil
			if _, err := CompileSingBox(input); !errors.Is(err, ErrDependency) {
				t.Fatalf("missing asset became omission: %v", err)
			}
		})
	}
}
