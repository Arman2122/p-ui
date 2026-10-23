//go:build linux

package nativeruntime

import (
	"context"
	"encoding/json"
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
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	ocserv "github.com/Arman2122/p-ui/internal/cores/internal/ocserv"
	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/native"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func TestNativeOcservIsolated(t *testing.T) {
	if os.Getenv("PENHOON_NATIVE_OCSERV_TEST") != "1" {
		t.Skip("requires isolated namespaces and ocserv1.2.4/OpenConnect")
	}
	for _, kind := range []string{"net", "mnt"} {
		self, err := os.Readlink("/proc/self/ns/" + kind)
		initial, e := os.Readlink("/proc/1/ns/" + kind)
		if err != nil || e != nil || self == initial || os.Geteuid() != 0 {
			t.Fatal("refusing host namespace")
		}
	}
	systemCommand(t, "mount", "--make-rprivate", "/")
	systemCommand(t, "mount", "-t", "tmpfs", "-o", "mode=0755", "ocserv-test", "/run")
	if err := os.Mkdir("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	systemCommand(t, "ip", "link", "set", "lo", "up")
	systemCommand(t, "sysctl", "-w", "net.ipv4.conf.all.rp_filter=0", "net.ipv4.conf.default.rp_filter=0")
	dir, err := os.MkdirTemp("/run", "ocserv-")
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
	t.Setenv("PUI_NATIVE_OCSERV_UID", "65534")
	t.Setenv("PUI_NATIVE_OCSERV_GID", "65534")
	t.Setenv("PUI_NATIVE_OCSERV_DIR", filepath.Join(dir, "daemon"))
	adapter := ocserv.New(ocserv.Deps{Binary: "/usr/sbin/ocserv", Occtl: "/usr/bin/occtl", StateDir: filepath.Join(dir, "mint")})
	settingsRaw, err := adapter.MintInstance(ocserv.Kind, `{"subnet":"10.236.0.0/29"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	var settings ocserv.Settings
	if err := json.Unmarshal([]byte(settingsRaw), &settings); err != nil {
		t.Fatal(err)
	}
	config := ocserv.NodeConfiguration{
		ID: 1, Port: 31443, Listen: netip.MustParseAddr("0.0.0.0"), MTU: 1400, ServerCert: settings.ServerCert, ServerKey: settings.ServerKey,
		Clients: []ocserv.NodeClient{{ClientID: uuid.NewString(), Username: "alice", Password: "ocserv-test-password", Subnet: netip.MustParsePrefix("10.236.0.0/29"), MaxSessions: 3}, {ClientID: uuid.NewString(), Username: "bob", Password: "ocserv-other-password", Subnet: netip.MustParsePrefix("10.236.0.8/29"), MaxSessions: 3}},
	}
	grant := governor.Grant{ID: uuid.NewString(), ClientID: config.Clients[0].ClientID, Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 64 << 20, Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 4 << 20, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 4 << 20, BurstBytes: 65536}}}
	other := grant
	other.ID = uuid.NewString()
	other.ClientID = config.Clients[1].ClientID
	store, err := state.Open(filepath.Join(dir, "private", "journal.db"), state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	trustNativeFixture(t, store)
	backend, journal, err := Open(t.Context(), filepath.Join(dir, "private"), "native", store, credentials.Config{ControlAddress: "10.237.0.5:7443", EnrollmentURL: "https://10.237.0.5:7444/enroll"})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	runtime := backend.(*Runtime)
	runtime.ocserv.executable = fixture
	defer func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	wanted := Configuration{OpenConnect: []ocserv.NodeConfiguration{config}}
	if err := runtime.Validate(asRuntime(t, wanted, grant, other)); err != nil {
		t.Fatal(err)
	}
	applyErr := func() error {
		mask := syscall.Umask(0o077)
		defer syscall.Umask(mask)
		return runtime.Apply(t.Context(), asRuntime(t, wanted, grant, other))
	}()
	if err := applyErr; err != nil {
		t.Fatal(err)
	}
	records, err := journal.(*native.Journal).Records()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Reserved != 0 {
			t.Fatal("ocserv reserved credit before a session existed")
		}
	}
	process := runtime.ocserv.processes[config.ID]
	systemCommand(t, "ip", "netns", "add", "unknown")
	systemCommand(t, "ip", "link", "add", "phvc1x9999", "type", "veth", "peer", "name", "pending0")
	systemCommand(t, "ip", "link", "set", "pending0", "netns", "unknown")
	systemCommand(t, "ip", "address", "add", "10.233.1.1/30", "dev", "phvc1x9999")
	systemCommand(t, "ip", "link", "set", "phvc1x9999", "up")
	systemCommand(t, "ip", "-n", "unknown", "address", "add", "10.233.1.2/30", "dev", "pending0")
	systemCommand(t, "ip", "-n", "unknown", "link", "set", "pending0", "up")
	unguarded, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "ip", "netns", "exec", "unknown", fixture, "-test.run=^TestNativeSpoofPacketHelper$")
	command.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "PENHOON_NATIVE_SPOOF_TARGET="+net.JoinHostPort("10.233.1.1", strconv.Itoa(unguarded.LocalAddr().(*net.UDPAddr).Port)))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unregistered interface probe: %v %s", err, output)
	}
	_ = unguarded.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buffer := make([]byte, 64)
	_, _, err = unguarded.ReadFrom(buffer)
	_ = unguarded.Close()
	if err == nil {
		t.Fatal("unguarded dynamic interface forwarded its first packet")
	}
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("dynamic guard probe did not time out: %v", err)
	}
	systemCommand(t, "ip", "link", "delete", "phvc1x9999")
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
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	type clientProcess struct {
		command *exec.Cmd
		done    chan error
		log     *boundedVPNLog
		target  string
		stopped bool
		address netip.Addr
	}
	clients := map[int]*clientProcess{}
	startClient := func(i int, user ocserv.NodeClient) *clientProcess {
		t.Helper()
		ns := fmt.Sprintf("ocs%d", i)
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
		profile, err := adapter.RenderClient(core.Instance{ID: 1, Port: int(config.Port), Settings: settingsRaw}, core.User{Credentials: map[string]any{core.CredUsername: user.Username, core.CredPassword: user.Password}}, network+"1")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, fmt.Sprintf("client%d.conf", i))
		if err := os.WriteFile(path, []byte(profile.Body), 0o600); err != nil {
			t.Fatal(err)
		}
		script := filepath.Join(dir, fmt.Sprintf("client%d.sh", i))
		body := fmt.Sprintf("#!/bin/sh\nset -eu\nif [ \"$reason\" = connect ]; then\nip link set \"$TUNDEV\" mtu \"$INTERNAL_IP4_MTU\" up\nip address replace \"$INTERNAL_IP4_ADDRESS/32\" dev \"$TUNDEV\"\nip route replace %s/32 dev \"$TUNDEV\"\nfi\n", user.Subnet.Addr().Next())
		if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		args := []string{"netns", "exec", ns, "/usr/sbin/openconnect", "--config", path, "--passwd-on-stdin", "--interface", "ocsclient", "--script", script, "--non-inter", "--disable-ipv6", "-v"}
		if os.Getenv("PENHOON_OCSERV_TCP") == "1" {
			args = append(args, "--no-dtls")
		}
		args = append(args, fmt.Sprintf("https://%s1:%d", network, config.Port))
		client := &clientProcess{command: exec.CommandContext(t.Context(), "ip", args...), done: make(chan error, 1), log: new(boundedVPNLog), target: net.JoinHostPort(user.Subnet.Addr().Next().String(), port)}
		client.command.Stdin = strings.NewReader(user.Password + "\n")
		client.command.Stdout, client.command.Stderr = client.log, client.log
		if err := client.command.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { client.done <- client.command.Wait() }()
		t.Cleanup(func() {
			if !client.stopped {
				_ = client.command.Process.Kill()
				<-client.done
				client.stopped = true
			}
		})
		clients[i] = client
		return client
	}
	for i := 1; i <= 3; i++ {
		user := config.Clients[0]
		if i == 3 {
			user = config.Clients[1]
		}
		startClient(i, user)
	}
	waitClient := func(client *clientProcess) {
		t.Helper()
		deadline := time.Now().Add(25 * time.Second)
		for !strings.Contains(client.log.String(), "Configured as") || os.Getenv("PENHOON_OCSERV_TCP") != "1" && !strings.Contains(client.log.String(), "Established DTLS") {
			if time.Now().After(deadline) {
				t.Fatalf("ocserv client not ready: %s; daemon: %s", client.log.String(), process.log.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
		_, tail, _ := strings.Cut(client.log.String(), "Configured as ")
		address, _, _ := strings.Cut(tail, ",")
		client.address, err = netip.ParseAddr(address)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, client := range clients {
		waitClient(client)
	}
	stopClient := func(client *clientProcess) {
		t.Helper()
		if client.stopped {
			return
		}
		_ = client.command.Process.Signal(syscall.SIGINT)
		select {
		case <-client.done:
		case <-time.After(5 * time.Second):
			_ = client.command.Process.Kill()
			<-client.done
		}
		client.stopped = true
	}
	records, err = journal.(*native.Journal).Records()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Reserved != native.MaxSliceBytes || record.Lease == nil {
			t.Fatalf("session addition discarded quota: %+v", record)
		}
	}
	traffic := func(i, size int, deny bool, source string) error {
		cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", fmt.Sprintf("ocs%d", i), fixture, "-test.run=^TestOpenVPNTrafficHelper$")
		cmd.Env = append(os.Environ(), "PENHOON_OVPN_TARGET="+clients[i].target, "PENHOON_OVPN_BYTES="+strconv.Itoa(size), "PENHOON_OVPN_SOURCE="+source, "GORACE=atexit_sleep_ms=0")
		if deny {
			cmd.Env = append(cmd.Env, "PENHOON_OVPN_DENY=1")
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("ocserv traffic: %w: %s", err, output)
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
		t.Fatalf("ocserv shared rate exceeded: %v", elapsed)
	}
	runtime.mu.Lock()
	var bobAddress netip.Addr
	for _, binding := range runtime.ocserv.bindings {
		if binding.Username == "bob" {
			bobAddress = binding.Address
		}
	}
	runtime.mu.Unlock()
	systemCommand(t, "ip", "-n", "ocs1", "address", "add", bobAddress.String()+"/32", "dev", "ocsclient")
	assertNativeSpoofBlocked(t, "ocs1", config.Clients[0].Subnet.Addr().Next().String(), bobAddress.String())
	if err := traffic(1, 0, true, bobAddress.String()); err != nil {
		t.Fatal(err)
	}
	if err := traffic(3, 128<<10, false, ""); err != nil {
		t.Fatal(err)
	}
	grant.Policy.Enabled = false
	grant.Policy.Revision++
	if err := runtime.Apply(t.Context(), asRuntime(t, wanted, grant, other)); err != nil {
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
	if err := runtime.Apply(t.Context(), asRuntime(t, wanted, grant, other)); err != nil {
		t.Fatal(err)
	}
	if err := traffic(1, 128<<10, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	reserved := func(id string) uint64 {
		t.Helper()
		rows, err := journal.(*native.Journal).Records()
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Grant.ID == id {
				return row.Reserved
			}
		}
		t.Fatal("grant missing")
		return 0
	}
	aliceReserved, bobReserved := reserved(grant.ID), reserved(other.ID)
	runtime.mu.Lock()
	var old ocservBinding
	for _, binding := range runtime.ocserv.bindings {
		if binding.Address == clients[1].address {
			old = binding
		}
	}
	runtime.mu.Unlock()
	if old.Device == "" {
		t.Fatal("Alice session binding missing")
	}
	hook := filepath.Join(runtime.ocserv.dir, config.Prefix(), "hook-"+process.generation)
	hookData, err := os.ReadFile(hook)
	if err != nil {
		t.Fatal(err)
	}
	delayed := strings.Replace(string(hookData), "#!/bin/sh\n", "#!/bin/sh\nif [ \"$REASON\" = disconnect ]; then sleep 6; fi\n", 1)
	if err := os.WriteFile(hook, []byte(delayed), 0o750); err != nil {
		t.Fatal(err)
	}
	stopClient(clients[1])
	waitClient(startClient(4, config.Clients[1]))
	runtime.mu.Lock()
	replacement, exists := runtime.ocserv.bindings[old.Device]
	runtime.mu.Unlock()
	if !exists || replacement.Username != "bob" || replacement.Index == old.Index {
		t.Fatalf("did not exercise cross-client interface-name reuse: old=%+v new=%+v", old, replacement)
	}
	time.Sleep(7 * time.Second)
	runtime.mu.Lock()
	current := runtime.ocserv.bindings[old.Device]
	runtime.mu.Unlock()
	if current != replacement {
		t.Fatalf("late disconnect removed replacement ownership: %+v", current)
	}
	if err := os.WriteFile(hook, hookData, 0o750); err != nil {
		t.Fatal(err)
	}
	systemCommand(t, "ip", "-n", "ocs4", "address", "add", old.Address.String()+"/32", "dev", "ocsclient")
	assertNativeSpoofBlocked(t, "ocs4", config.Clients[1].Subnet.Addr().Next().String(), old.Address.String())
	if err := traffic(4, 128<<10, false, ""); err != nil {
		t.Fatal(err)
	}
	if reserved(grant.ID) != aliceReserved || reserved(other.ID) != bobReserved {
		t.Fatal("dynamic interface reuse replayed or discarded credit")
	}
	waitClient(startClient(5, config.Clients[0]))
	grant.ID, grant.Policy.Term = uuid.NewString(), uuid.NewString()
	grant.Policy.Revision = 1
	grant.Policy.CreditCeiling = 64 << 10
	if err := runtime.Apply(t.Context(), asRuntime(t, wanted, grant, other)); err != nil {
		t.Fatal(err)
	}
	if err := traffic(2, 128<<10, false, ""); err == nil {
		t.Fatal("ocserv exceeded shared quota")
	}
	if err := traffic(5, 0, true, ""); err != nil {
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
		t.Fatalf("ocserv quota accounting = %d", measured)
	}
	for _, client := range clients {
		stopClient(client)
	}
	if err := process.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-process.done
	if err := runtime.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reserved(other.ID) != bobReserved {
		t.Fatal("ocserv daemon restart discarded kernel credit")
	}
	waitClient(startClient(6, config.Clients[1]))
	if err := traffic(6, 128<<10, false, ""); err != nil {
		t.Fatal(err)
	}
}
