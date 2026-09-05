package openvpn

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores/internal/unithealth"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/radius"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

var errNoRoom = errors.New("openvpn: the tunnel subnet has no free address")

// PluginPath is where the distribution installs openvpn-auth-radius.
const PluginPath = "/usr/lib/openvpn/radiusplugin.so"

// Deps are the host facts the core cannot decide itself.
type Deps struct {
	// Radius is the panel's embedded server; every inbound registers a sink on it.
	Radius *radius.Server
	// Backend runs the daemons; nil selects the exec backend.
	Backend supervisor.Backend
	// User and Group are the daemon identity under systemd.
	User  string
	Group int
	// AuthFailure hears every refused login.
	AuthFailure func(instanceID int, username, ip, reason string)
	// Admit asks the panel to decide a login against the client's cap; nil admits
	// every login, which is what a core built without a panel does.
	Admit core.AdmitFunc
	// Binary and StateDir override the daemon and the state directory.
	Binary   string
	StateDir string
}

// Core is the OpenVPN adapter: one supervised daemon per inbound, its users
// answered by a RADIUS sink, its bytes and sessions read from a ledger.
type Core struct {
	deps    Deps
	backend supervisor.Backend

	mu        sync.Mutex
	instances map[int]*instance
	exits     map[int]*exitState
	starts    int
	active    map[string]struct{}
}

type instance struct {
	inst       core.Instance
	settings   Settings
	userByName map[string]core.User
	ledger     *radius.Ledger
	paths      paths
	specHash   string
	configHash string
	running    bool
	// output is the exec backend's stderr file, held so a restart closes the old one.
	output *os.File
}

// New returns the core; nothing runs until the first Reconcile.
func New(deps Deps) *Core {
	if deps.Group == 0 {
		deps.Group = -1
	}
	backend := deps.Backend
	if backend == nil {
		backend = supervisor.NewExec()
	}
	return &Core{deps: deps, backend: backend, instances: map[int]*instance{}, exits: map[int]*exitState{}}
}

func (c *Core) Kinds() []core.Kind { return []core.Kind{Kind} }

func (c *Core) Describe() core.Descriptor {
	return core.Descriptor{
		ID:       ID,
		TitleKey: "cores.openvpn.title",
		Caps: core.Capabilities{
			// Users live in the sink; a login never touches the daemon's config.
			UserHotAdd:    core.Yes(),
			PerUserStats:  core.Yes(),
			QuotaPushdown: core.No(),
			OnlineUsers:   core.Yes(),
			ShareLink:     core.Yes(),
		},
	}
}

// Preflight: the daemon and the plugin both come from the distribution.
func (c *Core) Preflight(context.Context) error {
	if _, err := os.Stat(c.binaryPath()); err != nil {
		return fmt.Errorf("openvpn: %w (apt-get install openvpn)", err)
	}
	if _, err := os.Stat(PluginPath); err != nil {
		return fmt.Errorf("openvpn: %w (apt-get install openvpn-auth-radius)", err)
	}
	if c.deps.Radius == nil {
		return errors.New("openvpn: no RADIUS server to authenticate against")
	}
	return nil
}

func (c *Core) binaryPath() string {
	if c.deps.Binary != "" {
		return c.deps.Binary
	}
	if p, err := exec.LookPath("openvpn"); err == nil {
		return p
	}
	return "/usr/sbin/openvpn"
}

func (c *Core) stateDir() string {
	if c.deps.StateDir != "" {
		return c.deps.StateDir
	}
	if info, err := os.Stat("/var/lib/p-ui"); err == nil && info.IsDir() {
		return "/var/lib/p-ui/openvpn"
	}
	return filepath.Join(config.GetBinFolderPath(), "openvpn")
}

func (c *Core) authFailure(id int, username string, source netip.Addr, reason string) {
	if c.deps.AuthFailure == nil {
		return
	}
	ip := ""
	if source.IsValid() {
		ip = source.String()
	}
	c.deps.AuthFailure(id, username, ip, reason)
}

// OutlivesPanel: under systemd the daemons keep serving through a panel
// restart, and their sessions keep being accounted once the panel is back.
func (c *Core) OutlivesPanel() bool { return c.backend.Name() == string(supervisor.ModeSystemd) }

