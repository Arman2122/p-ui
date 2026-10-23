//go:build linux

package nativeruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	openvpn "github.com/Arman2122/p-ui/internal/cores/internal/openvpn"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/localrpc"
	"github.com/Arman2122/p-ui/internal/node/native"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

type openVPNManager struct {
	daemonFiles
	ctx             context.Context
	plane           *guardedPlane
	dir, executable string
	uid             uint32
	processes       map[string]*openVPNProcess
	clock           limits.Clock
	binding         *admission.Binding
}

type daemonFiles struct{ gid uint32 }

type openVPNProcess struct {
	command  *exec.Cmd
	listener net.Listener
	conn     net.Conn
	auth     *openvpn.NodeAuthenticator
	done     chan struct{}
	err      error
	log      *boundedVPNLog
}

type boundedVPNLog struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedVPNLog) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, data...)
	if len(b.data) > 8192 {
		b.data = b.data[len(b.data)-8192:]
	}
	return len(data), nil
}
func (b *boundedVPNLog) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.data) }

func newOpenVPNManager(ctx context.Context, plane *guardedPlane) *openVPNManager {
	uid, _ := strconv.ParseUint(os.Getenv("PUI_NATIVE_OPENVPN_UID"), 10, 32)
	gid, _ := strconv.ParseUint(os.Getenv("PUI_NATIVE_OPENVPN_GID"), 10, 32)
	dir := os.Getenv("PUI_NATIVE_OPENVPN_DIR")
	if dir == "" {
		dir = "/run/pui-openvpn"
	}
	executable, _ := os.Executable()
	return &openVPNManager{daemonFiles: daemonFiles{gid: uint32(gid)}, ctx: ctx, plane: plane, dir: dir, executable: executable, uid: uint32(uid), processes: map[string]*openVPNProcess{}}
}

func (m *openVPNManager) preflight(configs []openvpn.NodeConfiguration) error {
	if len(configs) == 0 {
		return nil
	}
	if m.uid == 0 || m.gid == 0 || !filepath.IsAbs(m.dir) || strings.ContainsAny(m.dir, " \t\r\n\"\\") || strings.ContainsAny(m.executable, " \t\r\n\"\\") {
		return errors.New("OpenVPN requires a dedicated daemon UID/GID and safe absolute runtime paths")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/sbin/openvpn", "--version")
	log := new(boundedVPNLog)
	command.Stdout = log
	command.Stderr = log
	if err := command.Run(); err != nil {
		return fmt.Errorf("OpenVPN preflight: %w", err)
	}
	if !strings.HasPrefix(log.String(), "OpenVPN 2.6.") {
		return errors.New("native OpenVPN requires qualified OpenVPN 2.6.x")
	}
	var instances []engine.Instance
	for _, config := range configs {
		instance := engine.Instance{ID: int(config.ID), Address: []string{netip.PrefixFrom(config.Address(), 32).String()}}
		for _, client := range config.Clients {
			for _, ip := range client.Addresses {
				instance.Peers = append(instance.Peers, engine.Peer{AllowedIPs: []string{netip.PrefixFrom(ip, 32).String()}})
			}
		}
		instances = append(instances, instance)
	}
	return m.plane.preflight(instances)
}

func (m daemonFiles) ensureDir(path string) error {
	if err := os.Mkdir(path, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return native.ErrOwnership
	}
	if err := os.Chown(path, 0, int(m.gid)); err != nil {
		return err
	}
	return os.Chmod(path, 0o750)
}

func (m daemonFiles) writeFile(path string, data []byte) error {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o640)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != 0 || stat.Nlink != 1 || info.Mode().Perm()&0o022 != 0 {
		return native.ErrOwnership
	}
	if err := file.Chown(0, int(m.gid)); err != nil {
		return err
	}
	if err := file.Chmod(0o640); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	_, err = file.Write(data)
	return err
}

