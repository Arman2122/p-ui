package ocserv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
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
The exit half: the panel runs an openconnect CLIENT towards an AnyConnect or
ocserv server, and the tun it brings up is a device routing rules send traffic
into. The plane fills the exit's table with that device by name
(drivers/coretun) and marks the sockets that want it; this file owns the
client process and the two files it reads.
*/

// ExitSettings is the egress row's settings JSON for an ocserv exit.
type ExitSettings struct {
	// Server is host or host:port; 443 when no port is given.
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
	// ServerCertPin is "pin-sha256:..." as openconnect prints it. Empty trusts
	// the system's CAs, which is what a publicly signed server needs.
	ServerCertPin string `json:"serverCertPin"`
	// Group answers a server that offers several; empty answers none.
	Group string `json:"group"`
}

// ExitDevicePrefix names an exit's tun: pcx<id>, apart from the inbounds' poc.
const ExitDevicePrefix = "pcx"

const exitReconnectSeconds = 300

func exitDeviceName(id int) string { return ExitDevicePrefix + strconv.Itoa(id) }

// exitUnitName keeps exits out of the inbound sweep, which reads the number
// after the core's own prefix: p-ui-ocserv-exit-5 parses as no inbound at all.
func exitUnitName(id int) string { return supervisor.UnitName(string(ID)+"-exit", id) }

type exitState struct {
	settings   ExitSettings
	dir        string
	passwd     string
	script     string
	configHash string
	specHash   string
	// output is the exec backend's stderr file, held so a restart closes the old one.
	output *os.File
}

func parseExitSettings(raw string) (ExitSettings, error) {
	var s ExitSettings
	if strings.TrimSpace(raw) == "" {
		return s, errors.New("ocserv exit: no settings")
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, fmt.Errorf("ocserv exit: settings: %w", err)
	}
	s.Server, s.Username = strings.TrimSpace(s.Server), strings.TrimSpace(s.Username)
	s.ServerCertPin, s.Group = strings.TrimSpace(s.ServerCertPin), strings.TrimSpace(s.Group)
	switch {
	case s.Server == "":
		return s, errors.New("ocserv exit: a server is required")
	case s.Username == "":
		return s, errors.New("ocserv exit: a username is required")
	case s.Password == "":
		return s, errors.New("ocserv exit: a password is required")
	case strings.ContainsAny(s.Password, "\r\n"):
		return s, errors.New("ocserv exit: the password cannot contain a line break")
	case s.ServerCertPin != "" && !strings.HasPrefix(s.ServerCertPin, "pin-sha256:"):
		return s, errors.New("ocserv exit: the certificate pin must be the pin-sha256:... openconnect prints")
	}
	if strings.Contains(s.Server, ":") {
		if _, port, err := net.SplitHostPort(s.Server); err != nil {
			return s, fmt.Errorf("ocserv exit: server %q is not host or host:port", s.Server)
		} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return s, fmt.Errorf("ocserv exit: port %q is out of range", port)
		}
	}
	return s, nil
}

func (c *Core) openconnectPath() string {
	if c.deps.Openconnect != "" {
		return c.deps.Openconnect
	}
	if p, err := exec.LookPath("openconnect"); err == nil {
		return p
	}
	return "/usr/sbin/openconnect"
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

// ExitHandle is pure, and answers SourceOwnerDaemon for the reason the OpenVPN
// exit does: a marked socket takes the device's own address at route lookup.
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
	if _, err := parseExitSettings(exit.Settings); err != nil {
		return err
	}
	if _, err := os.Stat(c.openconnectPath()); err != nil {
		return fmt.Errorf("ocserv exit: %w (apt-get install openconnect)", err)
	}
	return nil
}

// ReconcileExits converges the clients on desired: a changed row is
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
			failures = append(failures, fmt.Errorf("ocserv: exit %d: %w", exit.ID, err))
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
	settings, err := parseExitSettings(exit.Settings)
	if err != nil {
		return err
	}
	cur, ok := c.exits[exit.ID]
	if !ok {
		dir := filepath.Join(c.stateDir(), "exit-"+strconv.Itoa(exit.ID))
		cur = &exitState{dir: dir, passwd: filepath.Join(dir, "passwd"), script: filepath.Join(dir, "vpnc-script")}
		c.exits[exit.ID] = cur
	}
	cur.settings = settings
	if err := c.writeExitFilesLocked(cur); err != nil {
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
	logger.Infof("ocserv: exit %d dialling %s through %s", exit.ID, settings.Server, exitDeviceName(exit.ID))
	return nil
}

