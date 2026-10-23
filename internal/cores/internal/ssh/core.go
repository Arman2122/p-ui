package ssh

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

// Deps are the panel-side facts the core cannot hold itself: where the host
// key lives across restarts, and who wants to hear about a refused login.
type Deps struct {
	Governor      func(string) (*limits.Account, bool)
	Demand        func(context.Context, string, string) error
	DemandContext context.Context
	LoadHostKey   func() (string, error)
	StoreHostKey  func(pem string) error
	AuthFailure   func(AuthFailure)
	// Admit asks the panel to decide a login against the client's cap; nil admits
	// every login, which is what a core built without a panel does.
	Admit core.AdmitFunc
	// Dial replaces the destination dialer, policy and all; nil dials directly
	// under the destination policy in policy.go.
	Dial Dialer
}

// Core is the in-process SSH server behind the contract.
type Core struct {
	deps Deps

	mu        sync.Mutex
	hostKeys  []gossh.Signer
	instances map[int]*instance
	// failed is every desired listener that could not bind, by inbound id, so
	// Health can say so: an absent instance reads as idle, not as a fault.
	failed  map[int]bindFailure
	exits   map[int]*exitClient
	counter *core.Counter
	epoch   string
	starts  int
	// active is who moved bytes in the last scrape: online for a client whose
	// connection the panel polls between, the way every daemon core answers.
	active map[string]struct{}
}

type instance struct {
	inst        core.Instance
	settings    Settings
	fingerprint string
	srv         *server
}

type bindFailure struct {
	tag string
	err error
}

// New returns the core; nothing listens until the first Reconcile.
func New(deps Deps) *Core {
	return &Core{deps: deps, instances: map[int]*instance{}, failed: map[int]bindFailure{}, exits: map[int]*exitClient{}, counter: core.NewCounter(), epoch: strconv.FormatInt(time.Now().UnixNano(), 36)}
}

func (c *Core) Kinds() []core.Kind { return []core.Kind{Kind} }

func (c *Core) Describe() core.Descriptor {
	return core.Descriptor{
		ID:       ID,
		TitleKey: "cores.ssh.title",
		Caps: core.Capabilities{
			UserHotAdd:   core.Yes(),
			PerUserStats: core.Yes(),
			// The panel disables a depleted client and re-applies; the server
			// then closes its sessions. Nothing here needs a budget pushed in.
			QuotaPushdown: core.No(),
			OnlineUsers:   core.Yes(),
			ShareLink:     core.Yes(),
		},
	}
}

// Preflight: an in-process server needs nothing from the host.
func (c *Core) Preflight(context.Context) error { return nil }

func (c *Core) ClientCredentials(kind core.Kind) []string {
	if kind != Kind {
		return nil
	}
	return []string{core.CredUsername, core.CredPassword}
}

func (c *Core) MintClientCredentials(kind core.Kind, _ string, have map[string]string) (map[string]string, error) {
	minted := map[string]string{}
	if kind != Kind {
		return minted, nil
	}
	if !usernameRe.MatchString(have[core.CredUsername]) {
		minted[core.CredUsername] = "u" + randomToken(7)
	}
	if len(have[core.CredPassword]) < 6 {
		minted[core.CredPassword] = randomToken(16)
	}
	return minted, nil
}

func (c *Core) ValidateClient(kind core.Kind, _, _ string, have map[string]string) error {
	if kind != Kind {
		return nil
	}
	_, err := credentialOf(core.User{Email: "x", Enable: true, Credentials: map[string]any{
		core.CredUsername: have[core.CredUsername], core.CredPassword: have[core.CredPassword],
	}})
	return err
}

func (c *Core) ClientIdentity(core.Kind) string { return "email" }

// Transports: every transport is TCP; the WebSocket ones are HTTP on top of it.
func (c *Core) Transports(core.Kind, string, string) core.Transports {
	return core.Transports{TCP: true}
}

func (c *Core) ProvisionsUsers(kind core.Kind) bool { return kind == Kind }

func (c *Core) EnforcesRates(kind core.Kind) bool { return kind == Kind }

