//go:build linux

package nativeruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/vishvananda/netlink"

	ocserv "github.com/Arman2122/p-ui/internal/cores/internal/ocserv"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/localrpc"
	"github.com/Arman2122/p-ui/internal/node/native"
	"github.com/Arman2122/p-ui/internal/radius"
)

type ocservManager struct {
	daemonFiles
	ctx                                  context.Context
	plane                                *guardedPlane
	uid                                  uint32
	dir, executable, user, group, socket string
	server                               *http.Server
	processes                            map[uint32]*ocservProcess
	bindings                             map[string]ocservBinding
	clock                                limits.Clock
	binding                              *admission.Binding
}

type ocservProcess struct {
	config     ocserv.NodeConfiguration
	clients    map[string]ocserv.NodeClient
	generation string
	auth       *ocserv.NodeAuthenticator
	radius     *radius.Server
	command    *exec.Cmd
	done       chan struct{}
	err        error
	log        *boundedVPNLog
}

func newOcservManager(ctx context.Context, plane *guardedPlane) *ocservManager {
	uid, _ := strconv.ParseUint(os.Getenv("PUI_NATIVE_OCSERV_UID"), 10, 32)
	gid, _ := strconv.ParseUint(os.Getenv("PUI_NATIVE_OCSERV_GID"), 10, 32)
	dir := os.Getenv("PUI_NATIVE_OCSERV_DIR")
	if dir == "" {
		dir = "/run/pui-ocserv"
	}
	executable, _ := os.Executable()
	return &ocservManager{daemonFiles: daemonFiles{gid: uint32(gid)}, ctx: ctx, plane: plane, uid: uint32(uid), dir: dir, executable: executable, processes: map[uint32]*ocservProcess{}, bindings: map[string]ocservBinding{}}
}

func (m *ocservManager) preflight(configs []ocserv.NodeConfiguration) error {
	if len(configs) == 0 {
		return nil
	}
	if m.uid == 0 || m.gid == 0 || !filepath.IsAbs(m.dir) || strings.ContainsAny(m.dir, " \t\r\n\"\\") || strings.ContainsAny(m.executable, " \t\r\n\"\\") {
		return errors.New("ocserv requires a dedicated worker UID/GID and safe runtime paths")
	}
	user, err := osuser.LookupId(strconv.FormatUint(uint64(m.uid), 10))
	if err != nil {
		return err
	}
	group, err := osuser.LookupGroupId(strconv.FormatUint(uint64(m.gid), 10))
	if err != nil {
		return err
	}
	m.user, m.group = user.Username, group.Name
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/sbin/ocserv", "--version")
	log := new(boundedVPNLog)
	command.Stdout, command.Stderr = log, log
	if err := command.Run(); err != nil {
		return err
	}
	if !strings.Contains(log.String(), "OpenConnect VPN Server 1.2.4\n") {
		return errors.New("native ocserv requires qualified version 1.2.4")
	}
	var wanted []netip.Addr
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}
	for _, config := range configs {
		for _, link := range links {
			if strings.HasPrefix(link.Attrs().Name, config.Prefix()) && link.Attrs().Alias != m.plane.owner {
				return native.ErrOwnership
			}
		}
		for _, client := range config.Clients {
			for ip := client.Subnet.Addr(); client.Subnet.Contains(ip); ip = ip.Next() {
				wanted = append(wanted, ip)
			}
		}
	}
	return m.plane.preflightAddresses(wanted)
}

func (m *ocservManager) listen(handler http.Handler) error {
	if m.server != nil {
		return nil
	}
	if err := m.ensureDir(m.dir); err != nil {
		return err
	}
	control := filepath.Join(m.dir, "control")
	if err := os.MkdirAll(control, 0o700); err != nil {
		return err
	}
	m.socket = filepath.Join(control, "hooks.sock")
	listener, err := localrpc.Listen(m.ctx, m.socket, 0)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second, MaxHeaderBytes: 4096}
	m.server = server
	go func() { _ = server.Serve(listener) }()
	return nil
}

