package nodeconfig

import (
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores/internal/ssh"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func sshCompilerFixture(t *testing.T, transport string) Input {
	t.Helper()
	settings := map[string]any{"transport": transport, "path": "tunnel", "banner": "Node SSH", "udpgw": true, "clients": []map[string]any{{"username": "ghost", "password": "unassigned-secret"}}}
	inbound := Inbound{Instance: core.Instance{ID: 53, Kind: "ssh", Tag: "master-tag", Listen: "0.0.0.0", Port: 2222, Enable: true}, TargetID: 9, TargetTag: "node-tag", Egress: EgressBinding{Mode: EgressDirect}, Assets: map[string]string{}}
	inbound.Clients = []Client{{ClientID: nativeCompilerClientID, User: core.User{Email: "mutable-display@example.test", Enable: true, QuotaBytes: 12345, ExpiryUnixMilli: 123456789, Credentials: map[string]any{core.CredUsername: "alice", core.CredPassword: "preserved-password", "metadata": "not-a-node-credential"}}}}
	input := Input{NodeID: nativeCompilerNodeID, SSHHostKeys: HostKeyBinding{GenerateLocally: true}}
	if transport == "wss" {
		certificate, privateKey := compilerTestCertificate(t)
		settings["certFile"], settings["keyFile"] = "/absent/master/server.crt", "/absent/master/server.key"
		inbound.Assets["certFile"], inbound.Assets["keyFile"] = "certificate", "private-key"
		input.Assets = []Asset{{ID: "certificate", NodeID: input.NodeID, Kind: AssetCertificate, Data: []byte(certificate)}, {ID: "private-key", NodeID: input.NodeID, Kind: AssetPrivateKey, Data: []byte(privateKey)}}
	}
	inbound.Instance.Settings = compileFixtureJSON(t, settings)
	input.Inbounds = []Inbound{inbound}
	return input
}

func TestSSHCompilerPreservesLoginsAndUsesStableAccounting(t *testing.T) {
	for _, transport := range []string{"tcp", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			input := sshCompilerFixture(t, transport)
			before := compileFixtureJSON(t, input)
			result, err := CompileSSH(input)
			if err != nil || len(result.Runtimes) != 1 || len(result.Listeners) != 1 {
				t.Fatalf("compile SSH: %+v %v", result, err)
			}
			var config ssh.NodeConfiguration
			if err := json.Unmarshal(result.Runtimes[0].Configuration, &config); err != nil {
				t.Fatal(err)
			}
			if config.HostKeyPEM != "" || len(config.Inbounds) != 1 || !reflect.DeepEqual(result.Runtimes[0].ClientIDs, []string{nativeCompilerClientID}) {
				t.Fatal("explicit local host identity or accounting mapping changed")
			}
			inbound := config.Inbounds[0]
			user := inbound.Users[0]
			if inbound.ID != 9 || inbound.Tag != "node-tag" || len(inbound.Users) != 1 || user.Email != nativeCompilerClientID || !user.Enable || user.ExpiryUnixMilli != 0 || user.QuotaBytes != 0 || core.CredString(user.Credentials, core.CredUsername) != "alice" || core.CredString(user.Credentials, core.CredPassword) != "preserved-password" {
				t.Fatal("SSH login identity was coupled to billing identity or source billing fields")
			}
			if strings.Contains(string(result.Runtimes[0].Configuration), "mutable-display") || strings.Contains(string(result.Runtimes[0].Configuration), "unassigned-secret") || strings.Contains(string(result.Runtimes[0].Configuration), "not-a-node-credential") || strings.Contains(string(result.Runtimes[0].Configuration), "/absent/master/") {
				t.Fatal("source-only credentials or paths leaked into SSH configuration")
			}
			settings, err := ssh.ParseNodeSettings(inbound.Settings)
			if err != nil || settings.Path != "/tunnel" || !settings.UDPGW || settings.UDPGWPort != 7300 {
				t.Fatalf("existing SSH settings parser semantics changed: %+v %v", settings, err)
			}
			fileClaims, generatedClaims := 0, 0
			for _, claim := range result.Assets {
				if claim.Delivery == AssetFile {
					fileClaims++
					id, digest, err := configuration.ParseAssetReference(claim.NodePath)
					if err != nil || id != claim.AssetID || digest != claim.SHA256 {
						t.Fatal("file dependency lacks node target or material digest")
					}
				}
				if claim.Delivery == AssetGenerated {
					generatedClaims++
				}
			}
			if len(result.Runtimes[0].FileAssets) != fileClaims || configuration.ValidateFileAssets(result.Runtimes[0].FileAssets) != nil {
				t.Fatal("file dependencies do not carry exact immutable content")
			}
			if generatedClaims != 1 || (transport == "wss" && fileClaims != 2) || (transport != "wss" && fileClaims != 0) {
				t.Fatalf("asset prerequisites lost: %+v", result.Assets)
			}
			if after := compileFixtureJSON(t, input); after != before {
				t.Fatal("SSH compiler modified source identity or credentials")
			}
		})
	}
}