// Reconcile converges the listeners on desired: a changed listener is rebuilt,
// an unchanged one has its users replaced in place, a missing one is stopped.
func (c *Core) Reconcile(ctx context.Context, desired []core.Instance) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureHostKeyLocked(); err != nil {
		return err
	}
	want := map[int]struct{}{}
	var failures []error
	for _, d := range desired {
		want[d.ID] = struct{}{}
		if err := c.ensureLocked(ctx, d); err != nil {
			failures = append(failures, err)
		}
	}
	for id, cur := range c.instances {
		if _, ok := want[id]; !ok {
			cur.srv.stop()
			delete(c.instances, id)
		}
	}
	for id := range c.failed {
		if _, ok := want[id]; !ok {
			delete(c.failed, id)
		}
	}
	return errors.Join(failures...)
}

func (c *Core) ensureLocked(ctx context.Context, d core.Instance) error {
	if !d.Enable {
		if cur, ok := c.instances[d.ID]; ok {
			cur.srv.stop()
			delete(c.instances, d.ID)
		}
		delete(c.failed, d.ID)
		return nil
	}
	settings, err := parseSettings(d.Settings)
	if err != nil {
		return fmt.Errorf("ssh: inbound %d: %w", d.ID, err)
	}
	fp := fingerprint(d, settings)
	if cur, ok := c.instances[d.ID]; ok {
		if cur.fingerprint == fp {
			cur.inst, cur.settings = d, settings
			cur.srv.setBanner(settings.Banner)
			return cur.srv.setAuthentication(d.Users, admission.BindingFromContext(ctx), d.Tag)
		}
		cur.srv.stop()
		delete(c.instances, d.ID)
	}
	srv := newServer(d.ID, d.Tag, settings, c.hostKeys, c.dialerFor(settings), c.deps.AuthFailure, c.deps.Admit)
	srv.governor = c.deps.Governor
	srv.demand, srv.demandContext = c.deps.Demand, c.deps.DemandContext
	srv.udp = c.udpDialerFor(settings)
	userErr := srv.setAuthentication(d.Users, admission.BindingFromContext(ctx), d.Tag)
	if _, err := listen(listenSpec{listen: d.Listen, port: d.Port}, srv); err != nil {
		c.failed[d.ID] = bindFailure{tag: d.Tag, err: err}
		return fmt.Errorf("ssh: inbound %d: listen: %w", d.ID, err)
	}
	delete(c.failed, d.ID)
	c.starts++
	c.instances[d.ID] = &instance{inst: d, settings: settings, fingerprint: fp, srv: srv}
	logger.Infof("ssh: inbound %d (%s) listening on %s:%d over %s", d.ID, d.Tag, d.Listen, d.Port, settings.Transport)
	return userErr
}

/*
dialerFor chooses how one inbound's forwards leave this host.

Per instance rather than through Deps.Dial, which the exits share: an exit's
own control connection reaches a remote SSH server, and pushing that through
the bridge would let Xray route it back into an SSH exit and loop.

Fails closed. With the switch on and no bridge port yet -- the window between
enabling it and the allocator running -- the forward is refused rather than
dialled directly, because a direct dial there is the leak the switch exists to
prevent and nothing would report it.
*/
func (c *Core) dialerFor(settings Settings) Dialer {
	if !settings.RouteThroughXray {
		if c.deps.Dial != nil {
			return c.deps.Dial
		}
		return directDialer()
	}
	if settings.RouteXrayPort <= 0 {
		return refusingDialer(errors.New("ssh: this inbound routes through Xray and has no bridge port yet; save it again"))
	}
	return literalGuard(bridgeDialer(settings.RouteXrayPort))
}

/*
udpDialerFor is the UDPGW conversations' half of dialerFor. Direct, it is the
same dialer with network udp, so the destination policy holds for datagrams
too. Routed, each conversation is a SOCKS5 UDP association on the bridge, so
Xray's router sees it as it sees a TCP forward; with the switch on and no
port yet it is nil, which refuses UDP rather than dialling around the router.
*/
func (c *Core) udpDialerFor(settings Settings) udpDialer {
	if settings.RouteThroughXray {
		if settings.RouteXrayPort <= 0 {
			return nil
		}
		return guardedUDP(bridgeUDPDialer(settings.RouteXrayPort))
	}
	dial := c.deps.Dial
	if dial == nil {
		dial = directDialer()
	}
	return func(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
		return dial(ctx, "udp", dst.String())
	}
}

func (c *Core) StopAll(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, cur := range c.instances {
		cur.srv.stop()
		delete(c.instances, id)
	}
	for id, ex := range c.exits {
		ex.stop()
		delete(c.exits, id)
	}
	return nil
}

