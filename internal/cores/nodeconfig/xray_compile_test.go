package nodeconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	xuuid "github.com/xtls/xray-core/common/uuid"
	"github.com/xtls/xray-core/proxy/vless"
	vencoding "github.com/xtls/xray-core/proxy/vless/encoding"

	"github.com/Arman2122/p-ui/internal/core"
	xrayruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/xray"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func xrayCompilerFixture(t *testing.T) Input {
	t.Helper()
	return Input{NodeID: uuid.NewString(), Inbounds: []Inbound{{Instance: core.Instance{ID: 1, Kind: "vless", Tag: "source-listener", Listen: "127.0.0.1", Port: 14443, Enable: true, Settings: `{"decryption":"none","clients":[{"id":"stray-credential","email":"unassigned"}]}`}, TargetID: 9, TargetTag: "target-listener", Clients: []Client{{ClientID: uuid.NewString(), User: core.User{Email: "source-client-label", Enable: true, Credentials: map[string]any{core.CredUUID: uuid.NewString()}}}}}}}
}

func TestCompiledXrayPreservesCredentialsAndCanonicalRouting(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var connections sync.WaitGroup
	connections.Go(func() {
		for {
			connection, err := target.Accept()
			if err != nil {
				return
			}
			connections.Go(func() {
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.Copy(connection, connection)
			})
		}
	})
	t.Cleanup(func() { _ = target.Close(); connections.Wait() })
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}
	input := xrayCompilerFixture(t)
	input.Inbounds[0].Instance.Port = port
	input.Bases = []Base{{RuntimeID: "xray", Core: "xray", Configuration: json.RawMessage(`{"outbounds":[{"protocol":"blackhole","tag":"deny"},{"protocol":"freedom","tag":"allowed","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.1/32"]}]}}],"routing":{"rules":[{"type":"field","user":["source-client-label"],"inboundTag":["source-listener"],"outboundTag":"allowed"}]}}`)}}
	result, err := CompileXray(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Runtimes) != 1 || len(result.Listeners) != 1 || bytes.Contains(result.Runtimes[0].Configuration, []byte("stray-credential")) {
		t.Fatal("incomplete or unscoped compiler result")
	}
	account, err := limits.New(limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 2048, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	var runtime xrayruntime.Runtime
	client := input.Inbounds[0].Clients[0]
	if err := runtime.Apply(t.Context(), result.Runtimes[0].Configuration, func(id string) (*limits.Account, bool) { return account, id == client.ClientID }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	id, err := xuuid.ParseString(core.CredString(client.User.Credentials, core.CredUUID))
	if err != nil {
		t.Fatal(err)
	}
	request := &protocol.RequestHeader{Version: vencoding.Version, Command: protocol.RequestCommandTCP, Address: xnet.ParseAddress("127.0.0.1"), Port: xnet.Port(target.Addr().(*net.TCPAddr).Port), User: &protocol.MemoryUser{Account: &vless.MemoryAccount{ID: protocol.NewID(id)}}}
	if err := vencoding.EncodeRequestHeader(connection, request, &vencoding.Addons{}); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("q"), 1024)
	if _, err := connection.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := vencoding.DecodeResponseHeader(connection, request); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("compiled client/routing failed: %v", err)
	}
	if actual := account.Snapshot(); actual.AdmittedUpload != 1024 || actual.AdmittedDownload != 1024 {
		t.Fatalf("compiled identity accounting=%+v", actual)
	}
}

func TestXrayCompilerRefusesUnresolvedNetworking(t *testing.T) {
	for _, test := range []struct {
		name, base string
		want       error
	}{
		{"missing proxy", `{"outbounds":[{"tag":"proxy","protocol":"freedom","proxySettings":{"tag":"missing"}}]}`, ErrDependency},
		{"cycle", `{"outbounds":[{"tag":"one","protocol":"freedom","proxySettings":{"tag":"two"}},{"tag":"two","protocol":"freedom","proxySettings":{"tag":"one"}}]}`, ErrConflict},
		{"foreign client", `{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","user":["not-assigned"],"outboundTag":"direct"}]}}`, ErrDependency},
		{"foreign listener", `{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["other-node"],"outboundTag":"direct"}]}}`, ErrDependency},
		{"unbound geodata", `{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","domain":["geosite:private"],"outboundTag":"direct"}]}}`, ErrDependency},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := xrayCompilerFixture(t)
			input.Bases = []Base{{RuntimeID: "xray", Core: "xray", Configuration: json.RawMessage(test.base)}}
			if _, err := CompileXray(input); !errors.Is(err, test.want) {
				t.Fatalf("network validation=%v, want %v", err, test.want)
			}
		})
	}
}