func TestSSHCompilerRequiresApprovedBridgeAndNeverFallsBackDirect(t *testing.T) {
	input := sshCompilerFixture(t, "ws")
	inbound := &input.Inbounds[0]
	inbound.Egress = EgressBinding{}
	if _, err := CompileSSH(input); err != nil {
		t.Fatalf("plain SSH default failed inheritance: %v", err)
	}
	inbound.Instance.Settings = `{"transport":"ws","routeThroughXray":true,"routeXrayPort":19999,"udpgw":true}`
	if _, err := CompileSSH(input); !errors.Is(err, ErrDependency) {
		t.Fatalf("configured routing requirement was accepted without a bridge: %v", err)
	}
	inbound.Egress = EgressBinding{Mode: EgressXray, BridgeID: "node-bridge"}
	if _, err := CompileSSH(input); !errors.Is(err, ErrDependency) {
		t.Fatalf("missing bridge accepted: %v", err)
	}
	input.Bridges = []Bridge{{ID: "node-bridge", NodeID: input.NodeID, RuntimeID: "xray", Core: "xray", Endpoint: netip.MustParseAddrPort("127.0.0.1:31001")}}
	if _, err := CompileSSH(input); !errors.Is(err, ErrDependency) {
		t.Fatalf("UDPGW accepted a TCP-only bridge: %v", err)
	}
	input.Bridges[0].UDP = true
	result, err := CompileSSH(input)
	if err != nil || len(result.Bridges) != 1 || len(result.Listeners) != 1 {
		t.Fatalf("bound bridge compile: %+v %v", result, err)
	}
	var config ssh.NodeConfiguration
	if err := json.Unmarshal(result.Runtimes[0].Configuration, &config); err != nil {
		t.Fatal(err)
	}
	settings, err := ssh.ParseNodeSettings(config.Inbounds[0].Settings)
	if err != nil || !settings.RouteThroughXray || settings.RouteXrayPort != 31001 || result.Bridges[0].Endpoint.Port() != 31001 || !result.Bridges[0].UDP {
		t.Fatalf("node-local bridge binding changed: %+v %v", settings, err)
	}
}

func TestSSHCompilerRequiresExplicitHostIdentityAndAssets(t *testing.T) {
	input := sshCompilerFixture(t, "wss")
	input.SSHHostKeys = HostKeyBinding{}
	if _, err := CompileSSH(input); !errors.Is(err, ErrDependency) {
		t.Fatalf("compiler inferred an unapproved host-key strategy: %v", err)
	}
	keys, err := ssh.NewNodeHostKeys()
	if err != nil {
		t.Fatal(err)
	}
	input.SSHHostKeys.AssetID = "approved-host-identity"
	input.Assets = append(input.Assets, Asset{ID: "approved-host-identity", NodeID: input.NodeID, Kind: AssetSSHHostKeys, Data: []byte(keys)})
	result, err := CompileSSH(input)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ssh.ParseNodeConfiguration(result.Runtimes[0].Configuration)
	if err != nil || config.HostKeyPEM != keys {
		t.Fatalf("approved stable host identity changed: %v", err)
	}
	input.Inbounds[0].Clients = nil
	input.Assets = input.Assets[1:]
	if _, err := CompileSSH(input); !errors.Is(err, ErrDependency) {
		t.Fatalf("empty placement hid missing certificate material: %v", err)
	}
}

func TestSSHCompilerRejectsUnsupportedLimitsAndListenerConflict(t *testing.T) {
	input := sshCompilerFixture(t, "tcp")
	input.Inbounds[0].Clients[0].User.LimitIP = 2
	if _, err := CompileSSH(input); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("shared IP limit was silently weakened: %v", err)
	}
	input.Inbounds[0].Clients[0].User.LimitIP = 0
	second := input.Inbounds[0]
	second.Instance.ID, second.TargetID, second.Instance.Listen = 54, 10, "127.0.0.1"
	input.Inbounds = append(input.Inbounds, second)
	if result, err := CompileSSH(input); !errors.Is(err, ErrConflict) || len(result.Runtimes) != 0 {
		t.Fatalf("wildcard listener collision accepted: %+v %v", result, err)
	}
}

func TestSSHCompilerIsDeterministicAcrossPlacementOrder(t *testing.T) {
	input := sshCompilerFixture(t, "tcp")
	second := input.Inbounds[0]
	second.Instance.ID, second.TargetID, second.Instance.Port = 54, 10, 2223
	input.Inbounds = append(input.Inbounds, second)
	first, err := CompileSSH(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Inbounds[0], input.Inbounds[1] = input.Inbounds[1], input.Inbounds[0]
	other, err := CompileSSH(input)
	if err != nil || !reflect.DeepEqual(first, other) {
		t.Fatalf("reordering placement changed node definition: %v", err)
	}
}