// PlanChange: a listener field restarts the listener; a user edit is hot.
func (c *Core) PlanChange(before, after core.Instance) core.Action {
	bs, bErr := parseSettings(before.Settings)
	as, aErr := parseSettings(after.Settings)
	switch {
	case bErr != nil || aErr != nil, before.Enable != after.Enable:
		return core.ActionRestart
	case fingerprint(before, bs) != fingerprint(after, as):
		return core.ActionRestart
	case usersFingerprint(before.Users) != usersFingerprint(after.Users), bs.Banner != as.Banner:
		return core.ActionHotApply
	}
	return core.ActionNoop
}

func usersFingerprint(users []core.User) string {
	parts := make([]string, 0, len(users))
	for _, u := range users {
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%t|%d|%d", u.Email, core.CredString(u.Credentials, core.CredUsername), core.CredString(u.Credentials, core.CredPassword), u.Enable, u.ExpiryUnixMilli, u.LimitIP))
	}
	sort.Strings(parts)
	return fmt.Sprint(parts)
}

func (c *Core) ApplyInstance(ctx context.Context, inst core.Instance) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureHostKeyLocked(); err != nil {
		return err
	}
	return c.ensureLocked(ctx, inst)
}

func (c *Core) DropInstance(_ context.Context, inst core.Instance) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur, ok := c.instances[inst.ID]; ok {
		cur.srv.stop()
		delete(c.instances, inst.ID)
	}
	delete(c.failed, inst.ID)
	return nil
}

func (c *Core) AddUser(_ context.Context, inst core.Instance, user core.User) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.instances[inst.ID]
	if !ok {
		return fmt.Errorf("ssh: inbound %d is not running", inst.ID)
	}
	users := withoutEmail(cur.inst.Users, user.Email)
	users = append(users, user)
	cur.inst.Users = users
	return cur.srv.setUsers(users)
}

func (c *Core) RemoveUser(_ context.Context, inst core.Instance, email string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.instances[inst.ID]
	if !ok {
		return nil
	}
	cur.inst.Users = withoutEmail(cur.inst.Users, email)
	return cur.srv.setUsers(cur.inst.Users)
}

func withoutEmail(users []core.User, email string) []core.User {
	out := make([]core.User, 0, len(users))
	for _, u := range users {
		if u.Email != email {
			out = append(out, u)
		}
	}
	return out
}

// CollectTraffic feeds every listener's cumulative counters through the one
// engine, so a restart of this core is a restart like any daemon's.
func (c *Core) CollectTraffic(context.Context) ([]core.TrafficDelta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	readings := map[string]int64{}
	tags := map[string]string{}
	for _, cur := range c.instances {
		for key, value := range cur.srv.readings() {
			full := strconv.Itoa(cur.inst.ID) + "|" + key
			readings[full] = value
			tags[full] = cur.inst.Tag
		}
	}
	deltas := c.counter.Observe(c.epoch, readings)
	byEmail := map[string]*core.TrafficDelta{}
	var order []string
	for key, d := range deltas {
		id, email, dir := splitKey(key)
		agg, ok := byEmail[id+"|"+email]
		if !ok {
			agg = &core.TrafficDelta{Email: email, Tag: tags[key]}
			byEmail[id+"|"+email] = agg
			order = append(order, id+"|"+email)
		}
		if dir == dirUp {
			agg.Up += d
		} else {
			agg.Down += d
		}
	}
	sort.Strings(order)
	out := make([]core.TrafficDelta, 0, len(order))
	c.active = make(map[string]struct{}, len(order))
	for _, k := range order {
		out = append(out, *byEmail[k])
		c.active[byEmail[k].Email] = struct{}{}
	}
	return out, nil
}

func splitKey(key string) (id, email, dir string) {
	// "<id>|<email>|<dir>"; the email itself never carries a pipe.
	first := 0
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			first = i
			break
		}
	}
	last := len(key) - 1
	for ; last > first; last-- {
		if key[last] == '|' {
			break
		}
	}
	return key[:first], key[first+1 : last], key[last+1:]
}

