package ocserv

import (
	"context"
	"hash/fnv"
	"net/netip"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/radius"
)

// interimSeconds is how often the daemon reports a session's bytes; the quota
// job can overshoot by this much of a client's bandwidth, like Xray's poll.
const interimSeconds = 20

// sink answers the RADIUS server for one inbound: the login decision, the
// tunnel address, the speed plan, and the records that become the panel's bytes.
type sink struct {
	c  *Core
	id int
}

/*
Authenticate is the whole admission for a login: the client must exist, be
enabled and unexpired, present its password, and the panel must admit one more
device from where it connects — the cap is the panel's rule, asked through
Deps.Admit. Refusals reach the panel's auth-failure event. The tunnel address
and the speed plan are chosen here, once per session, because that reply is
the only thing this daemon reads per user.
*/
func (s *sink) Authenticate(ctx context.Context, username, password string, peer radius.Peer) radius.Verdict {
	s.c.mu.Lock()
	cur, ok := s.c.instances[s.id]
	if !ok {
		s.c.mu.Unlock()
		return radius.Verdict{Reason: "inbound is not running"}
	}
	user, found := cur.userByName[username]
	settings := cur.settings
	rate := cur.rates[user.Email]
	var sessions []core.Session
	if found {
		sessions = cur.ledger.Sessions()
	}
	s.c.mu.Unlock()

	refuse := func(reason string) radius.Verdict {
		s.c.authFailure(s.id, username, peer.Source, reason)
		return radius.Verdict{Reason: reason, ReplyMessage: reason}
	}
	if !found {
		return refuse("unknown user")
	}
	cred, err := credentialOf(user)
	if err != nil || cred.password != password {
		return refuse("wrong password")
	}
	if !user.Enable {
		return refuse("client is disabled")
	}
	if user.ExpiryUnixMilli > 0 && user.ExpiryUnixMilli < time.Now().UnixMilli() {
		return refuse("client has expired")
	}
	if err := s.c.admit(ctx, s.id, user, peer.Source, sessions); err != nil {
		return refuse(err.Error())
	}
	addr, err := allocate(settings, user.Email, sessions)
	if err != nil {
		return refuse(err.Error())
	}
	return radius.Verdict{
		Accept: true, FramedIP: addr, InterimSeconds: interimSeconds,
		UpBps: rate.UpBps, DownBps: rate.DownBps,
	}
}

/*
allocate picks the tunnel address for a session: a stable preference derived
from the email, moved along the pool past any address a live session holds.
Stable so a reconnecting client usually keeps its address, live-checked so a
panel restart, which forgets nothing about live sessions (the ledger adopts
them from accounting), never hands one address to two clients.
*/
func allocate(settings Settings, email string, live []core.Session) (netip.Addr, error) {
	pool, err := settings.pool()
	if err != nil {
		return netip.Addr{}, err
	}
	size := 1 << (32 - pool.Bits())
	// The network address, the server's own, and the broadcast are not clients'.
	usable := size - 3
	if usable < 1 {
		return netip.Addr{}, errNoRoom
	}
	taken := map[netip.Addr]string{}
	for _, sess := range live {
		for _, local := range sess.Local {
			taken[local] = sess.Email
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(email))
	start := int(h.Sum32() % uint32(usable))
	base := pool.Addr().As4()
	for i := 0; i < usable; i++ {
		offset := (start+i)%usable + 2
		raw := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
		raw += uint32(offset)
		addr := netip.AddrFrom4([4]byte{byte(raw >> 24), byte(raw >> 16), byte(raw >> 8), byte(raw)})
		if owner, held := taken[addr]; !held || owner == email {
			return addr, nil
		}
	}
	return netip.Addr{}, errNoRoom
}

// Account translates the daemon's username into the panel's email before the
// ledger sees it, so bytes are billed to the client and not to a login name.
func (s *sink) Account(_ context.Context, ev radius.Event) {
	s.c.mu.Lock()
	cur, ok := s.c.instances[s.id]
	if !ok {
		s.c.mu.Unlock()
		return
	}
	user, found := cur.userByName[ev.Username]
	ledger := cur.ledger
	s.c.mu.Unlock()
	// A record for a user the sink no longer holds still reaches the ledger
	// nameless: a removed client's Stop must close the session it opened.
	ev.Username = ""
	if found {
		ev.Username = user.Email
	}
	ledger.Apply(ev)
}
