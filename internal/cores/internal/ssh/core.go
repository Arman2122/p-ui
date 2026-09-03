package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
)

// Deps are the panel-side facts the core cannot hold itself: where the host
// key lives across restarts, and who wants to hear about a refused login.
type Deps struct {
	LoadHostKey  func() (string, error)
	StoreHostKey func(pem string) error
	AuthFailure  func(AuthFailure)
	// Dial replaces the destination dialer; nil dials directly.
	Dial Dialer
}

// Core is the in-process SSH server behind the contract.
type Core struct {
	deps Deps

	mu        sync.Mutex
	hostKey   gossh.Signer
	instances map[int]*instance
	counter   *core.Counter
	epoch     string
	starts    int
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

// New returns the core; nothing listens until the first Reconcile.
func New(deps Deps) *Core {
	return &Core{deps: deps, instances: map[int]*instance{}, counter: core.NewCounter(), epoch: strconv.FormatInt(time.Now().UnixNano(), 36)}
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
		if err := c.ensureLocked(d); err != nil {
			failures = append(failures, err)
		}
	}
	for id, cur := range c.instances {
		if _, ok := want[id]; !ok {
			cur.srv.stop()
			delete(c.instances, id)
		}
	}
	return errors.Join(failures...)
}

func (c *Core) ensureLocked(d core.Instance) error {
	if !d.Enable {
		if cur, ok := c.instances[d.ID]; ok {
			cur.srv.stop()
			delete(c.instances, d.ID)
		}
		return nil
	}
	settings, err := parseSettings(d.Settings)
	if err != nil {
		return fmt.Errorf("ssh: inbound %d: %w", d.ID, err)
	}
	fp := fingerprint(d, settings)
	if cur, ok := c.instances[d.ID]; ok {
		if cur.fingerprint == fp {
			cur.inst = d
			return cur.srv.setUsers(d.Users)
		}
		cur.srv.stop()
		delete(c.instances, d.ID)
	}
	srv := newServer(d.ID, d.Tag, settings, c.hostKey, c.deps.Dial, c.deps.AuthFailure)
	userErr := srv.setUsers(d.Users)
	if _, err := listen(listenSpec{listen: d.Listen, port: d.Port}, srv); err != nil {
		return fmt.Errorf("ssh: inbound %d: listen: %w", d.ID, err)
	}
	c.starts++
	c.instances[d.ID] = &instance{inst: d, settings: settings, fingerprint: fp, srv: srv}
	logger.Infof("ssh: inbound %d (%s) listening on %s:%d over %s", d.ID, d.Tag, d.Listen, d.Port, settings.Transport)
	return userErr
}

func (c *Core) StopAll(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, cur := range c.instances {
		cur.srv.stop()
		delete(c.instances, id)
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
	case usersFingerprint(before.Users) != usersFingerprint(after.Users):
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
	return c.ensureLocked(inst)
}

func (c *Core) DropInstance(_ context.Context, inst core.Instance) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur, ok := c.instances[inst.ID]; ok {
		cur.srv.stop()
		delete(c.instances, inst.ID)
	}
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

// ensureHostKeyLocked loads the node's key or mints one and stores it, so the
// fingerprint every client pinned survives a reinstall.
func (c *Core) ensureHostKeyLocked() error {
	if c.hostKey != nil {
		return nil
	}
	if c.deps.LoadHostKey != nil {
		stored, err := c.deps.LoadHostKey()
		if err != nil {
			return fmt.Errorf("ssh: load host key: %w", err)
		}
		if stored != "" {
			signer, err := gossh.ParsePrivateKey([]byte(stored))
			if err == nil {
				c.hostKey = signer
				return nil
			}
			logger.Warning("ssh: the stored host key does not parse and is replaced:", err)
		}
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := gossh.MarshalPrivateKey(priv, "p-ui")
	if err != nil {
		return err
	}
	encoded := string(pem.EncodeToMemory(block))
	if c.deps.StoreHostKey != nil {
		if err := c.deps.StoreHostKey(encoded); err != nil {
			return fmt.Errorf("ssh: store host key: %w", err)
		}
	}
	signer, err := gossh.ParsePrivateKey([]byte(encoded))
	if err != nil {
		return err
	}
	c.hostKey = signer
	return nil
}

// HostKeyFingerprint is what a client pins; shown so an operator can hand it out.
func (c *Core) HostKeyFingerprint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hostKey == nil {
		return ""
	}
	return gossh.FingerprintSHA256(c.hostKey.PublicKey())
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