/*
exitScript is the whole of what the client may do to the host: address the
tunnel. The stock vpnc-script rewrites the default route and resolv.conf, which
on a server that is also everybody else's gateway is the outage; here the only
way into the device is the table the plane fills.
*/
const exitScript = `#!/bin/sh
# Penhoon UI: address the tunnel and nothing else; routing is the panel's.
IP=$(command -v ip || echo /sbin/ip)
case "$reason" in
connect|reconnect)
	if [ -n "$INTERNAL_IP4_MTU" ]; then
		"$IP" link set dev "$TUNDEV" up mtu "$INTERNAL_IP4_MTU"
	else
		"$IP" link set dev "$TUNDEV" up
	fi
	"$IP" -4 addr replace "$INTERNAL_IP4_ADDRESS/32" peer "$INTERNAL_IP4_ADDRESS" dev "$TUNDEV"
	;;
esac
exit 0
`

// writeExitFilesLocked lands the password and the script, readable by the
// client's user and no one else; the script must also be runnable by it.
func (c *Core) writeExitFilesLocked(cur *exitState) error {
	if err := os.MkdirAll(cur.dir, 0o770); err != nil {
		return err
	}
	if err := os.Chmod(cur.dir, 0o770); err != nil {
		return err
	}
	if err := c.chgrp(cur.dir); err != nil {
		return err
	}
	if err := c.writeFile(cur.passwd, []byte(cur.settings.Password+"\n")); err != nil {
		return err
	}
	if err := c.writeFile(cur.script, []byte(exitScript)); err != nil {
		return err
	}
	if err := os.Chmod(cur.script, 0o750); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(strings.Join(c.exitArgs(0, cur), "\x00") + cur.settings.Password + exitScript))
	cur.configHash = hex.EncodeToString(sum[:8])
	return nil
}

// exitArgs is the client's command line. The password is never on it: it is
// read from stdin, which the unit's shell opens on the file.
func (c *Core) exitArgs(id int, cur *exitState) []string {
	s := cur.settings
	args := []string{
		c.openconnectPath(), "--protocol=anyconnect", "--interface=" + exitDeviceName(id), "--script=" + cur.script,
		"--user=" + s.Username, "--passwd-on-stdin", "--non-inter", "--no-proxy", "--disable-ipv6",
		"--reconnect-timeout=" + strconv.Itoa(exitReconnectSeconds),
	}
	if s.ServerCertPin != "" {
		args = append(args, "--servercert="+s.ServerCertPin)
	}
	if s.Group != "" {
		args = append(args, "--authgroup="+s.Group)
	}
	return append(args, s.Server)
}

// exitSpec is the unit: the client as p-ui-core with the one capability a tun
// needs, restarted whenever it exits.
func (c *Core) exitSpec(id int, cur *exitState) supervisor.Spec {
	s := supervisor.Spec{
		Name:               exitUnitName(id),
		Description:        fmt.Sprintf("Penhoon UI ocserv exit %d", id),
		Exec:               append([]string{"/bin/sh", "-c", `exec "$@" <"$PUI_PASSWD_FILE"`, "openconnect"}, c.exitArgs(id, cur)...),
		Env:                []string{"PUI_CONFIG_HASH=" + cur.configHash, "PUI_PASSWD_FILE=" + cur.passwd},
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
	f, err := os.OpenFile(filepath.Join(cur.dir, "openconnect.err"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return
	}
	_ = c.chgrp(f.Name())
	cur.output = f
}

// dropExitLocked stops the client and removes its directory: the password
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
		return fmt.Errorf("ocserv: stop exit %d: %w", id, err)
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
			failures = append(failures, fmt.Errorf("ocserv: stop orphan %s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}