func (c *Core) SharesFile(core.Kind) bool { return true }

func (c *Core) Reconcile(ctx context.Context, desired []core.Instance) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	want := map[int]struct{}{}
	var failures []error
	for _, d := range desired {
		want[d.ID] = struct{}{}
		if err := c.applyLocked(ctx, d); err != nil {
			failures = append(failures, err)
		}
	}
	for id := range c.instances {
		if _, ok := want[id]; !ok {
			failures = append(failures, c.dropLocked(ctx, id))
		}
	}
	failures = append(failures, c.sweepOrphansLocked(ctx, want))
	return errors.Join(failures...)
}

func (c *Core) sweepOrphansLocked(ctx context.Context, want map[int]struct{}) error {
	names, err := c.backend.List(ctx)
	if err != nil {
		return nil
	}
	prefix := supervisor.UnitName(string(ID), 0)
	prefix = prefix[:len(prefix)-1]
	var failures []error
	for _, name := range names {
		id, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
		if !strings.HasPrefix(name, prefix) || err != nil {
			continue
		}
		if _, ok := want[id]; ok {
			continue
		}
		if err := c.backend.Stop(ctx, name); err != nil {
			failures = append(failures, fmt.Errorf("openvpn: stop orphan %s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

/*
applyLocked converges one inbound: the sink learns the users at once (a login
never waits for the daemon), the files are rendered, and the daemon is
started or, when a listener-level field changed, restarted. Users changing
alone touch nothing on disk.
*/
func (c *Core) applyLocked(ctx context.Context, d core.Instance) error {
	if !d.Enable {
		return c.dropLocked(ctx, d.ID)
	}
	settings, err := parseSettings(d.Settings)
	if err != nil {
		return fmt.Errorf("openvpn: inbound %d: %w", d.ID, err)
	}
	if settings.ServerCert == "" || settings.ServerKey == "" || settings.TLSCryptV2ServerKey == "" {
		return fmt.Errorf("openvpn: inbound %d: server identity not minted yet; save the inbound", d.ID)
	}
	cur, ok := c.instances[d.ID]
	if !ok {
		cur = &instance{ledger: radius.NewLedger(d.Tag), paths: pathsIn(filepath.Join(c.stateDir(), strconv.Itoa(d.ID)))}
		cur.ledger.Stale = 3 * interimSeconds * time.Second
		c.instances[d.ID] = cur
	}
	cur.inst, cur.settings = d, settings
	cur.userByName = usersByName(d.Users)
	if c.deps.Radius != nil {
		if err := c.deps.Radius.Register(radius.NASIdentifier(string(ID), d.ID), &sink{c: c, id: d.ID}); err != nil {
			return fmt.Errorf("openvpn: inbound %d: %w", d.ID, err)
		}
	}
	if err := c.writeFilesLocked(cur); err != nil {
		return fmt.Errorf("openvpn: inbound %d: %w", d.ID, err)
	}
	spec := c.spec(cur)
	// The flag says what the panel last did; whether the daemon is still there
	// only the backend knows, and a unit it reports down is started, not kept.
	if cur.running && !c.unitRunning(ctx, spec.Name) {
		cur.running = false
	}
	if cur.running && spec.Hash() == cur.specHash {
		return nil
	}
	// Opened only for a start: Hash ignores Output, and a sink opened on every
	// pass leaked a descriptor and truncated the log each 10 s.
	spec.Output = c.stderrSink(cur)
	if err := c.backend.Ensure(ctx, spec); err != nil {
		return fmt.Errorf("openvpn: inbound %d: %w", d.ID, err)
	}
	c.starts++
	cur.running, cur.specHash = true, spec.Hash()
	logger.Infof("openvpn: inbound %d (%s) %s on %s:%d, device %s", d.ID, d.Tag, settings.Proto, listenLabel(d.Listen), d.Port, deviceName(d.ID))
	return nil
}

// stderrSink keeps what the daemon prints before its log file opens: an
// options error is the usual reason a unit dies at once, and it goes there.
func (c *Core) stderrSink(cur *instance) io.Writer {
	if cur.output != nil {
		_ = cur.output.Close()
		cur.output = nil
	}
	f, err := os.OpenFile(filepath.Join(cur.paths.dir, "openvpn.err"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return nil
	}
	_ = c.chgrp(f.Name())
	cur.output = f
	return f
}

func listenLabel(listen string) string {
	if listen == "" {
		return "*"
	}
	return listen
}

func usersByName(users []core.User) map[string]core.User {
	out := make(map[string]core.User, len(users))
	for _, u := range users {
		if name := core.CredString(u.Credentials, core.CredUsername); name != "" {
			out[name] = u
		}
	}
	return out
}

// writeFilesLocked lands the identity, the two configs and the directories
// the daemon writes into, all readable by the daemon user and no one else.
func (c *Core) writeFilesLocked(cur *instance) error {
	p := cur.paths
	for _, dir := range []string{p.dir, p.ccd, p.tmp} {
		if err := os.MkdirAll(dir, 0o770); err != nil {
			return err
		}
		// MkdirAll is subject to the umask; the daemon user must create its log,
		// status file, socket and ccd entries here, so the mode is set explicitly.
		if err := os.Chmod(dir, 0o770); err != nil {
			return err
		}
		if err := c.chgrp(dir); err != nil {
			return err
		}
	}
	nas := radius.NASIdentifier(string(ID), cur.inst.ID)
	authAddr, acctAddr, secret := radius.DefaultAuthAddr, radius.DefaultAcctAddr, ""
	if c.deps.Radius != nil {
		authAddr, acctAddr, secret = c.deps.Radius.AuthAddr(), c.deps.Radius.AcctAddr(), c.deps.Radius.Secret()
	}
	pluginCnf, err := renderPlugin(nas, authAddr, acctAddr, secret, cur.settings, p)
	if err != nil {
		return err
	}
	for path, body := range map[string]string{
		p.cert:   cur.settings.ServerCert,
		p.key:    cur.settings.ServerKey,
		p.tc2:    cur.settings.TLSCryptV2ServerKey,
		p.plugin: pluginCnf,
		p.conf:   renderServer(cur.inst, cur.settings, p, PluginPath),
	} {
		if err := c.writeFile(path, []byte(body)); err != nil {
			return err
		}
	}
	// The unit carries the rendered config's hash: OpenVPN cannot reload, so a
	// changed file must change the spec, which is what restarts the daemon.
	sum := sha256.Sum256([]byte(renderServer(cur.inst, cur.settings, p, PluginPath) + cur.settings.ServerCert + cur.settings.TLSCryptV2ServerKey))
	cur.configHash = hex.EncodeToString(sum[:8])
	return nil
}

func (c *Core) chgrp(path string) error {
	if c.deps.Group < 0 {
		return nil
	}
	if err := os.Chown(path, -1, c.deps.Group); err != nil && !os.IsPermission(err) {
		return fmt.Errorf("openvpn: chgrp %s: %w", path, err)
	}
	return nil
}

func (c *Core) writeFile(path string, body []byte) error {
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, body) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return err
	}
	if err := c.chgrp(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// spec is the unit: the daemon as p-ui-core with the two capabilities a tun
// server needs, under systemd; the exec backend runs it as the panel.
func (c *Core) spec(cur *instance) supervisor.Spec {
	s := supervisor.Spec{
		Name:               supervisor.UnitName(string(ID), cur.inst.ID),
		Description:        fmt.Sprintf("Penhoon UI openvpn inbound %d", cur.inst.ID),
		Exec:               []string{c.binaryPath(), "--config", cur.paths.conf},
		Env:                []string{"PUI_CONFIG_HASH=" + cur.configHash},
		WorkingDir:         cur.paths.dir,
		Restart:            supervisor.RestartOnFailure,
		RestartAfter:       2 * time.Second,
		StartLimitInterval: time.Minute,
		StartLimitBurst:    10,
		StopTimeout:        15 * time.Second,
		KillSignal:         syscall.SIGTERM,
	}
	if c.backend.Name() == string(supervisor.ModeSystemd) && c.deps.User != "" {
		s.User = c.deps.User
		s.Capabilities = []string{"CAP_NET_ADMIN", "CAP_NET_BIND_SERVICE"}
	}
	return s
}

// unitRunning asks the backend, whose answer outlives the flag: under exec a
// daemon that exits stays down, and under systemd one past its start limit fails.
func (c *Core) unitRunning(ctx context.Context, unit string) bool {
	st, err := c.backend.Status(ctx, unit)
	return err == nil && st.State == supervisor.StateRunning
}

func (c *Core) dropLocked(ctx context.Context, id int) error {
	cur, ok := c.instances[id]
	if !ok {
		return nil
	}
	delete(c.instances, id)
	if cur.output != nil {
		_ = cur.output.Close()
	}
	if c.deps.Radius != nil {
		c.deps.Radius.Unregister(radius.NASIdentifier(string(ID), id))
	}
	if !cur.running {
		return nil
	}
	if err := c.backend.Stop(ctx, supervisor.UnitName(string(ID), id)); err != nil {
		return fmt.Errorf("openvpn: stop inbound %d: %w", id, err)
	}
	return nil
}

func (c *Core) StopAll(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var failures []error
	for id := range c.instances {
		failures = append(failures, c.dropLocked(ctx, id))
	}
	for id := range c.exits {
		failures = append(failures, c.dropExitLocked(ctx, id))
	}
	return errors.Join(failures...)
}

// PlanChange: a listener or identity field restarts the daemon; users are
// the sink's and never reach the daemon, so their edits are hot.
func (c *Core) PlanChange(before, after core.Instance) core.Action {
	switch {
	case before.Enable != after.Enable, before.Port != after.Port, before.Listen != after.Listen, before.Settings != after.Settings:
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
	return c.applyLocked(ctx, inst)
}

func (c *Core) DropInstance(ctx context.Context, inst core.Instance) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropLocked(ctx, inst.ID)
}

// AddUser is hot: the sink admits the user from now on. A user whose account
// changed underneath a session (disabled, new password) has it ended.
func (c *Core) AddUser(ctx context.Context, inst core.Instance, user core.User) error {
	c.mu.Lock()
	cur, ok := c.instances[inst.ID]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("openvpn: inbound %d is not running", inst.ID)
	}
	cur.inst.Users = append(withoutEmail(cur.inst.Users, user.Email), user)
	cur.userByName = usersByName(cur.inst.Users)
	sock := cur.paths.mgmt
	c.mu.Unlock()
	if !user.Enable {
		return c.endSessions(ctx, sock, user)
	}
	return nil
}

func (c *Core) RemoveUser(ctx context.Context, inst core.Instance, email string) error {
	c.mu.Lock()
	cur, ok := c.instances[inst.ID]
	if !ok {
		c.mu.Unlock()
		return nil
	}
	var gone core.User
	for _, u := range cur.inst.Users {
		if u.Email == email {
			gone = u
		}
	}
	cur.inst.Users = withoutEmail(cur.inst.Users, email)
	cur.userByName = usersByName(cur.inst.Users)
	sock := cur.paths.mgmt
	c.mu.Unlock()
	if gone.Email == "" {
		return nil
	}
	return c.endSessions(ctx, sock, gone)
}

func (c *Core) endSessions(ctx context.Context, sock string, user core.User) error {
	name := core.CredString(user.Credentials, core.CredUsername)
	if name == "" {
		return nil
	}
	if err := killSessions(ctx, sock, name); err != nil {
		return fmt.Errorf("openvpn: ending sessions of %s: %w", user.Email, err)
	}
	return nil
}

// admit asks the panel to decide a login; nil deps or no cap admits at once.
func (c *Core) admit(ctx context.Context, id int, user core.User, source netip.Addr, sessions []core.Session) error {
	if c.deps.Admit == nil || user.LimitIP <= 0 {
		return nil
	}
	return c.deps.Admit(ctx, core.Admission{
		Kind: Kind, InstanceID: id, Email: user.Email, Source: source,
		Held: core.HeldSources(sessions, user.Email), LimitIP: user.LimitIP,
	})
}

// AdmitsAtLogin: the RADIUS reply refuses an over-cap device, but only where a
// panel wired the callback that decides it.
func (c *Core) AdmitsAtLogin(kind core.Kind) bool { return c.deps.Admit != nil && kind == Kind }

// CutSessions ends the client's sessions from one source through the management
// socket. By source, so the device over the cap goes and the rest stay up.
func (c *Core) CutSessions(ctx context.Context, email string, source netip.Addr) (int, error) {
	c.mu.Lock()
	type target struct{ sock, cn string }
	var targets []target
	for _, cur := range c.instances {
		for _, u := range cur.inst.Users {
			if u.Email != email {
				continue
			}
			if cn := core.CredString(u.Credentials, core.CredUsername); cn != "" {
				targets = append(targets, target{sock: cur.paths.mgmt, cn: cn})
			}
		}
	}
	c.mu.Unlock()
	cut := 0
	for _, t := range targets {
		n, err := cutSource(ctx, t.sock, t.cn, source)
		if err != nil {
			return cut, err
		}
		cut += n
	}
	return cut, nil
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

// CollectTraffic drains every inbound's ledger: what the daemon reported
// since the last pass, each byte once.
func (c *Core) CollectTraffic(context.Context) ([]core.TrafficDelta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []core.TrafficDelta
	ids := make([]int, 0, len(c.instances))
	for id := range c.instances {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	c.active = map[string]struct{}{}
	for _, id := range ids {
		for _, d := range c.instances[id].ledger.Drain() {
			out = append(out, d)
			c.active[d.Email] = struct{}{}
		}
	}
	return out, nil
}

func (c *Core) Sessions(context.Context) ([]core.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []core.Session
	for _, cur := range c.instances {
		out = append(out, cur.ledger.Sessions()...)
	}
	return out, nil
}

func (c *Core) OnlineEmails(ctx context.Context) ([]string, error) {
	sessions, _ := c.Sessions(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]struct{}{}
	for email := range c.active {
		seen[email] = struct{}{}
	}
	for _, s := range sessions {
		seen[s.Email] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for email := range seen {
		out = append(out, email)
	}
	sort.Strings(out)
	return out, nil
}

// IngressSelector: decrypted traffic appears on the tun device, so a rule
// selects it the way it selects kernel WireGuard's.
func (c *Core) IngressSelector(kind core.Kind) core.IngressSelector {
	if kind != Kind {
		return core.IngressNone
	}
	return core.IngressDevice
}

func (c *Core) IngressCriteria(kind core.Kind) []core.Criterion {
	if kind != Kind {
		return nil
	}
	return append(core.CriteriaByAddress(), core.CriterionDomain)
}

func (c *Core) IngressHandle(_ context.Context, inst core.Instance) (core.IngressHandle, error) {
	return core.IngressHandle{Device: deviceName(inst.ID)}, nil
}

// ShapingSelector: every client is one tunnel address on the device.
func (c *Core) ShapingSelector(kind core.Kind) core.Selector {
	if kind != Kind {
		return core.SelectorNone
	}
	return core.SelectorInnerIP
}

// ShapingTargets keys each client by the tunnel address its live session
// holds, read from the accounting the daemon sent; a client with no session
// is not shaped, and cannot be, since it has no address yet.
func (c *Core) ShapingTargets(_ context.Context, inst core.Instance) (core.ShapingTarget, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.instances[inst.ID]
	if !ok || !cur.running {
		return core.ShapingTarget{}, nil
	}
	target := core.ShapingTarget{Device: deviceName(inst.ID), Selector: core.SelectorInnerIP}
	for _, sess := range cur.ledger.Sessions() {
		if len(sess.Local) == 0 {
			continue
		}
		if target.Keys == nil {
			target.Keys = map[string]core.SubjectKey{}
		}
		key := target.Keys[sess.Email]
		for _, local := range sess.Local {
			key.Prefixes = append(key.Prefixes, netip.PrefixFrom(local, 32))
		}
		target.Keys[sess.Email] = key
	}
	return target, nil
}

/*
MintClientCredentials: a login name and password when missing, and the
client's tls-crypt-v2 key wrapped by this inbound's server key, minted with
the daemon's own tool because the wrapping is its format. The key is per
inbound, which is why it rides client_credentials rather than a column.
*/
func (c *Core) MintClientCredentials(kind core.Kind, settings string, have map[string]string) (map[string]string, error) {
	minted := map[string]string{}
	if kind != Kind {
		return minted, nil
	}
	if have[core.CredUsername] == "" {
		minted[core.CredUsername] = "u" + randomToken(7)
	}
	if len(have[core.CredPassword]) < 8 {
		minted[core.CredPassword] = randomToken(16)
	}
	// The wrapped key needs the inbound's server key; a client minted before the
	// inbound has one (or with no settings at all) gets it on its next save.
	if serverKey := tlsCryptV2ServerKeyOf(settings); have[core.CredTLSCryptV2] == "" && serverKey != "" {
		s := Settings{TLSCryptV2ServerKey: serverKey}
		name := have[core.CredUsername]
		if name == "" {
			name = minted[core.CredUsername]
		}
		key, err := c.mintClientKey(s.TLSCryptV2ServerKey, name)
		if err != nil {
			return nil, err
		}
		minted[core.CredTLSCryptV2] = key
	}
	return minted, nil
}

func (c *Core) mintClientKey(serverKey, username string) (string, error) {
	dir, err := os.MkdirTemp("", "p-ui-tc2-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	serverFile, clientFile := filepath.Join(dir, "server.key"), filepath.Join(dir, "client.key")
	if err := os.WriteFile(serverFile, []byte(serverKey), 0o600); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(context.Background(), c.binaryPath(), "--tls-crypt-v2", serverFile, "--genkey", "tls-crypt-v2-client", clientFile, "p-ui:"+username)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("openvpn: mint client key: %w: %s", err, strings.TrimSpace(string(out)))
	}
	body, err := os.ReadFile(clientFile)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *Core) ValidateClient(kind core.Kind, _, email string, have map[string]string) error {
	if kind != Kind {
		return nil
	}
	_, err := credentialOf(core.User{Email: email, Enable: true, Credentials: map[string]any{
		core.CredUsername: have[core.CredUsername], core.CredPassword: have[core.CredPassword],
	}})
	return err
}

const tokenAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func randomToken(n int) string {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	out := make([]byte, n)
	for i, b := range raw {
		out[i] = tokenAlphabet[int(b)%len(tokenAlphabet)]
	}
	return string(out)
}

// tlsCryptV2ServerKeyOf reads the one field client minting needs without
// demanding the rest of the settings be valid yet.
func tlsCryptV2ServerKeyOf(settings string) string {
	var doc struct {
		Key string `json:"tlsCryptV2ServerKey"`
	}
	_ = json.Unmarshal([]byte(settings), &doc)
	return doc.Key
}

// Health reads each unit from the backend and asks the daemon itself whether it
// answers on its management socket, which opens only once the config loaded.
func (c *Core) Health(ctx context.Context) (core.Health, error) {
	c.mu.Lock()
	ids := make([]int, 0, len(c.instances))
	socks := make(map[int]string, len(c.instances))
	tags := make(map[int]string, len(c.instances))
	for id, cur := range c.instances {
		ids = append(ids, id)
		socks[id], tags[id] = cur.paths.mgmt, cur.inst.Tag
	}
	c.mu.Unlock()
	sort.Ints(ids)
	var h core.Health
	for _, id := range ids {
		ih, err := unithealth.Of(ctx, c.backend, supervisor.UnitName(string(ID), id))
		if err != nil {
			return core.Health{}, fmt.Errorf("openvpn: inbound %d: %w", id, err)
		}
		ih.InstanceID, ih.Tag = id, tags[id]
		if ih.State == core.HealthRunning {
			conn, _, err := management(ctx, socks[id])
			switch {
			case err != nil:
				ih.Detail = "the management socket does not answer: " + err.Error()
			case conn == nil:
				ih.Detail = "the management socket is not open yet"
			default:
				_ = conn.Close()
				ih.Ready = true
			}
		}
		h.Instances = append(h.Instances, ih)
	}
	return h, nil
}

// TailLog is the unit's last lines from the backend: the journal under systemd,
// the panel's own ring under exec.
func (c *Core) TailLog(ctx context.Context, instanceID, n int) ([]string, error) {
	if instanceID <= 0 {
		return nil, fmt.Errorf("openvpn: logs are per inbound; %d names none", instanceID)
	}
	return c.backend.Logs(ctx, supervisor.UnitName(string(ID), instanceID), n)
}