func (m *openVPNManager) tunnel(ctx context.Context, config openvpn.NodeConfiguration) error {
	link, err := m.plane.owned(config.Device())
	if err != nil {
		var missing netlink.LinkNotFoundError
		if !errors.As(err, &missing) {
			return err
		}
		attrs := netlink.NewLinkAttrs()
		attrs.Name = config.Device()
		attrs.MTU = int(config.MTU)
		if err := netlink.LinkAdd(&netlink.Tuntap{LinkAttrs: attrs, Mode: netlink.TUNTAP_MODE_TUN, Flags: netlink.TUNTAP_DEFAULTS, Owner: m.uid, Group: m.gid}); err != nil {
			return err
		}
		link, err = netlink.LinkByName(config.Device())
		if err != nil {
			return err
		}
		if err := netlink.LinkSetAlias(link, m.plane.owner); err != nil {
			return err
		}
	}
	if err := netlink.LinkSetDown(link); err != nil {
		return err
	}
	if tun, ok := link.(*netlink.Tuntap); !ok || tun.Mode != netlink.TUNTAP_MODE_TUN || tun.Owner != m.uid || tun.Group != m.gid {
		return native.ErrOwnership
	}
	if err := m.plane.gate.Guard(ctx, []string{config.Device()}); err != nil {
		return err
	}
	if err := netlink.LinkSetMTU(link, int(config.MTU)); err != nil {
		return err
	}
	addresses, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if err := netlink.AddrDel(link, &address); err != nil {
			return err
		}
	}
	address := &netlink.Addr{IPNet: &net.IPNet{IP: net.IP(config.Address().AsSlice()), Mask: net.CIDRMask(32, 32)}}
	if err := netlink.AddrAdd(link, address); err != nil {
		return err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return err
	}
	routes, err := m.plane.gate.Routes(config.Device())
	if err != nil {
		return err
	}
	for _, route := range routes {
		if err := m.plane.gate.DeleteRoute(ctx, config.Device(), route); err != nil {
			return err
		}
	}
	for _, client := range config.Clients {
		for _, ip := range client.Addresses {
			if err := m.plane.gate.AddRoute(ctx, config.Device(), netip.PrefixFrom(ip, 32)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *openVPNManager) start(ctx context.Context, config openvpn.NodeConfiguration, grants []governor.Grant) (*openVPNProcess, error) {
	if err := m.ensureDir(m.dir); err != nil {
		return nil, err
	}
	dir := filepath.Join(m.dir, config.Device())
	if err := m.ensureDir(dir); err != nil {
		return nil, err
	}
	allow := filepath.Join(dir, "allow")
	if err := m.ensureDir(allow); err != nil {
		return nil, err
	}
	for _, client := range config.Clients {
		var addresses strings.Builder
		for _, ip := range client.Addresses {
			fmt.Fprintln(&addresses, ip)
		}
		if err := m.writeFile(filepath.Join(allow, client.Username), []byte(addresses.String())); err != nil {
			return nil, err
		}
	}
	socket := filepath.Join(dir, "management.sock")
	text, err := config.Render(dir, m.executable, socket)
	if err != nil {
		return nil, err
	}
	for name, data := range map[string]string{"server.conf": text, "server.crt": config.ServerCert, "server.key": config.ServerKey, "tc2.key": config.TLSCryptV2ServerKey} {
		if err := m.writeFile(filepath.Join(dir, name), []byte(data)); err != nil {
			return nil, err
		}
	}
	if err := m.tunnel(ctx, config); err != nil {
		return nil, err
	}
	listener, err := localrpc.Listen(ctx, socket, m.uid)
	if err != nil {
		return nil, err
	}
	if err := os.Chown(socket, 0, int(m.gid)); err != nil {
		_ = listener.Close()
		return nil, err
	}
	process := &openVPNProcess{listener: listener, auth: openvpn.NewNodeAuthenticator(config), done: make(chan struct{}), log: new(boundedVPNLog)}
	if m.binding != nil {
		if err := process.auth.BindAdmission(m.binding, config.InboundTag); err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	process.auth.SetClock(m.clock)
	process.auth.Grants(grants)
	process.command = exec.CommandContext(m.ctx, "/usr/sbin/openvpn", "--config", filepath.Join(dir, "server.conf"))
	process.command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	process.command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: m.uid, Gid: m.gid, Groups: []uint32{}}, Pdeathsig: syscall.SIGKILL}
	process.command.Stdout, process.command.Stderr = process.log, process.log
	if err := process.command.Start(); err != nil {
		_ = listener.Close()
		return nil, err
	}
	go func() { process.err = process.command.Wait(); close(process.done) }()
	accepted := make(chan net.Conn)
	stopAccept := make(chan struct{})
	defer close(stopAccept)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			select {
			case accepted <- connection:
			case <-stopAccept:
				_ = connection.Close()
			}
		}
	}()
	ready := make(chan struct{})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case process.conn = <-accepted:
		_ = listener.Close()
		go func() {
			_ = process.auth.Run(m.ctx, process.conn, ready)
			_ = process.command.Process.Signal(syscall.SIGTERM)
		}()
	case <-process.done:
		_ = process.stop()
		return nil, fmt.Errorf("OpenVPN startup: %w: %s", process.err, process.log.String())
	case <-ctx.Done():
		_ = process.stop()
		return nil, ctx.Err()
	}
	select {
	case <-ready:
		return process, nil
	case <-process.done:
		_ = process.stop()
		return nil, fmt.Errorf("OpenVPN startup: %w: %s", process.err, process.log.String())
	case <-ctx.Done():
		_ = process.stop()
		return nil, fmt.Errorf("OpenVPN startup: %w: %s", ctx.Err(), process.log.String())
	}
}

func (p *openVPNProcess) stop() error {
	_ = p.listener.Close()
	if p.conn != nil {
		_ = p.conn.Close()
	}
	_ = p.command.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
		return nil
	case <-time.After(5 * time.Second):
		_ = p.command.Process.Kill()
		<-p.done
		return nil
	}
}

func (m *openVPNManager) stop() error {
	for name, process := range m.processes {
		if err := process.stop(); err != nil {
			return err
		}
		delete(m.processes, name)
	}
	return nil
}

func (m *openVPNManager) reconcile(ctx context.Context, configs []openvpn.NodeConfiguration, grants []governor.Grant) error {
	wanted := make(map[string]bool, len(configs))
	for _, config := range configs {
		wanted[config.Device()] = true
	}
	links, err := m.plane.Links(ctx)
	if err != nil {
		return err
	}
	for _, name := range links {
		if !wanted[name] {
			link, err := m.plane.owned(name)
			if err != nil {
				return err
			}
			if err := netlink.LinkDel(link); err != nil {
				return err
			}
		}
	}
	if err := m.running(ctx, configs, grants); err != nil {
		return err
	}
	for _, process := range m.processes {
		process.auth.Grants(grants)
	}
	return nil
}

func (m *openVPNManager) running(ctx context.Context, configs []openvpn.NodeConfiguration, grants []governor.Grant) error {
	for _, config := range configs {
		if process := m.processes[config.Device()]; process != nil {
			select {
			case <-process.done:
				_ = process.stop()
				delete(m.processes, config.Device())
			default:
				continue
			}
		}
		process, err := m.start(ctx, config, grants)
		if err != nil {
			return err
		}
		m.processes[config.Device()] = process
	}
	return nil
}

func OpenVPNLearn(args []string) error { return openvpn.NodeLearn(args) }

var _ io.Writer = (*boundedVPNLog)(nil)
