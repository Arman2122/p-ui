package singbox

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	anytlsservice "github.com/anytls/sing-anytls"
	"github.com/google/uuid"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/anytls"
	"github.com/sagernet/sing-box/protocol/tuic"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	sjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func hotDescriptor() admission.Descriptor {
	return admission.Descriptor{ClientID: uuid.NewString(), InboundTag: "hot", CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString()}
}

func TestSingboxFundedAuthenticationRetainsExactHandle(t *testing.T) {
	for _, kind := range []string{"anytls", "tuic"} {
		t.Run(kind, func(t *testing.T) {
			descriptor := hotDescriptor()
			broker := admission.New("singbox", func() (time.Time, error) { return time.Now(), nil })
			registry := newAuthenticationRegistry()
			defer registry.invalidate(nil)
			var old any = &anytlsservice.User{Name: descriptor.ClientID, Password: "old"}
			if kind == "tuic" {
				old = &option.TUICUser{Name: descriptor.ClientID, UUID: uuid.NewString(), Password: "old"}
			}
			initial, err := broker.Prepare([]admission.Descriptor{descriptor})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := registry.reconcile(admission.WithBinding(t.Context(), initial), "hot", []any{old}, []any{old}, true); err != nil {
				t.Fatal(err)
			}
			if err := broker.Commit(1, initial); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if kind == "tuic" {
				ctx = auth.ContextWithUser(ctx, old.(*option.TUICUser))
			} else {
				ctx = auth.ContextWithUser(ctx, old.(*anytlsservice.User))
			}
			account, err := limits.New(limits.Policy{Term: "funded", Revision: 1, Enabled: true, CreditCeiling: 4096, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
			if err != nil {
				t.Fatal(err)
			}
			tracker := &Tracker{authentication: registry, Lookup: func(string) (*limits.Account, bool) { return account, true }}
			if _, _, err := tracker.account(ctx, descriptor.ClientID, "hot"); err != nil {
				t.Fatal(err)
			}
			descriptor.CredentialGeneration++
			next, err := broker.Prepare([]admission.Descriptor{descriptor})
			if err != nil {
				t.Fatal(err)
			}
			var replacement any = &anytlsservice.User{Name: descriptor.ClientID, Password: "old"}
			if kind == "tuic" {
				copied := *old.(*option.TUICUser)
				replacement = &copied
			}
			handles, err := registry.reconcile(admission.WithBinding(t.Context(), next), "hot", []any{old}, []any{replacement}, false)
			if err != nil || len(handles) != 1 || handles[0] == old {
				t.Fatalf("replacement handle=%v error=%v", handles, err)
			}
			if err := broker.Commit(2, next); err != nil {
				t.Fatal(err)
			}
			if _, _, err := tracker.account(ctx, descriptor.ClientID, "hot"); !errors.Is(err, limits.ErrBlocked) {
				t.Fatal("old funded carrier rebound by mutable user name")
			}
		})
	}
}

func TestSingboxHotAuthenticationPreservesTCPAndUDP(t *testing.T) {
	for _, kind := range []string{"anytls", "tuic"} {
		t.Run(kind, func(t *testing.T) {
			certificate, key := certificatePair(t)
			network := "tcp"
			if kind == "tuic" {
				network = "udp"
			}
			port := freePort(t, network)
			address := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
			a, b := hotDescriptor(), hotDescriptor()
			aUUID, bUUID := uuid.NewString(), uuid.NewString()
			aPassword := "old-a-password"
			accounts := make(map[string]*limits.Account)
			for _, client := range []string{a.ClientID, b.ClientID} {
				account, err := limits.New(limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 1 << 20, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
				if err != nil {
					t.Fatal(err)
				}
				accounts[client] = account
			}
			lookup := func(id string) (*limits.Account, bool) { account, ok := accounts[id]; return account, ok }
			broker := admission.New("singbox", func() (time.Time, error) { return time.Now(), nil })
			var managed Managed
			t.Cleanup(func() { _ = managed.Close() })
			revision := uint64(0)
			apply := func(includeA bool) {
				t.Helper()
				descriptors := []admission.Descriptor{b}
				if includeA {
					descriptors = append(descriptors, a)
				}
				listen := option.ListenOptions{Listen: &address, ListenPort: port}
				tls := option.InboundTLSOptionsContainer{TLS: &option.InboundTLSOptions{Enabled: true, Certificate: []string{certificate}, Key: []string{key}, ALPN: []string{"h3"}}}
				var inbound any
				if kind == "tuic" {
					users := []option.TUICUser{{Name: b.ClientID, UUID: bUUID, Password: "stable-b-password"}}
					if includeA {
						users = append(users, option.TUICUser{Name: a.ClientID, UUID: aUUID, Password: aPassword})
					}
					inbound = &option.TUICInboundOptions{ListenOptions: listen, InboundTLSOptionsContainer: tls, Users: users}
				} else {
					users := []option.AnyTLSUser{{Name: b.ClientID, Password: "stable-b-password"}}
					if includeA {
						users = append(users, option.AnyTLSUser{Name: a.ClientID, Password: aPassword})
					}
					inbound = &option.AnyTLSInboundOptions{ListenOptions: listen, InboundTLSOptionsContainer: tls, Users: users}
				}
				options := option.Options{Log: &option.LogOptions{Disabled: true}, Inbounds: []option.Inbound{{Type: kind, Tag: "hot", Options: inbound}}, Outbounds: []option.Outbound{{Type: "direct", Tag: "direct", Options: &option.DirectOutboundOptions{}}}}
				data, err := sjson.MarshalContext(Context(t.Context()), options)
				if err != nil {
					t.Fatal(err)
				}
				binding, err := broker.Prepare(descriptors)
				if err != nil {
					t.Fatal(err)
				}
				if err := managed.InvalidateAuthentication(t.Context(), []admission.Subject{{ClientID: a.ClientID, InboundTag: "hot"}}); err != nil {
					t.Fatal(err)
				}
				if err := managed.Apply(admission.WithBinding(t.Context(), binding), data, lookup); err != nil {
					t.Fatal(err)
				}
				revision++
				if err := broker.Commit(revision, binding); err != nil {
					t.Fatal(err)
				}
			}
			apply(false)
			instance := managed.instance
			clientContext := service.ContextWithDefaultRegistry(Context(t.Context()))
			client, err := box.New(box.Options{Context: clientContext, Options: option.Options{Log: &option.LogOptions{Disabled: true}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			outbound := func(id, password string) adapter.Outbound {
				t.Helper()
				remote := option.ServerOptions{Server: "127.0.0.1", ServerPort: port}
				tls := option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: "node.test", Certificate: []string{certificate}, ALPN: []string{"h3"}}}
				var outbound adapter.Outbound
				var err error
				if kind == "tuic" {
					outbound, err = tuic.NewOutbound(clientContext, client.Router(), client.LogFactory().NewLogger("hot"), "hot", option.TUICOutboundOptions{ServerOptions: remote, OutboundTLSOptionsContainer: tls, UUID: id, Password: password})
				} else {
					outbound, err = anytls.NewOutbound(clientContext, client.Router(), client.LogFactory().NewLogger("hot"), "hot", option.AnyTLSOutboundOptions{ServerOptions: remote, OutboundTLSOptionsContainer: tls, Password: password, MinIdleSession: 1})
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = common.Close(outbound) })
				if lifecycle, ok := outbound.(adapter.Lifecycle); ok {
					for _, stage := range adapter.ListStartStages {
						if err := lifecycle.Start(stage); err != nil {
							t.Fatal(err)
						}
					}
				}
				return outbound
			}
			tcpTarget, udpTarget := echoServer(t), udpEchoServer(t)
			bOutbound := outbound(bUUID, "stable-b-password")
			bTCP := dial(t, map[string]adapter.Outbound{"hot": bOutbound}, "hot", tcpTarget)
			bUDP, err := bOutbound.ListenPacket(t.Context(), M.ParseSocksaddr(udpTarget))
			if err != nil {
				t.Fatal(err)
			}
			defer bUDP.Close()
			udpRemote, err := net.ResolveUDPAddr("udp", udpTarget)
			if err != nil {
				t.Fatal(err)
			}
			expected := uint64(0)
			checkB := func() {
				t.Helper()
				if managed.instance != instance {
					t.Fatal("hot sing-box authentication restarted unchanged runtime")
				}
				if err := hotExchange(bTCP, []byte("stable-b")); err != nil {
					t.Fatalf("unchanged B stream interrupted: %v", err)
				}
				_ = bUDP.SetDeadline(time.Now().Add(time.Second))
				if _, err := bUDP.WriteTo([]byte("stable-b"), udpRemote); err != nil {
					t.Fatal(err)
				}
				response := make([]byte, 64)
				n, _, err := bUDP.ReadFrom(response)
				if err != nil || string(response[:n]) != "stable-b" {
					t.Fatalf("unchanged B packet association interrupted: %q %v", response[:n], err)
				}
				expected += 16
				hotMeasured(t, accounts[b.ClientID], expected)
			}
			checkB()
			apply(true)
			checkB()
			aOutbound := outbound(aUUID, aPassword)
			aTCP := dial(t, map[string]adapter.Outbound{"hot": aOutbound}, "hot", tcpTarget)
			if err := hotExchange(aTCP, []byte("old-a")); err != nil {
				t.Fatal(err)
			}
			a.CredentialGeneration++
			aPassword = "new-a-password"
			apply(true)
			checkB()
			if err := hotExchange(aTCP, []byte("retired-a")); err == nil {
				t.Fatal("retired sing-box funded stream survived credential change")
			}
			refusedDemandStream(t, aOutbound, tcpTarget)
			fresh := outbound(aUUID, aPassword)
			freshTCP := dial(t, map[string]adapter.Outbound{"hot": fresh}, "hot", tcpTarget)
			if err := hotExchange(freshTCP, []byte("fresh-a")); err != nil {
				t.Fatal(err)
			}
			a.PlacementRevision++
			apply(true)
			checkB()
			if err := hotExchange(freshTCP, []byte("old-placement")); err == nil {
				t.Fatal("sing-box placement-only change retained old proof")
			}
			apply(false)
			checkB()
			a.CredentialGeneration++
			aPassword = "old-a-password"
			apply(true)
			checkB()
			refusedDemandStream(t, aOutbound, tcpTarget)
			checkB()
		})
	}
}

func hotExchange(conn net.Conn, payload []byte) error {
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	data := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, data); err != nil {
		return err
	}
	if !bytes.Equal(data, payload) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func hotMeasured(t *testing.T, account *limits.Account, expected uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		got := account.Snapshot()
		if got.MeasuredUpload == expected && got.MeasuredDownload == expected {
			return
		}
		if got.MeasuredUpload > expected || got.MeasuredDownload > expected || time.Now().After(deadline) {
			t.Fatalf("unchanged accounting changed: %+v expected=%d", got, expected)
		}
		time.Sleep(time.Millisecond)
	}
}
