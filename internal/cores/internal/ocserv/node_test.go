package ocserv

import (
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/radius"
)

type nodeAuthClock struct {
	now time.Time
	err error
}

func (c nodeAuthClock) Now() (time.Time, error) { return c.now, c.err }

func TestNodeRadiusUsesTrustedAuthorizationTime(t *testing.T) {
	wall := time.Now()
	client := NodeClient{ClientID: "client", Username: "alice", Password: "long-password"}
	a := NewNodeAuthenticator(NodeConfiguration{Clients: []NodeClient{client}})
	a.Grants([]governor.Grant{{ClientID: "client", Policy: limits.Policy{Enabled: true, ExpiresUnixMilli: wall.Add(time.Hour).UnixMilli()}}})
	for _, clock := range []nodeAuthClock{{now: wall.Add(2 * time.Hour)}, {err: limits.ErrClock}} {
		a.SetClock(clock)
		verdict := a.Authenticate(t.Context(), client.Username, client.Password, radius.Peer{})
		if verdict.Accept || !verdict.Authenticated || verdict.Reason != "client authorization unavailable" {
			t.Fatalf("invalid trusted time admitted ocserv: %+v", verdict)
		}
	}
	a.SetClock(nodeAuthClock{now: wall})
	if verdict := a.Authenticate(t.Context(), client.Username, client.Password, radius.Peer{}); !verdict.Accept {
		t.Fatalf("valid trusted authentication: %+v", verdict)
	}
}
