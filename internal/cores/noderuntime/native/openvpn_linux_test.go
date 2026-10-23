//go:build linux

package nativeruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	openvpn "github.com/Arman2122/p-ui/internal/cores/internal/openvpn"
	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "ocserv-hook" {
		if err := OcservHook(os.Args[2:]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "openvpn-learn" {
		if OpenVPNLearn(os.Args[2:]) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestOpenVPNTrafficHelper(t *testing.T) {
	target := os.Getenv("PENHOON_OVPN_TARGET")
	if target == "" {
		t.Skip("OpenVPN client subprocess")
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	if source := os.Getenv("PENHOON_OVPN_SOURCE"); source != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(source)}
	}
	conn, err := dialer.DialContext(t.Context(), "tcp", target)
	deny := os.Getenv("PENHOON_OVPN_DENY") == "1"
	if err != nil {
		if deny {
			return
		}
		t.Fatal(err)
	}
	defer conn.Close()
	if deny {
		t.Fatal("unauthorized OpenVPN source connected")
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	size, _ := strconv.Atoi(os.Getenv("PENHOON_OVPN_BYTES"))
	if size == 0 {
		size = 128 << 10
	}
	payload := bytes.Repeat([]byte{0xa4}, size)
	done := make(chan error, 1)
	go func() { _, err := conn.Write(payload); done <- err }()
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, received); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, received) {
		t.Fatal("OpenVPN payload changed")
	}
}

func TestNativeOpenVPNIsolated(t *testing.T) {
	if os.Getenv("PENHOON_NATIVE_OPENVPN_TEST") != "1" {
		t.Skip("requires private network/mount namespaces and OpenVPN 2.6")
	}
	for _, kind := range []string{"net", "mnt"} {
		self, err := os.Readlink("/proc/self/ns/" + kind)
		initial, e := os.Readlink("/proc/1/ns/" + kind)
		if err != nil || e != nil || self == initial || os.Geteuid() != 0 {
			t.Fatal("refusing host namespace")
		}
	}
	systemCommand(t, "mount", "--make-rprivate", "/")
	systemCommand(t, "mount", "-t", "tmpfs", "-o", "mode=0755", "openvpn-test", "/run")
	if err := os.Mkdir("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	systemCommand(t, "ip", "link", "set", "lo", "up")
	systemCommand(t, "sysctl", "-w", "net.ipv4.conf.all.rp_filter=0", "net.ipv4.conf.default.rp_filter=0")
	dir, err := os.MkdirTemp("/run", "openvpn-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	fixture := filepath.Join(dir, "fixture.bin")
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUI_NATIVE_OPENVPN_UID", "65534")
	t.Setenv("PUI_NATIVE_OPENVPN_GID", "65534")
	t.Setenv("PUI_NATIVE_OPENVPN_DIR", filepath.Join(dir, "daemon"))
	adapter := openvpn.New(openvpn.Deps{Binary: "/usr/sbin/openvpn", StateDir: filepath.Join(dir, "mint")})
	settingsRaw, err := adapter.MintInstance(openvpn.Kind, `{"proto":"udp","subnet":"10.238.0.0/24"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	var settings openvpn.Settings
	if err := json.Unmarshal([]byte(settingsRaw), &settings); err != nil {
		t.Fatal(err)
	}
	clientID := uuid.NewString()
	config := openvpn.NodeConfiguration{
		ID: 1, Port: 31194, Listen: netip.MustParseAddr("0.0.0.0"), Proto: "udp", Subnet: netip.MustParsePrefix("10.238.0.0/24"), MTU: 1400, ServerCert: settings.ServerCert, ServerKey: settings.ServerKey, TLSCryptV2ServerKey: settings.TLSCryptV2ServerKey,
		Clients: []openvpn.NodeClient{{ClientID: clientID, Username: "alice", Password: "test-long-password", Addresses: []netip.Addr{netip.MustParseAddr("10.238.0.2"), netip.MustParseAddr("10.238.0.3")}}, {ClientID: uuid.NewString(), Username: "bob", Password: "test-other-password", Addresses: []netip.Addr{netip.MustParseAddr("10.238.0.4")}}},
	}
	clientProto := "udp4"
	if os.Getenv("PENHOON_OPENVPN_TCP") == "1" {
		config.Proto = "tcp"
		clientProto = "tcp4-client"
	}
	grant := governor.Grant{ID: uuid.NewString(), ClientID: clientID, Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 64 << 20, Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 4 << 20, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 4 << 20, BurstBytes: 65536}}}
	other := grant
	other.ID = uuid.NewString()
	other.ClientID = config.Clients[1].ClientID
	identity := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	store, err := state.Open(filepath.Join(dir, "private", "journal.db"), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	trustNativeFixture(t, store)
	public := credentials.Config{ControlAddress: "10.237.0.5:7443", EnrollmentURL: "https://10.237.0.5:7444/enroll"}
	backend, journal, err := Open(t.Context(), filepath.Join(dir, "private"), "native", store, public)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	runtime := backend.(*Runtime)
	runtime.openvpn.executable = fixture
	defer func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	deployment := func() *Configuration { return &Configuration{OpenVPN: []openvpn.NodeConfiguration{config}} }
	wanted := asRuntime(t, *deployment(), grant, other)
	if err := runtime.Validate(wanted); err != nil {
		t.Fatal(err)
	}
	applyErr := func() error {
		mask := syscall.Umask(0o077)
		defer syscall.Umask(mask)
		return runtime.Apply(t.Context(), wanted)
	}()
	if err := applyErr; err != nil {
		t.Fatal(err)
	}
	process := runtime.openvpn.processes[config.Device()]
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", process.command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(status), "Uid:\t65534\t65534\t65534\t65534") || !strings.Contains(string(status), "CapEff:\t0000000000000000") {
		t.Fatalf("OpenVPN privileges not dropped: %s", status)
	}
	for _, test := range []struct {
		address string
		allowed bool
	}{{"10.238.0.2", true}, {"10.238.0.4", false}, {"10.238.0.99", false}} {
		cmd := exec.CommandContext(t.Context(), fixture, "openvpn-learn", filepath.Join(runtime.openvpn.dir, config.Device(), "allow"), "add", test.address, "alice")
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{}}}
		if err := cmd.Run(); (err == nil) != test.allowed {
			t.Fatalf("learn-address ownership %s = %v", test.address, err)
		}
	}
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	target := net.JoinHostPort(config.Address().String(), strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	var clients []*exec.Cmd
	for i := 1; i <= 3; i++ {
		ns := fmt.Sprintf("ovpn%d", i)
		outer, peer := fmt.Sprintf("outer%d", i), fmt.Sprintf("peer%d", i)
		network := fmt.Sprintf("172.30.%d.", i)
		systemCommand(t, "ip", "netns", "add", ns)
		systemCommand(t, "ip", "link", "add", outer, "type", "veth", "peer", "name", peer)
		systemCommand(t, "ip", "link", "set", peer, "netns", ns)
		systemCommand(t, "ip", "address", "add", network+"1/30", "dev", outer)
		systemCommand(t, "ip", "link", "set", outer, "up")
		systemCommand(t, "ip", "-n", ns, "address", "add", network+"2/30", "dev", peer)
		systemCommand(t, "ip", "-n", ns, "link", "set", peer, "up")
		systemCommand(t, "ip", "-n", ns, "link", "set", "lo", "up")
		user := config.Clients[0]
		if i == 3 {
			user = config.Clients[1]
		}
		keys, err := adapter.MintClientCredentials(openvpn.Kind, settingsRaw, map[string]string{core.CredUsername: user.Username, core.CredPassword: user.Password})
		if err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(dir, fmt.Sprintf("client%d.auth", i))
		if err := os.WriteFile(authFile, []byte(user.Username+"\n"+user.Password+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cryptFile := filepath.Join(dir, fmt.Sprintf("client%d.key", i))
		if err := os.WriteFile(cryptFile, []byte(keys[core.CredTLSCryptV2]), 0o600); err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode([]byte(config.ServerCert))
		fingerprint := sha256.Sum256(block.Bytes)
		var fingerprintParts []string
		for _, value := range fingerprint {
			fingerprintParts = append(fingerprintParts, hex.EncodeToString([]byte{value}))
		}
		clientConfig := fmt.Sprintf("client\ndev vpnclient\ndev-type tun\nproto %s\nremote %s1 %d\nnobind\nauth-user-pass %s\nauth-nocache\npeer-fingerprint %s\nremote-cert-tls server\ntls-crypt-v2 %s\ndisable-dco\nverb 3\n", clientProto, network, config.Port, authFile, strings.Join(fingerprintParts, ":"), cryptFile)
		path := filepath.Join(dir, fmt.Sprintf("client%d.conf", i))
		if err := os.WriteFile(path, []byte(clientConfig), 0o600); err != nil {
			t.Fatal(err)
		}
		log := new(boundedVPNLog)
		cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", ns, "/usr/sbin/openvpn", "--config", path)
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		clients = append(clients, cmd)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
		deadline := time.Now().Add(15 * time.Second)
		for !strings.Contains(log.String(), "Initialization Sequence Completed") {
			if time.Now().After(deadline) {
				t.Fatalf("client failed: %s; server: %s", log.String(), process.log.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	traffic := func(i, amount int, deny bool, source string) error {
		cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", fmt.Sprintf("ovpn%d", i), fixture, "-test.run=^TestOpenVPNTrafficHelper$")
		cmd.Env = append(os.Environ(), "PENHOON_OVPN_TARGET="+target, "PENHOON_OVPN_BYTES="+strconv.Itoa(amount), "PENHOON_OVPN_SOURCE="+source, "GORACE=atexit_sleep_ms=0")
		if deny {
			cmd.Env = append(cmd.Env, "PENHOON_OVPN_DENY=1")
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("traffic: %w: %s", err, output)
		}
		return nil
	}
	start := time.Now()
	results := make(chan error, 2)
	for i := 1; i <= 2; i++ {
		go func() { results <- traffic(i, 512<<10, false, "") }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 1400*time.Millisecond {
		t.Fatalf("shared OpenVPN rate exceeded: %v", elapsed)
	}
	systemCommand(t, "ip", "-n", "ovpn1", "address", "add", "10.238.0.4/32", "dev", "vpnclient")
	assertNativeSpoofBlocked(t, "ovpn1", config.Address().String(), "10.238.0.4")
	if err := traffic(1, 0, true, "10.238.0.4"); err != nil {
		t.Fatal(err)
	}
	if err := traffic(3, 128<<10, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	grant.Policy.Enabled = false
	grant.Policy.Revision++
	if err := runtime.Apply(t.Context(), asRuntime(t, *deployment(), grant, other)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if err := traffic(i, 0, true, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := traffic(3, 128<<10, false, ""); err != nil {
		t.Fatal(err)
	}
	grant.Policy.Enabled = true
	grant.Policy.Revision++
	if err := runtime.Apply(t.Context(), asRuntime(t, *deployment(), grant, other)); err != nil {
		t.Fatal(err)
	}
	if err := traffic(1, 128<<10, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := process.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-process.done
	if err := runtime.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if err := client.Process.Signal(syscall.SIGUSR1); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if err := traffic(1, 32<<10, false, ""); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("OpenVPN reconnect failed: %s", runtime.openvpn.processes[config.Device()].log.String())
		}
	}
	if err := traffic(2, 32<<10, false, ""); err != nil {
		t.Fatal(err)
	}
	grant.ID, grant.Policy.Term = uuid.NewString(), uuid.NewString()
	grant.Policy.Revision = 1
	grant.Policy.CreditCeiling = 64 << 10
	if err := runtime.Apply(t.Context(), asRuntime(t, *deployment(), grant, other)); err != nil {
		t.Fatal(err)
	}
	if err := traffic(1, 128<<10, false, ""); err == nil {
		t.Fatal("OpenVPN exceeded its shared credit")
	}
	if err := traffic(2, 0, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	batches, err := store.PendingUsage(64)
	if err != nil {
		t.Fatal(err)
	}
	var measured uint64
	for _, batch := range batches {
		for _, checkpoint := range batch.Checkpoints {
			if checkpoint.GrantId == grant.ID {
				measured = max(measured, checkpoint.Uploaded+checkpoint.Downloaded)
			}
		}
	}
	if measured == 0 || measured > grant.Policy.CreditCeiling {
		t.Fatalf("OpenVPN durable measured quota = %d", measured)
	}
}
