package singbox

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func refusedDemandStream(t *testing.T, outbound adapter.Outbound, target string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	connection, err := outbound.DialContext(ctx, "tcp", M.ParseSocksaddr(target))
	if err != nil {
		return
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	_, _ = connection.Write([]byte("denied demand must not forward"))
	if n, err := connection.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("demand receipt forwarded traffic: %d %v", n, err)
	}
}

func TestAuthenticatedSingboxDemandCannotAdmitDeniedStreams(t *testing.T) {
	for _, kind := range []string{"anytls", "tuic"} {
		t.Run(kind, func(t *testing.T) {
			var current atomic.Pointer[limits.Account]
			events := make(chan [2]string, 16)
			lookup := func(client string) (*limits.Account, bool) {
				account := current.Load()
				return account, account != nil && client == "client-c"
			}
			demand := func(_ context.Context, client, inbound string) error {
				events <- [2]string{client, inbound}
				return nil
			}
			clients, tags := startPairWithAdmission(t, kind, lookup, demand, "test-password")
			target := echoServer(t)
			for _, tag := range tags {
				refusedDemandStream(t, clients[tag], target)
				select {
				case event := <-events:
					if event != [2]string{"client-c", tag} {
						t.Fatalf("wrong authenticated identity or inbound: %v", event)
					}
				case <-time.After(time.Second):
					t.Fatal("authenticated denied stream produced no demand")
				}
			}
			incorrect, _ := startPairWithAdmission(t, kind, lookup, demand, "incorrect-password")
			refusedDemandStream(t, incorrect[tags[0]], target)
			if len(events) != 0 {
				t.Fatal("invalid protocol credential produced demand")
			}
			account, err := limits.New(limits.Policy{Term: "funded-next-attempt", Revision: 1, Enabled: true, CreditCeiling: 4096, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
			if err != nil {
				t.Fatal(err)
			}
			current.Store(account)
			connection := dial(t, clients, tags[0], target)
			if _, err := connection.Write([]byte("verified")); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, 8)
			if _, err := io.ReadFull(connection, got); err != nil || string(got) != "verified" {
				t.Fatalf("committed authority did not admit next attempt: %q %v", got, err)
			}
			if len(events) != 0 {
				t.Fatal("funded protocol stream emitted demand")
			}
			account.Quarantine()
			refusedDemandStream(t, clients[tags[1]], target)
			select {
			case event := <-events:
				if event != [2]string{"client-c", tags[1]} {
					t.Fatalf("wrong denied-account demand: %v", event)
				}
			case <-time.After(time.Second):
				t.Fatal("denied existing account produced no demand")
			}
		})
	}
}

func TestAuthenticatedSingboxPacketDemandCannotForward(t *testing.T) {
	for _, kind := range []string{"anytls", "tuic"} {
		t.Run(kind, func(t *testing.T) {
			events := make(chan [2]string, 8)
			clients, tags := startPairWithAdmission(t, kind, func(string) (*limits.Account, bool) { return nil, false }, func(_ context.Context, client, inbound string) error {
				events <- [2]string{client, inbound}
				return nil
			}, "test-password")
			receiver, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer receiver.Close()
			remote := receiver.LocalAddr().(*net.UDPAddr)
			packet, err := clients[tags[0]].ListenPacket(t.Context(), M.ParseSocksaddr(remote.String()))
			if err != nil {
				t.Fatal(err)
			}
			defer packet.Close()
			_ = packet.SetDeadline(time.Now().Add(time.Second))
			if _, err := packet.WriteTo([]byte("no packet authority"), remote); err != nil {
				t.Fatal(err)
			}
			select {
			case event := <-events:
				if event != [2]string{"client-c", tags[0]} {
					t.Fatalf("wrong packet demand identity: %v", event)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("authenticated packet attempt produced no demand")
			}
			_ = receiver.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			if n, _, err := receiver.ReadFrom(make([]byte, 1024)); n != 0 || err == nil {
				t.Fatalf("demand receipt released UDP payload: %d %v", n, err)
			}
		})
	}
}
