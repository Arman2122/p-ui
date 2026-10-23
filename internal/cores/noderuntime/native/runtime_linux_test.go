//go:build linux

package nativeruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"google.golang.org/protobuf/proto"

	"github.com/Arman2122/p-ui/internal/awg"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/command"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/localrpc"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/trustedtime"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

type observedBackend struct{ worker.Backend }

func trustNativeFixture(t *testing.T, store *state.Store) {
	t.Helper()
	source, err := trustedtime.SystemSource()
	if err != nil {
		t.Fatal(err)
	}
	clock, err := trustedtime.New(store, source)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := clock.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := clock.Accept(nonce, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func (b observedBackend) AuthorityJournalID() string {
	return b.Backend.(authority.Source).AuthorityJournalID()
}

func (b observedBackend) Authority(ctx context.Context, after string, limit int) (authority.Page, error) {
	return b.Backend.(authority.Source).Authority(ctx, after, limit)
}

func (b observedBackend) SetClock(clock limits.Clock) {
	b.Backend.(interface{ SetClock(limits.Clock) }).SetClock(clock)
}

func (b observedBackend) Validate(runtime *configuration.Runtime) error {
	err := b.Backend.Validate(runtime)
	if err != nil {
		fmt.Println("native validation error:", err)
	}
	return err
}

func (b observedBackend) Apply(ctx context.Context, runtime *configuration.Runtime) error {
	err := b.Backend.Apply(ctx, runtime)
	if err != nil {
		fmt.Println("native apply error:", err)
	}
	return err
}

func TestNativeServiceHelper(t *testing.T) {
	dir := os.Getenv("PENHOON_NATIVE_SERVICE")
	if dir == "" {
		t.Skip("native service subprocess")
	}
	ctx, cancel := signal.NotifyContext(t.Context(), syscall.SIGTERM)
	defer cancel()
	err := command.ManagedWorker(ctx, []string{"--state-dir", filepath.Join(dir, "state"), "--config", filepath.Join(dir, "node.json"), "--socket", filepath.Join(dir, "rpc", "native.sock"), "--agent-uid", "65534"}, io.Discard, "native", 250*time.Millisecond, func(ctx context.Context, dir, id string, store *state.Store, config credentials.Config) (worker.Backend, io.Closer, error) {
		backend, closer, err := Open(ctx, dir, id, store, config)
		return observedBackend{backend}, closer, err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeAPIHelper(t *testing.T) {
	dir := os.Getenv("PENHOON_NATIVE_API")
	if dir == "" {
		t.Skip("native API subprocess")
	}
	client, err := localrpc.Client(filepath.Join(dir, "rpc", "native.sock"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	client.Timeout = 20 * time.Second
	action := os.Getenv("PENHOON_NATIVE_ACTION")
	var data []byte
	method := http.MethodGet
	if action == "time" {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://runtime/v1/time", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
		_ = response.Body.Close()
		challenge := new(nodev1.TimeRequest)
		if err != nil || response.StatusCode != http.StatusOK || proto.Unmarshal(body, challenge) != nil {
			t.Fatalf("time challenge: %d %v", response.StatusCode, err)
		}
		data, err = proto.Marshal(&nodev1.TimeResponse{NodeId: challenge.NodeId, InstallationId: challenge.InstallationId, Nonce: challenge.Nonce, ServerUnixMilli: time.Now().UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		method = http.MethodPost
	} else if action != "usage" {
		method = http.MethodPost
		data, err = os.ReadFile(filepath.Join(dir, "request.pb"))
		if err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), method, "http://runtime/v1/"+action, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if action == "validate" || action == "apply" {
		probe, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://runtime/v1/journals", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(probe)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		journal := new(nodev1.RuntimeJournalIdentity)
		if err != nil || response.StatusCode != http.StatusOK || proto.Unmarshal(body, journal) != nil {
			t.Fatalf("journal identity: %d %v", response.StatusCode, err)
		}
		req.Header.Set("X-Pui-State-Journal", journal.StateJournalId)
		req.Header.Set("X-Pui-Credit-Journal", journal.CreditJournalId)
		req.Header.Set("X-Pui-Agent-Journal", "325a74b7-8dca-4d9c-8757-a37f983fa1f0")
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	fmt.Printf("STATUS=%d\n", response.StatusCode)
	data, err = io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	if action == "usage" && response.StatusCode == http.StatusOK {
		batch := new(nodev1.UsageBatch)
		if err := proto.Unmarshal(data, batch); err != nil {
			t.Fatal(err)
		}
		value, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("BATCH=%s\n", value)
	}
}

func TestNativeClientHelper(t *testing.T) {
	address := os.Getenv("PENHOON_NATIVE_TARGET")
	if address == "" {
		t.Skip("native traffic subprocess")
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	if os.Getenv("PENHOON_NATIVE_DENY") == "1" {
		dialer.Timeout = 3 * time.Second
	}
	if source := os.Getenv("PENHOON_NATIVE_SOURCE"); source != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(source)}
	}
	conn, err := dialer.DialContext(t.Context(), "tcp", address)
	if err != nil {
		if os.Getenv("PENHOON_NATIVE_DENY") == "1" {
			fmt.Println("DENIED")
			return
		}
		t.Fatal(err)
	}
	defer conn.Close()
	if os.Getenv("PENHOON_NATIVE_DENY") == "1" {
		t.Fatal("unauthorized client connected")
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte{0x4b}, 128<<10)
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
		t.Fatal("native payload changed")
	}
}

func systemCommand(t *testing.T, name string, args ...string) {
	t.Helper()
	if output, err := exec.CommandContext(t.Context(), name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, output)
	}
}

func apiCall(t *testing.T, dir, action string, message proto.Message, want int) *nodev1.UsageBatch {
	t.Helper()
	if message != nil {
		data, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "request.pb"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), filepath.Join(dir, "fixture.bin"), "-test.run=^TestNativeAPIHelper$")
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	cmd.Env = append(os.Environ(), "PENHOON_NATIVE_API="+dir, "PENHOON_NATIVE_ACTION="+action)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native API: %v: %s", err, output)
	}
	status := 0
	var batch *nodev1.UsageBatch
	for line := range strings.SplitSeq(string(output), "\n") {
		if strings.HasPrefix(line, "STATUS=") {
			status, _ = strconv.Atoi(strings.TrimPrefix(line, "STATUS="))
		}
		if strings.HasPrefix(line, "BATCH=") {
			batch = new(nodev1.UsageBatch)
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "BATCH=")), batch); err != nil {
				t.Fatal(err)
			}
		}
	}
	if status != want {
		log, _ := os.ReadFile(filepath.Join(dir, "service.log"))
		t.Logf("native service: %s", log)
		t.Fatalf("native %s status %d, want %d: %s", action, status, want, output)
	}
	return batch
}

func TestNativeRuntimeWireGuardIsolated(t *testing.T) {
	if os.Getenv("PENHOON_NATIVE_RUNTIME_TEST") != "1" {
		t.Skip("requires private Linux network and mount namespaces")
	}
	for _, kind := range []string{"net", "mnt"} {
		self, err := os.Readlink("/proc/self/ns/" + kind)
		if err != nil {
			t.Fatal(err)
		}
		initial, err := os.Readlink("/proc/1/ns/" + kind)
		if err != nil || self == initial || os.Geteuid() != 0 {
			t.Fatal("refusing host namespace")
		}
	}
	systemCommand(t, "mount", "--make-rprivate", "/")
	systemCommand(t, "mount", "-t", "tmpfs", "-o", "mode=0755", "native-runtime-test", "/run")
	if err := os.Mkdir("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	systemCommand(t, "ip", "link", "set", "lo", "up")
	ipv6 := os.Getenv("PENHOON_NATIVE_RUNTIME_IPV6") == "1"
	if ipv6 {
		systemCommand(t, "sysctl", "-qw", "net.ipv6.conf.all.forwarding=0")
	}
	dir, err := os.MkdirTemp("/run", "native-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(dir, 0, 65534); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.OpenFile(filepath.Join(dir, "fixture.bin"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(fixture, source)
	_ = source.Close()
	closeErr := fixture.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	identity := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	public := credentials.Config{ControlAddress: "10.237.0.5:7443", EnrollmentURL: "https://10.237.0.5:7444/enroll"}
	public.Identity.NodeID = identity.NodeID
	public.Identity.InstallationID = identity.InstallationID
	public.Identity.ControlPlaneID = identity.ControlPlaneID
	data, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	config, grant, clientKey := testConfig(t)
	networkOverride := os.Getenv("PENHOON_NATIVE_BINDING_TEST") == "1"
	if networkOverride {
		config.Network = &NetworkBinding{PeerIPv4: netip.MustParseAddr("172.31.240.2"), GatewayIPv4: netip.MustParseAddr("172.31.240.3")}
		systemCommand(t, "ip", "route", "add", "blackhole", "169.254.0.0/16")
	}
	for i := 1; i <= 2; i++ {
		if i == 2 {
			server, err := wgtypes.GeneratePrivateKey()
			if err != nil {
				t.Fatal(err)
			}
			copy := config.WireGuard[0]
			copy.ID = 2
			copy.Port = 31822
			copy.PrivateKey = server.String()
			copy.Addresses = []netip.Prefix{netip.MustParsePrefix("10.239.2.1/32")}
			copy.Clients = []Peer{{ClientID: grant.ClientID, PublicKey: clientKey.PublicKey().String(), Addresses: []netip.Addr{netip.MustParseAddr("10.239.2.2")}}}
			config.WireGuard = append(config.WireGuard, copy)
		}
		if ipv6 {
			config.WireGuard[i-1].Addresses = append(config.WireGuard[i-1].Addresses, netip.MustParsePrefix(fmt.Sprintf("fd99:239:%d::1/128", i)))
			config.WireGuard[i-1].Clients[0].Addresses = append(config.WireGuard[i-1].Clients[0].Addresses, netip.MustParseAddr(fmt.Sprintf("fd99:239:%d::2", i)))
		}
		inbound := config.WireGuard[i-1]
		kind, tool := "wireguard", "wg"
		if i == 2 && os.Getenv("PENHOON_NATIVE_AWG_TEST") == "1" {
			kind, tool = "amneziawg", "awg"
			config.AmneziaWG = []AmneziaWG{{WireGuard: inbound, Parameters: awg.Params{Jc: 3, Jmin: 32, Jmax: 64, S1: 17, S2: 23, S3: 7, S4: 11, H1: 101<<32 | 101, H2: 102<<32 | 102, H3: 103<<32 | 103, H4: 104<<32 | 104}}}
			config.WireGuard = config.WireGuard[:1]
		}
		ns, outer, peer := fmt.Sprintf("client%d", i), fmt.Sprintf("outer%d", i), fmt.Sprintf("peer%d", i)
		prefix := fmt.Sprintf("172.29.%d.", i)
		systemCommand(t, "ip", "netns", "add", ns)
		systemCommand(t, "ip", "link", "add", outer, "type", "veth", "peer", "name", peer)
		systemCommand(t, "ip", "link", "set", peer, "netns", ns)
		systemCommand(t, "ip", "address", "add", prefix+"1/30", "dev", outer)
		systemCommand(t, "ip", "link", "set", outer, "up")
		systemCommand(t, "ip", "-n", ns, "address", "add", prefix+"2/30", "dev", peer)
		systemCommand(t, "ip", "-n", ns, "link", "set", peer, "up")
		systemCommand(t, "ip", "-n", ns, "link", "set", "lo", "up")
		systemCommand(t, "ip", "-n", ns, "link", "add", "wgclient", "type", kind)
		keyPath := filepath.Join(dir, fmt.Sprintf("client%d.key", i))
		if err := os.WriteFile(keyPath, []byte(clientKey.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		serverKey, _ := wgtypes.ParseKey(inbound.PrivateKey)
		allowed := fmt.Sprintf("10.239.%d.1/32", i)
		if ipv6 {
			allowed += fmt.Sprintf(",fd99:239:%d::1/128", i)
		}
		systemCommand(t, "ip", "netns", "exec", ns, tool, "set", "wgclient", "private-key", keyPath, "peer", serverKey.PublicKey().String(), "allowed-ips", allowed, "endpoint", prefix+"1:"+strconv.Itoa(31820+i))
		if kind == "amneziawg" {
			systemCommand(t, "ip", "netns", "exec", ns, "awg", "set", "wgclient", "jc", "3", "jmin", "32", "jmax", "64", "s1", "17", "s2", "23", "s3", "7", "s4", "11", "h1", "101", "h2", "102", "h3", "103", "h4", "104")
			systemCommand(t, "ip", "-n", ns, "link", "set", "wgclient", "mtu", "1409")
		}
		systemCommand(t, "ip", "-n", ns, "address", "add", fmt.Sprintf("10.239.%d.2/32", i), "dev", "wgclient")
		systemCommand(t, "ip", "-n", ns, "link", "set", "wgclient", "up")
		systemCommand(t, "ip", "-n", ns, "route", "add", fmt.Sprintf("10.239.%d.1/32", i), "dev", "wgclient")
		if ipv6 {
			systemCommand(t, "ip", "-n", ns, "-6", "address", "add", fmt.Sprintf("fd99:239:%d::2/128", i), "dev", "wgclient", "nodad")
			systemCommand(t, "ip", "-n", ns, "-6", "route", "add", fmt.Sprintf("fd99:239:%d::1/128", i), "dev", "wgclient")
		}
	}
	network, listen := "tcp4", "0.0.0.0:0"
	if ipv6 {
		network, listen = "tcp6", "[::]:0"
	}
	listener, err := net.Listen(network, listen)
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
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	traffic := func(i int, deny bool) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", fmt.Sprintf("client%d", i), os.Args[0], "-test.run=^TestNativeClientHelper$")
		target := fmt.Sprintf("10.239.%d.1:%d", i, port)
		if ipv6 {
			target = fmt.Sprintf("[fd99:239:%d::1]:%d", i, port)
		}
		cmd.Env = append(os.Environ(), "PENHOON_NATIVE_TARGET="+target)
		if deny {
			cmd.Env = append(cmd.Env, "PENHOON_NATIVE_DENY=1")
		}
		if output, err := cmd.CombinedOutput(); err != nil {
			for _, args := range [][]string{{"ip", "-6", "route", "show", "table", "all"}, {"ip", "-d", "link", "show", "phva2"}, {"awg", "show", "phva2", "latest-handshakes"}, {"awg", "show", "phva2", "transfer"}, {"ip", "netns", "exec", "client2", "awg", "show", "wgclient", "latest-handshakes"}, {"nft", "list", "table", "inet", "penhoon_node_v2"}} {
				diagnostic, _ := exec.CommandContext(t.Context(), args[0], args[1:]...).CombinedOutput()
				t.Logf("native traffic diagnostic %v: %s", args, diagnostic)
			}
			log, _ := os.ReadFile(filepath.Join(dir, "service.log"))
			t.Logf("native service: %s", log)
			t.Fatalf("client %d: %v: %s", i, err, output)
		}
	}
	var child *exec.Cmd
	var done chan error
	var log *os.File
	start := func() {
		t.Helper()
		log, err = os.OpenFile(filepath.Join(dir, "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		child = exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestNativeServiceHelper$")
		child.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 0, Gid: 65534}}
		child.Env = append(os.Environ(), "PENHOON_NATIVE_SERVICE="+dir)
		child.Stdout, child.Stderr = log, log
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		done = make(chan error, 1)
		go func() { done <- child.Wait() }()
		deadline := time.Now().Add(10 * time.Second)
		for {
			probe, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(t.Context(), "unix", filepath.Join(dir, "rpc", "native.sock"))
			if err == nil {
				_ = probe.Close()
				break
			}
			select {
			case err := <-done:
				child = nil
				_ = log.Close()
				body, _ := os.ReadFile(filepath.Join(dir, "service.log"))
				t.Fatalf("native service exited: %v: %s", err, body)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("native service did not listen")
			}
			time.Sleep(20 * time.Millisecond)
		}
		apiCall(t, dir, "time", nil, 204)
	}
	stop := func(kill bool) {
		t.Helper()
		if child == nil {
			return
		}
		signal := os.Signal(syscall.SIGTERM)
		if kill {
			signal = syscall.SIGKILL
		}
		_ = child.Process.Signal(signal)
		err := <-done
		_ = log.Close()
		child = nil
		if err != nil && !kill {
			body, _ := os.ReadFile(filepath.Join(dir, "service.log"))
			t.Fatalf("native shutdown: %v: %s", err, body)
		}
	}
	t.Cleanup(func() { stop(false) })
	start()
	deployment := func(revision uint64) *nodev1.Deployment {
		runtime := asRuntime(t, config, grant)
		body, err := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{*runtime}})
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(body)
		return &nodev1.Deployment{NodeId: identity.NodeID, ControlPlaneId: identity.ControlPlaneID, AuthorityGeneration: 1, Revision: revision, Payload: body, PayloadSha256: hash[:]}
	}
	if ipv6 {
		apiCall(t, dir, "validate", deployment(1), 409)
		apiCall(t, dir, "apply", deployment(1), 409)
		if _, err := net.InterfaceByName("phvw1"); err == nil {
			t.Fatal("IPv6 prerequisite failure changed native tunnels")
		}
		systemCommand(t, "sysctl", "-qw", "net.ipv6.conf.all.forwarding=1")
	}
	apiCall(t, dir, "validate", deployment(1), 204)
	apiCall(t, dir, "apply", deployment(1), 204)
	traffic(1, false)
	traffic(2, false)
	revision := uint64(1)
	if networkOverride {
		original := config.Network
		for _, binding := range []NetworkBinding{
			{PeerIPv4: netip.MustParseAddr("10.237.0.5"), GatewayIPv4: original.GatewayIPv4},
			{PeerIPv4: original.PeerIPv4, GatewayIPv4: netip.MustParseAddr("10.237.0.5")},
			{PeerIPv4: netip.MustParseAddr("10.239.1.2"), GatewayIPv4: original.GatewayIPv4},
		} {
			config.Network = &binding
			conflicting := deployment(revision + 1)
			config.Network = original
			apiCall(t, dir, "validate", conflicting, 409)
			apiCall(t, dir, "apply", conflicting, 409)
		}
		traffic(1, false)
		config.Network = &NetworkBinding{PeerIPv4: netip.MustParseAddr("172.31.241.2"), GatewayIPv4: netip.MustParseAddr("172.31.241.3")}
		grant.Policy.Revision++
		revision++
		apiCall(t, dir, "validate", deployment(revision), 204)
		apiCall(t, dir, "apply", deployment(revision), 204)
		traffic(1, false)
		traffic(2, false)
		peer, err := net.InterfaceByName("phvdownin")
		if err != nil {
			t.Fatal(err)
		}
		addresses, err := peer.Addrs()
		if err != nil || len(addresses) != 1 || addresses[0].String() != "172.31.241.2/32" {
			t.Fatalf("network rebind retained old/unexpected addresses: %v %v", addresses, err)
		}
	}
	invalid := proto.Clone(deployment(revision + 1)).(*nodev1.Deployment)
	var invalidConfig Configuration
	encoded, _ := json.Marshal(config)
	if err := json.Unmarshal(encoded, &invalidConfig); err != nil {
		t.Fatal(err)
	}
	invalidConfig.WireGuard[0].Clients[0].Addresses[0] = netip.MustParseAddr("172.29.1.1")
	invalidRuntime := asRuntime(t, invalidConfig, grant)
	invalid.Payload, err = json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{*invalidRuntime}})
	if err != nil {
		t.Fatal(err)
	}
	invalidHash := sha256.Sum256(invalid.Payload)
	invalid.PayloadSha256 = invalidHash[:]
	apiCall(t, dir, "validate", invalid, 409)
	apiCall(t, dir, "apply", invalid, 409)
	invalidConfig.WireGuard[0].Clients[0].Addresses[0] = netip.MustParseAddr("10.237.0.5")
	invalidRuntime = asRuntime(t, invalidConfig, grant)
	invalid.Payload, err = json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{*invalidRuntime}})
	if err != nil {
		t.Fatal(err)
	}
	invalidHash = sha256.Sum256(invalid.Payload)
	invalid.PayloadSha256 = invalidHash[:]
	apiCall(t, dir, "apply", invalid, 409)
	traffic(1, false)
	spoofSource, spoofTarget := "10.239.2.2", fmt.Sprintf("10.239.1.1:%d", port)
	if ipv6 {
		spoofSource, spoofTarget = "fd99:239:2::2", fmt.Sprintf("[fd99:239:1::1]:%d", port)
		systemCommand(t, "ip", "-n", "client1", "-6", "address", "add", spoofSource+"/128", "dev", "wgclient", "nodad")
	} else {
		systemCommand(t, "ip", "-n", "client1", "address", "add", spoofSource+"/32", "dev", "wgclient")
	}
	spoof := exec.CommandContext(t.Context(), "ip", "netns", "exec", "client1", os.Args[0], "-test.run=^TestNativeClientHelper$")
	spoof.Env = append(os.Environ(), "PENHOON_NATIVE_TARGET="+spoofTarget, "PENHOON_NATIVE_DENY=1", "PENHOON_NATIVE_SOURCE="+spoofSource)
	if output, err := spoof.CombinedOutput(); err != nil {
		t.Fatalf("spoofed native address: %v: %s", err, output)
	}
	first := apiCall(t, dir, "usage", nil, 200)
	if len(first.Checkpoints) == 0 {
		t.Fatal("native usage missing")
	}
	var measured uint64
	for _, c := range first.Checkpoints {
		if c.ClientId != grant.ClientID || c.GrantId != grant.ID || c.MeterId != "native" {
			t.Fatal("wrong native accounting identity")
		}
		measured += c.Uploaded + c.Downloaded
	}
	if measured == 0 {
		t.Fatal("empty native traffic")
	}
	stop(true)
	traffic(1, false)
	start()
	apiCall(t, dir, "apply", deployment(revision), 204)
	traffic(2, false)
	grant.Policy.Enabled = false
	grant.Policy.Revision++
	revision++
	apiCall(t, dir, "apply", deployment(revision), 204)
	traffic(1, true)
	traffic(2, true)
	grant.Policy.Enabled = true
	grant.Policy.Revision++
	revision++
	apiCall(t, dir, "apply", deployment(revision), 204)
	traffic(1, false)
	stop(false)
	store, err := state.Open(filepath.Join(dir, "state", "journal.db"), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cursors, err := store.Cursors()
	if err != nil || cursors.Applied != revision {
		t.Fatalf("applied revision = %+v %v", cursors, err)
	}
	batches, err := store.PendingUsage(64)
	if err != nil {
		t.Fatal(err)
	}
	var latest uint64
	for _, b := range batches {
		for _, c := range b.Checkpoints {
			latest = max(latest, c.Uploaded+c.Downloaded)
		}
	}
	if latest < 6*(256<<10) || latest > grant.Policy.CreditCeiling {
		t.Fatalf("native handover lost or invented usage: %d", latest)
	}
}
