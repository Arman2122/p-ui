package radius

import (
	"context"
	"sync/atomic"
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
)

type pendingAuthorizationSink struct{}

func (pendingAuthorizationSink) Authenticate(_ context.Context, _ string, password string, _ Peer) Verdict {
	if password != "verified-password" {
		return Verdict{Reason: "wrong password"}
	}
	return Verdict{Authenticated: true, Reason: "client authorization unavailable"}
}

func (pendingAuthorizationSink) Account(context.Context, Event) {}

func TestAuthenticatedAuthorizationDenialDoesNotCountAsFailedPassword(t *testing.T) {
	server, err := New("authorization-fixture-secret", "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	var failures atomic.Int32
	server.Rejected = func(string, string, Peer, string) { failures.Add(1) }
	if err := server.Register("native-ocserv", pendingAuthorizationSink{}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		password string
		reason   string
		failures int32
	}{{"verified-password", "client authorization unavailable", 0}, {"wrong-password", "wrong password", 1}} {
		response := exchange(t, server.AuthAddr(), accessRequest(server.Secret(), "native-ocserv", "alice", test.password))
		if response.Code != radius.CodeAccessReject || rfc2865.ReplyMessage_GetString(response) != test.reason || failures.Load() != test.failures {
			t.Fatalf("authorization denial classification code=%v reason=%q failures=%d", response.Code, rfc2865.ReplyMessage_GetString(response), failures.Load())
		}
	}
}