func (c *Core) OnlineEmails(context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]struct{}{}
	for email := range c.active {
		seen[email] = struct{}{}
	}
	for _, cur := range c.instances {
		for _, email := range cur.srv.online() {
			seen[email] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for email := range seen {
		out = append(out, email)
	}
	sort.Strings(out)
	return out, nil
}

func (c *Core) Sessions(context.Context) ([]core.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []core.Session
	for _, cur := range c.instances {
		out = append(out, cur.srv.sessions()...)
	}
	return out, nil
}

// AdmitsAtLogin: this core refuses an over-cap device at the SSH handshake, but
// only when a panel wired the callback that decides it.
func (c *Core) AdmitsAtLogin(kind core.Kind) bool { return c.deps.Admit != nil && kind == Kind }

// CutSessions closes the client's live connections from one source; a reconnect
// is then refused at login. By source so the devices inside the cap stay up.
func (c *Core) CutSessions(_ context.Context, email string, source netip.Addr) (int, error) {
	c.mu.Lock()
	servers := make([]*server, 0, len(c.instances))
	for _, cur := range c.instances {
		servers = append(servers, cur.srv)
	}
	c.mu.Unlock()
	cut := 0
	for _, srv := range servers {
		cut += srv.cut(email, source)
	}
	return cut, nil
}

func (c *Core) EnforceRates(_ context.Context, inst core.Instance, limits map[string]core.RateLimit) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.instances[inst.ID]
	if !ok {
		return nil
	}
	cur.srv.setLimits(limits)
	return nil
}

func (c *Core) EnforcedRates(_ context.Context, inst core.Instance) (map[string]core.RateLimit, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.instances[inst.ID]
	if !ok {
		return nil, nil
	}
	return cur.srv.enforcedRates(), nil
}

// ensureHostKeyLocked loads the node's host keys, mints whichever of the two
// families is missing, and stores the set: what every client pinned is kept.
func (c *Core) ensureHostKeyLocked() error {
	if len(c.hostKeys) > 0 {
		return nil
	}
	var stored string
	if c.deps.LoadHostKey != nil {
		var err error
		if stored, err = c.deps.LoadHostKey(); err != nil {
			return fmt.Errorf("ssh: load host key: %w", err)
		}
	}
	keys := parseHostKeys(stored)
	minted := false
	for _, algo := range hostKeyAlgos {
		if keys.has(algo) {
			continue
		}
		if err := keys.mint(algo); err != nil {
			return fmt.Errorf("ssh: mint %s host key: %w", algo, err)
		}
		minted = true
	}
	if minted && c.deps.StoreHostKey != nil {
		if err := c.deps.StoreHostKey(keys.encode()); err != nil {
			return fmt.Errorf("ssh: store host key: %w", err)
		}
	}
	c.hostKeys = keys.signers()
	return nil
}

// HostKeyFingerprint is the ed25519 key's, the one a client that can negotiate
// it is shown; an operator hands it out and the exits pin it.
func (c *Core) HostKeyFingerprint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.hostKeys) == 0 {
		return ""
	}
	return gossh.FingerprintSHA256(c.hostKeys[0].PublicKey())
}

const tokenAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func randomToken(n int) string {
	out := make([]byte, n)
	for i := range out {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(tokenAlphabet))))
		if err != nil {
			panic(err)
		}
		out[i] = tokenAlphabet[idx.Int64()]
	}
	return string(out)
}

// Health is per listener: the server is the panel's own process, so alive and
// ready are one question — is the listener still open.
func (c *Core) Health(context.Context) (core.Health, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]int, 0, len(c.instances)+len(c.failed))
	for id := range c.instances {
		ids = append(ids, id)
	}
	for id := range c.failed {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var h core.Health
	for _, id := range ids {
		cur, ok := c.instances[id]
		if !ok {
			bind := c.failed[id]
			h.Instances = append(h.Instances, core.InstanceHealth{InstanceID: id, Tag: bind.tag, State: core.HealthFailed, Detail: bind.err.Error(), PID: os.Getpid()})
			continue
		}
		ih := core.InstanceHealth{InstanceID: id, Tag: cur.inst.Tag, PID: os.Getpid(), SinceUnixMilli: cur.srv.opened.UnixMilli()}
		if cur.srv.listening() {
			ih.State, ih.Ready = core.HealthRunning, true
		} else {
			ih.State, ih.Detail = core.HealthStopped, "listener closed"
		}
		h.Instances = append(h.Instances, ih)
	}
	return h, nil
}
