package xrayruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-shadowsocks/shadowaead_2022"
	M "github.com/sagernet/sing/common/metadata"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/transport"
)

var errPausedAuthenticationBoundary = errors.New("authenticated dispatch observed")

type pausedShadowsocksRead struct {
	net.Conn
	ctx    context.Context
	reads  int
	paused chan struct{}
	resume chan struct{}
}

func (c *pausedShadowsocksRead) Read(data []byte) (int, error) {
	c.reads++
	if c.reads == 2 {
		close(c.paused)
		select {
		case <-c.resume:
		case <-c.ctx.Done():
			return 0, c.ctx.Err()
		}
	}
	return c.Conn.Read(data)
}

type authenticationBoundary struct{ users chan *protocol.MemoryUser }

func (*authenticationBoundary) Type() any    { return routing.DispatcherType() }
func (*authenticationBoundary) Start() error { return nil }
func (*authenticationBoundary) Close() error { return nil }
func (d *authenticationBoundary) Dispatch(ctx context.Context, _ xnet.Destination) (*transport.Link, error) {
	d.users <- session.InboundFromContext(ctx).User
	return nil, errPausedAuthenticationBoundary
}

func (*authenticationBoundary) DispatchLink(context.Context, xnet.Destination, *transport.Link) error {
	return errPausedAuthenticationBoundary
}

func TestXrayShadowsocks2022PausedAuthenticationKeepsOriginalUser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	key := func(value byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, 16)) }
	user := func(name string, value byte) *protocol.User {
		return &protocol.User{Email: name, Account: serial.ToTypedMessage(&shadowsocks_2022.Account{Key: key(value)})}
	}
	server, err := shadowsocks_2022.NewMultiServer(ctx, &shadowsocks_2022.MultiUserServerConfig{Method: "2022-blake3-aes-128-gcm", Key: key(1), Users: []*protocol.User{user("alice", 2), user("bob", 3)}})
	if err != nil {
		t.Fatal(err)
	}
	original := server.GetUser(ctx, "alice")
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	_ = left.SetDeadline(time.Now().Add(5 * time.Second))
	_ = right.SetDeadline(time.Now().Add(5 * time.Second))
	paused := &pausedShadowsocksRead{Conn: left, ctx: ctx, paused: make(chan struct{}), resume: make(chan struct{})}
	resume := sync.OnceFunc(func() { close(paused.resume) })
	defer resume()
	boundary := &authenticationBoundary{users: make(chan *protocol.MemoryUser, 1)}
	processed := make(chan error, 1)
	go func() {
		processed <- server.Process(session.ContextWithInbound(ctx, &session.Inbound{Tag: "hot"}), xnet.Network_TCP, paused, boundary)
	}()
	method, err := shadowaead_2022.New("2022-blake3-aes-128-gcm", [][]byte{bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dialed := make(chan error, 1)
	go func() { _, err := method.DialConn(right, M.ParseSocksaddr("127.0.0.1:9")); dialed <- err }()
	select {
	case <-paused.paused:
	case err := <-processed:
		t.Fatalf("request stopped before authenticated header: %v", err)
	case <-ctx.Done():
		t.Fatal("authenticated header did not reach the variable-length read")
	}
	if err := server.RemoveUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	replacement, err := user("alice", 4).ToMemoryUser()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.AddUser(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	resume()
	select {
	case err := <-processed:
		if !errors.Is(err, errPausedAuthenticationBoundary) {
			t.Fatalf("dispatch boundary=%v", err)
		}
	case <-ctx.Done():
		t.Fatal("paused authenticated request did not resume")
	}
	select {
	case captured := <-boundary.users:
		if captured != original || captured == replacement || captured == server.GetUser(ctx, "bob") {
			t.Fatal("paused SS2022 authentication changed identity after user replacement")
		}
	default:
		t.Fatal("paused authenticated request did not reach dispatch")
	}
	if err := <-dialed; err != nil {
		t.Fatal(err)
	}
}

func TestXrayEmptyShadowsocks2022KeepsMultiUserAuthentication(t *testing.T) {
	data := []byte(`{"log":{"loglevel":"none"},"penhoon":{"version":1,"clientIdentities":[]},"inbounds":[{"tag":"empty","listen":"127.0.0.1","port":24081,"protocol":"shadowsocks","settings":{"method":"2022-blake3-aes-128-gcm","password":"AQEBAQEBAQEBAQEBAQEBAQ==","clients":[]}}],"outbounds":[{"protocol":"blackhole"}]}`)
	config, err := xrayConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.Inbound[0].ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := settings.(*shadowsocks_2022.MultiUserServerConfig); !ok {
		t.Fatalf("empty SS2022 list switched authentication mode: %T", settings)
	}
	if err := new(Runtime).Validate(data); err != nil {
		t.Fatal(err)
	}
}

func TestXrayShadowsocks2022FailedUserPublicationPreservesExistingUsers(t *testing.T) {
	ctx := t.Context()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 16))
	server, err := shadowsocks_2022.NewMultiServer(ctx, &shadowsocks_2022.MultiUserServerConfig{
		Method: "2022-blake3-aes-128-gcm", Key: key,
		Users: []*protocol.User{{Email: "existing", Account: serial.ToTypedMessage(&shadowsocks_2022.Account{Key: key})}},
	})
	if err != nil {
		t.Fatal(err)
	}
	original := server.GetUser(ctx, "existing")
	err = server.AddUser(ctx, &protocol.MemoryUser{Email: "invalid", Account: &shadowsocks_2022.MemoryAccount{Key: "!invalid"}})
	var malformed base64.CorruptInputError
	if !errors.As(err, &malformed) || malformed != 0 {
		t.Fatalf("invalid credential publication error=%v", err)
	}
	if server.GetUsersCount(ctx) != 1 || server.GetUser(ctx, "existing") != original || server.GetUser(ctx, "invalid") != nil {
		t.Fatal("failed SS2022 credential publication changed the live user set")
	}
}
