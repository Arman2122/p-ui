package openvpn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

/*
The exit half: the panel runs an OpenVPN CLIENT towards a provider, and the tun
it brings up is a device routing rules send traffic into. The plane fills the
exit's table with that device by name (drivers/coretun) and marks the sockets
that want it; this file owns the daemon and the files it reads.
*/

// ExitSettings is the egress row's settings JSON for an openvpn exit: the
// provider's .ovpn as handed out, plus the login it asks for.
type ExitSettings struct {
	Profile  string `json:"profile"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ExitDevicePrefix names an exit's tun: pvx<id>. Its own namespace, apart from
// the inbounds' pov<id>, so an exit and an inbound of one id never share a name.
const ExitDevicePrefix = "pvx"

func exitDeviceName(id int) string { return ExitDevicePrefix + strconv.Itoa(id) }

// exitUnitName keeps exits out of the inbound sweep, which reads the number
// after the core's own prefix: p-ui-openvpn-exit-5 parses as no inbound at all.
func exitUnitName(id int) string { return supervisor.UnitName(string(ID)+"-exit", id) }

type exitState struct {
	settings   ExitSettings
	dir        string
	conf       string
	auth       string
	configHash string
	specHash   string
	// output is the exec backend's stderr file, held so a restart closes the old one.
	output *os.File
}

// exitLogin is the username and password the daemon presents: the settings
// first, then the inline block the profile itself may carry.
func (s ExitSettings) exitLogin(p exitProfile) (string, string, error) {
	switch {
	case s.Username != "" && s.Password == "":
		return "", "", errors.New("openvpn exit: a username needs a password")
	case s.Username != "":
		return s.Username, s.Password, nil
	case p.login[0] != "":
		return p.login[0], p.login[1], nil
	case p.wantsLogin:
		return "", "", errProfileNoLogin
	}
	return "", "", nil
}

func parseExitSettings(raw string) (ExitSettings, exitProfile, error) {
	var s ExitSettings
	if strings.TrimSpace(raw) == "" {
		return s, exitProfile{}, errors.New("openvpn exit: no settings")
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, exitProfile{}, fmt.Errorf("openvpn exit: settings: %w", err)
	}
	s.Username = strings.TrimSpace(s.Username)
	profile, err := parseExitProfile(s.Profile)
	if err != nil {
		return s, profile, err
	}
	if _, _, err := s.exitLogin(profile); err != nil {
		return s, profile, err
	}
	return s, profile, nil
}

func (c *Core) ExitKinds() []core.Kind { return []core.Kind{Kind} }

// ExitHandleKind: the client makes a kernel device, and a device is what the
// router sends marked traffic into.
func (c *Core) ExitHandleKind(kind core.Kind) core.ExitHandleKind {
	if kind != Kind {
		return core.ExitNone
	}
	return core.ExitDevice
}

/*
ExitHandle is pure: the name is a function of the row id, so a compile or a
preview never starts anything. SourceOwnerDaemon because what reaches the
device is a marked socket this host originated, whose source the kernel picks
from the device at route lookup; nothing has to translate it.
*/
func (c *Core) ExitHandle(_ context.Context, exit core.Exit) (core.ExitHandle, error) {
	if exit.Kind != Kind {
		return core.ExitHandle{}, nil
	}
	return core.ExitHandle{Device: exitDeviceName(exit.ID), Source: core.SourceOwnerDaemon}, nil
}

func (c *Core) ValidateExit(exit core.Exit) error {
	if exit.Kind != Kind {
		return nil
	}
	if _, _, err := parseExitSettings(exit.Settings); err != nil {
		return err
	}
	if _, err := os.Stat(c.binaryPath()); err != nil {
		return fmt.Errorf("openvpn exit: %w (apt-get install openvpn)", err)
	}
	return nil
}

// ReconcileExits converges the client daemons on desired: a changed row is
// restarted, an unchanged one left connected, a missing one stopped.
func (c *Core) ReconcileExits(ctx context.Context, desired []core.Exit) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	want := map[int]struct{}{}
	var failures []error
	for _, exit := range desired {
		if exit.Kind != Kind || !exit.Enable {
			continue
		}
		want[exit.ID] = struct{}{}
		if err := c.ensureExitLocked(ctx, exit); err != nil {
			failures = append(failures, fmt.Errorf("openvpn: exit %d: %w", exit.ID, err))
		}
	}
	for id := range c.exits {
		if _, ok := want[id]; !ok {
			failures = append(failures, c.dropExitLocked(ctx, id))
		}
	}
	failures = append(failures, c.sweepExitOrphansLocked(ctx, want))
	return errors.Join(failures...)
}

func (c *Core) ensureExitLocked(ctx context.Context, exit core.Exit) error {
	settings, profile, err := parseExitSettings(exit.Settings)
	if err != nil {
		return err
	}
	cur, ok := c.exits[exit.ID]
	if !ok {
		dir := filepath.Join(c.stateDir(), "exit-"+strconv.Itoa(exit.ID))
		cur = &exitState{dir: dir, conf: filepath.Join(dir, "client.conf"), auth: filepath.Join(dir, "auth")}
		c.exits[exit.ID] = cur
	}
	cur.settings = settings
	if err := c.writeExitFilesLocked(exit.ID, cur, profile); err != nil {
		return err
	}
	spec := c.exitSpec(exit.ID, cur)
	if cur.specHash == spec.Hash() {
		return nil
	}
	// Opened only for a start: Hash ignores Output, and a sink opened on every
	// pass would leak a descriptor and truncate the log each time.
	c.attachExitOutput(cur)
	if err := c.backend.Ensure(ctx, spec); err != nil {
		return err
	}
	cur.specHash = spec.Hash()
	logger.Infof("openvpn: exit %d dialling through %s", exit.ID, exitDeviceName(exit.ID))
	return nil
}

// writeExitFilesLocked lands the config and, when the profile logs in, the
// credentials file, readable by the daemon user and no one else.
func (c *Core) writeExitFilesLocked(id int, cur *exitState, profile exitProfile) error {
	if err := c.ensureStateRoot(); err != nil {
		return err
	}
	if err := os.MkdirAll(cur.dir, 0o770); err != nil {
		return err
	}
	if err := os.Chmod(cur.dir, 0o770); err != nil {
		return err
	}
	if err := c.chgrp(cur.dir); err != nil {
		return err
	}
	username, password, err := cur.settings.exitLogin(profile)
	if err != nil {
		return err
	}
	auth, authPath := "", ""
	if username != "" {
		auth, authPath = username+"\n"+password+"\n", cur.auth
		if err := c.writeFile(cur.auth, []byte(auth)); err != nil {
			return err
		}
	} else if err := os.Remove(cur.auth); err != nil && !os.IsNotExist(err) {
		return err
	}
	conf := renderExitConfig(id, profile, authPath, c.hasDCO())
	if err := c.writeFile(cur.conf, []byte(conf)); err != nil {
		return err
	}
	// The unit carries the hash: OpenVPN cannot reload, so a changed file must
	// change the spec, which is what restarts the client.
	sum := sha256.Sum256([]byte(conf + auth))
	cur.configHash = hex.EncodeToString(sum[:8])
	return nil
}

// exitSpec is the unit: the client as p-ui-core with the one capability a tun
// needs, restarted whenever it exits (a provider that hung up is still wanted).
func (c *Core) exitSpec(id int, cur *exitState) supervisor.Spec {
	s := supervisor.Spec{
		Name:               exitUnitName(id),
		Description:        fmt.Sprintf("Penhoon UI openvpn exit %d", id),
		Exec:               []string{c.binaryPath(), "--config", cur.conf},
		Env:                []string{"PUI_CONFIG_HASH=" + cur.configHash},
		WorkingDir:         cur.dir,
		Restart:            supervisor.RestartAlways,
		RestartAfter:       3 * time.Second,
		StartLimitInterval: time.Minute,
		StartLimitBurst:    10,
		StopTimeout:        15 * time.Second,
		KillSignal:         syscall.SIGTERM,
	}
	if cur.output != nil {
		s.Output = cur.output
	}
	if c.backend.Name() == string(supervisor.ModeSystemd) && c.deps.User != "" {
		s.User = c.deps.User
		s.Capabilities = []string{"CAP_NET_ADMIN"}
	}
	return s
}

// attachExitOutput opens the client's stderr file for the exec backend,
// replacing the one a previous start held.
func (c *Core) attachExitOutput(cur *exitState) {
	if cur.output != nil {
		_ = cur.output.Close()
		cur.output = nil
	}
	f, err := os.OpenFile(filepath.Join(cur.dir, "openvpn.err"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return
	}
	_ = c.chgrp(f.Name())
	cur.output = f
}

// dropExitLocked stops the client and removes its directory: the credentials
// file has no business outliving the row.
func (c *Core) dropExitLocked(ctx context.Context, id int) error {
	cur, ok := c.exits[id]
	if !ok {
		return nil
	}
	delete(c.exits, id)
	if cur.output != nil {
		_ = cur.output.Close()
	}
	if err := c.backend.Stop(ctx, exitUnitName(id)); err != nil && !errors.Is(err, supervisor.ErrNotRunning) {
		return fmt.Errorf("openvpn: stop exit %d: %w", id, err)
	}
	return os.RemoveAll(cur.dir)
}

func (c *Core) sweepExitOrphansLocked(ctx context.Context, want map[int]struct{}) error {
	names, err := c.backend.List(ctx)
	if err != nil {
		return nil
	}
	prefix := strings.TrimSuffix(exitUnitName(0), "0")
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
