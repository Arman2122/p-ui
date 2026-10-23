package mtproto

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arman2122/mtg-proxy/antireplay"
	"github.com/Arman2122/mtg-proxy/essentials"
	"github.com/Arman2122/mtg-proxy/ipblocklist"
	"github.com/Arman2122/mtg-proxy/logger"
	"github.com/Arman2122/mtg-proxy/mtglib"
	"github.com/Arman2122/mtg-proxy/mtglib/obfuscation"
	network "github.com/Arman2122/mtg-proxy/network/v2"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

type interruptedDial struct {
	mtglib.Network
	stream  chan context.Context
	entered chan struct{}
	closed  chan struct{}
}

type shutdownLogger struct {
	mtglib.Logger
	relay atomic.Bool
}

func (l *shutdownLogger) Named(name string) mtglib.Logger {
	if name == "relay" {
		l.relay.Store(true)
	}
	return l
}
func (l *shutdownLogger) BindInt(string, int) mtglib.Logger     { return l }
func (l *shutdownLogger) BindStr(string, string) mtglib.Logger  { return l }
func (l *shutdownLogger) BindJSON(string, string) mtglib.Logger { return l }

func (n interruptedDial) Send(ctx context.Context, event mtglib.Event) {
	if _, ok := event.(mtglib.EventStart); ok {
		n.stream <- ctx
	}
}

func (n interruptedDial) DialContext(ctx context.Context, _, _ string) (essentials.Conn, error) {
	<-n.stream
	close(n.entered)
	<-ctx.Done()
	<-n.closed
	connection, peer := net.Pipe()
	go func() { defer peer.Close(); _, _ = io.Copy(io.Discard, peer) }()
	return essentials.WrapNetConn(connection), nil
}

func TestShutdownDuringSuccessfulDialCannotRelayNilConnection(t *testing.T) {
	secret, err := mtglib.GenerateSecretMode(mtglib.SecretModeDD, "")
	if err != nil {
		t.Fatal(err)
	}
	account, err := limits.New(limits.Policy{
		Term: "shutdown", Revision: 1, Enabled: true, QuotaUnlimited: true,
		Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	ntw := interruptedDial{
		Network: network.New(nil, "penhoon-test", time.Second, time.Second, time.Second, net.KeepAliveConfig{}, 0),
		stream:  make(chan context.Context, 1), entered: make(chan struct{}), closed: make(chan struct{}),
	}
	log := &shutdownLogger{Logger: logger.NewNoopLogger()}
	proxy, err := New(mtglib.ProxyOpts{
		Secrets: map[string]mtglib.Secret{"client": secret}, Network: ntw, EventStream: ntw,
		AntiReplayCache: antireplay.NewNoop(), IPBlocklist: ipblocklist.NewNoop(), IPAllowlist: allowAll{ipblocklist.NewNoop()},
		Logger: log, ListenerModes: mtglib.ModeSetOf(mtglib.SecretModeDD),
	},
		func(string) (*limits.Account, bool) { return account, true })
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = proxy.ServeWithPolicy(listener, mtglib.ListenerPolicy{Modes: mtglib.ModeSetOf(mtglib.SecretModeDD)})
	}()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := (obfuscation.Obfuscator{Secret: secret.Key[:]}).SendHandshake(essentials.WrapNetConn(client), 2, obfuscation.TagPaddedIntermediate); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = io.Copy(io.Discard, client)
		close(ntw.closed)
	}()
	select {
	case <-ntw.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Telegram dial was not reached")
	}
	_ = listener.Close()
	stopped := make(chan struct{})
	go func() { defer close(stopped); proxy.Shutdown() }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish after canceled dial")
	}
	<-served
	if log.relay.Load() {
		t.Fatal("canceled dial entered relay")
	}
}