func (m *ocservManager) start(ctx context.Context, config ocserv.NodeConfiguration, grants []governor.Grant) (*ocservProcess, error) {
	if err := m.plane.gate.GuardDynamic(ctx, config.Prefix()); err != nil {
		return nil, err
	}
	dir := filepath.Join(m.dir, config.Prefix())
	if err := m.ensureDir(dir); err != nil {
		return nil, err
	}
	if err := m.ensureDir(filepath.Join(dir, "users")); err != nil {
		return nil, err
	}
	process := &ocservProcess{config: config, clients: map[string]ocserv.NodeClient{}, generation: uuid.NewString(), auth: ocserv.NewNodeAuthenticator(config), done: make(chan struct{}), log: new(boundedVPNLog)}
	if m.binding != nil {
		if err := process.auth.BindAdmission(m.binding, config.InboundTag); err != nil {
			return nil, err
		}
	}
	process.auth.SetClock(m.clock)
	for _, client := range config.Clients {
		process.clients[client.Username] = client
	}
	process.auth.Grants(grants)
	srv, err := radius.New(radius.NewSecret(), "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	process.radius = srv
	if err := srv.Register(radius.NASIdentifier("node-ocserv", int(config.ID)), process.auth); err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = srv.Close()
		}
	}()
	hook := filepath.Join(dir, "hook-"+process.generation)
	if err := m.writeFile(hook, []byte("#!/bin/sh\nexec "+m.executable+" ocserv-hook "+m.socket+" "+process.generation+"\n")); err != nil {
		return nil, err
	}
	if err := os.Chmod(hook, 0o750); err != nil {
		return nil, err
	}
	files, err := config.Files(dir, hook, m.user, m.group, srv)
	if err != nil {
		return nil, err
	}
	for path, data := range files {
		if err := m.writeFile(path, []byte(data)); err != nil {
			return nil, err
		}
		if filepath.Base(path) == "server.key" || filepath.Base(path) == "servers" {
			if err := os.Chmod(path, 0o600); err != nil {
				return nil, err
			}
		}
	}
	process.command = exec.CommandContext(m.ctx, "/usr/sbin/ocserv", "-f", "-d", "2", "-c", filepath.Join(dir, "ocserv.conf"))
	process.command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	// The pinned daemon clears worker groups only when its supervisor starts with GID 0.
	process.command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 0, Gid: 0, Groups: []uint32{}}, Pdeathsig: syscall.SIGKILL}
	process.command.Stdout, process.command.Stderr = process.log, process.log
	if err := process.command.Start(); err != nil {
		return nil, err
	}
	go func() { process.err = process.command.Wait(); close(process.done) }()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = process.stop()
			return nil, ctx.Err()
		case <-process.done:
			_ = process.stop()
			return nil, fmt.Errorf("ocserv startup: %w: %s", process.err, process.log.String())
		case <-deadline.C:
			_ = process.stop()
			return nil, fmt.Errorf("ocserv readiness timeout: %s", process.log.String())
		case <-ticker.C:
			probe, cancel := context.WithTimeout(ctx, time.Second)
			err := ocserv.NodeReady(probe, dir)
			cancel()
			if err == nil {
				success = true
				return process, nil
			}
		}
	}
}

func (p *ocservProcess) stop() error {
	_ = p.command.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = p.command.Process.Kill()
		<-p.done
	}
	return p.radius.Close()
}

func (m *ocservManager) stop() error {
	var failures []error
	for id, process := range m.processes {
		failures = append(failures, process.stop())
		delete(m.processes, id)
	}
	clear(m.bindings)
	return errors.Join(failures...)
}

func (m *ocservManager) byGeneration(generation string) *ocservProcess {
	for _, process := range m.processes {
		if process.generation == generation {
			return process
		}
	}
	return nil
}

func (m *ocservManager) startMissing(ctx context.Context, configs []ocserv.NodeConfiguration, grants []governor.Grant) error {
	for _, config := range configs {
		if m.processes[config.ID] != nil {
			continue
		}
		process, err := m.start(ctx, config, grants)
		if err != nil {
			return err
		}
		m.processes[config.ID] = process
	}
	return nil
}

func (m *ocservManager) dead() bool {
	changed := false
	for id, process := range m.processes {
		select {
		case <-process.done:
			_ = process.stop()
			delete(m.processes, id)
			changed = true
			for device, binding := range m.bindings {
				if binding.Generation == process.generation {
					delete(m.bindings, device)
				}
			}
		default:
		}
	}
	return changed
}

func (m *ocservManager) prune() bool {
	changed := m.dead()
	return m.pruneBindings() || changed
}

func (m *ocservManager) pruneBindings() bool {
	changed := false
	for device, binding := range m.bindings {
		link, err := netlink.LinkByName(device)
		if err != nil || link.Attrs().Index != binding.Index || link.Attrs().Alias != m.plane.owner {
			delete(m.bindings, device)
			changed = true
		}
	}
	return changed
}
