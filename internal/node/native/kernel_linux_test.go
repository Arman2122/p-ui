//go:build linux

package native

import (
	"bytes"
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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func kernelCommand(t *testing.T, name string, args ...string) {
	t.Helper()
	if output, err := exec.CommandContext(t.Context(), name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, output)
	}
}

type peerResult struct {
	Bytes   int64
	Elapsed float64
}

func TestNativePacketPeer(t *testing.T) {
	target := os.Getenv("PENHOON_NATIVE_PEER")
	if target == "" {
		t.Skip("native peer subprocess")
	}
	conn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	fmt.Println("NATIVE_CONNECTED")
	if os.Getenv("PENHOON_NATIVE_CONNECT_ONLY") == "1" {
		return
	}
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready [1]byte
	if _, err := io.ReadFull(conn, ready[:]); err != nil || ready[0] != 'g' {
		t.Fatalf("peer barrier: %v", err)
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.CopyN(conn, endlessBytes{}, 1<<20)
		_ = conn.(*net.TCPConn).CloseWrite()
	}()
	n, _ := io.Copy(io.Discard, conn)
	<-done
	data, _ := json.Marshal(peerResult{Bytes: n, Elapsed: time.Since(start).Seconds()})
	fmt.Printf("NATIVE_RESULT=%s\n", data)
}

type endlessBytes struct{}

func (endlessBytes) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0x65
	}
	return len(p), nil
}

func runNativePeers(t *testing.T, port int) []peerResult {
	t.Helper()
	var results [2]peerResult
	var failures [2]error
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", fmt.Sprintf("native%d", i+1), os.Args[0], "-test.run=^TestNativePacketPeer$")
			cmd.Env = append(os.Environ(), fmt.Sprintf("PENHOON_NATIVE_PEER=10.239.%d.1:%d", i+1, port))
			output, err := cmd.CombinedOutput()
			if err != nil {
				failures[i] = fmt.Errorf("peer: %w: %s", err, output)
				return
			}
			for line := range strings.SplitSeq(string(output), "\n") {
				if strings.HasPrefix(line, "NATIVE_RESULT=") {
					failures[i] = json.Unmarshal([]byte(strings.TrimPrefix(line, "NATIVE_RESULT=")), &results[i])
					return
				}
			}
			failures[i] = fmt.Errorf("peer returned no result: %s", output)
		})
	}
	wg.Wait()
	for _, err := range failures {
		if err != nil {
			for _, args := range [][]string{{"nft", "list", "table", "inet", table}, {"tc", "-s", "class", "show", "dev", "phvup"}, {"tc", "-s", "class", "show", "dev", downOut}, {"tc", "-s", "filter", "show", "dev", downOut, "parent", "1:"}, {"ip", "route", "show", "table", "all"}, {"ip", "rule", "show"}, {"ip", "-s", "link", "show", downOut}, {"ip", "-s", "link", "show", downIn}, {"sysctl", "net.ipv4.conf.phvdownin.forwarding", "net.ipv4.conf.phvdownin.accept_local", "net.ipv4.conf.phvdownin.rp_filter"}, {"wg", "show", "all", "latest-handshakes"}, {"wg", "show", "all", "transfer"}} {
				output, failure := exec.CommandContext(t.Context(), args[0], args[1:]...).CombinedOutput()
				t.Logf("native diagnostic %v: %v %s", args, failure, output)
			}
			t.Fatal(err)
		}
	}
	return results[:]
}

func TestNativeKernelIsolated(t *testing.T) {
	if os.Getenv("PENHOON_NATIVE_KERNEL_TEST") != "1" {
		t.Skip("requires explicitly isolated Linux network and mount namespaces")
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
	kernelCommand(t, "mount", "--make-rprivate", "/")
	kernelCommand(t, "mount", "-t", "tmpfs", "-o", "mode=0755", "penhoon-native-test", "/run")
	if err := os.Mkdir("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	kernelCommand(t, "ip", "link", "set", "lo", "up")
	installation := uuid.NewString()
	p, err := NewLinux(installation, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.Initialize(t.Context()); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	foreign, _ := NewLinux(uuid.NewString(), uuid.NewString())
	if err := foreign.Initialize(t.Context()); !errors.Is(err, ErrOwnership) {
		t.Fatalf("foreign ownership: %v", err)
	}
	duplicate, _ := NewLinux(installation, uuid.NewString())
	if err := duplicate.Initialize(t.Context()); !errors.Is(err, ErrOwnership) {
		t.Fatalf("second credit journal adopted kernel authority: %v", err)
	}
	var selectors []Selector
	for i := 1; i <= 2; i++ {
		device, peer, namespace := fmt.Sprintf("phvtest%d", i), fmt.Sprintf("peer%d", i), fmt.Sprintf("native%d", i)
		prefix := fmt.Sprintf("10.239.%d.", i)
		kernelCommand(t, "ip", "netns", "add", namespace)
		if os.Getenv("PENHOON_NATIVE_WIREGUARD_TEST") == "1" {
			wireguardPair(t, i, namespace, device, peer, prefix)
		} else {
			kernelCommand(t, "ip", "link", "add", device, "type", "veth", "peer", "name", peer)
			kernelCommand(t, "ip", "link", "set", peer, "netns", namespace)
		}
		kernelCommand(t, "ip", "link", "set", device, "alias", p.owner)
		if err := p.Guard(t.Context(), []string{device}); err != nil {
			t.Fatal(err)
		}
		kernelCommand(t, "ip", "address", "add", prefix+"1/30", "dev", device)
		kernelCommand(t, "ip", "link", "set", device, "up")
		kernelCommand(t, "ip", "-n", namespace, "address", "add", prefix+"2/30", "dev", peer)
		kernelCommand(t, "ip", "-n", namespace, "link", "set", peer, "up")
		kernelCommand(t, "ip", "-n", namespace, "link", "set", "lo", "up")
		selectors = append(selectors, Selector{Device: device, Address: netip.MustParseAddr(prefix + "2")})
	}
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			var peers []net.Conn
			for range 2 {
				conn, err := listener.Accept()
				if err != nil {
					for _, peer := range peers {
						_ = peer.Close()
					}
					return
				}
				peers = append(peers, conn)
			}
			for _, conn := range peers {
				go func() {
					defer func() { _ = conn.Close() }()
					_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
					if _, err := conn.Write([]byte{'g'}); err != nil {
						return
					}
					_, _ = io.Copy(conn, conn)
				}()
			}
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	j, err := Open(filepath.Join(t.TempDir(), "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	g := testGrant(32 << 20)
	g.Policy.Upload = limits.Rate{Mode: limits.Limited, BitsPerSecond: 8_000_000, BurstBytes: 65536}
	g.Policy.Download = g.Policy.Upload
	if err := j.Apply(g); err != nil {
		t.Fatal(err)
	}
	lease, err := j.Reserve(g.ID, selectors, MaxSliceBytes, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Install(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	checkKernelDeadline(t, p, lease)
	if err := p.Install(t.Context(), lease); !errors.Is(err, ErrBusy) {
		t.Fatalf("replayed kernel slice: %v", err)
	}
	conflict := lease
	conflict.ID = uuid.NewString()
	conflict.Grant.Policy.Upload.BitsPerSecond *= 2
	conflict.Grant.Policy.Download.BitsPerSecond *= 2
	if err := p.Install(t.Context(), conflict); !errors.Is(err, ErrBusy) {
		t.Fatalf("rebound selector changed active rates: %v", err)
	}
	results := runNativePeers(t, port)
	var elapsed float64
	var count int64
	for _, result := range results {
		if result.Bytes != 1<<20 {
			t.Fatalf("shared bidirectional rate dropped stream: %+v", results)
		}
		count += result.Bytes
		elapsed = max(elapsed, result.Elapsed)
	}
	if float64(count) > 1_000_000*elapsed+2*65536 {
		for _, device := range []string{"phvup", "phvdown"} {
			output, err := exec.CommandContext(t.Context(), "tc", "-s", "class", "show", "dev", device).CombinedOutput()
			t.Logf("%s rate state: %v %s", device, err, output)
		}
		t.Fatalf("multiplied rate: %+v", results)
	}
	t.Logf("shared upload/download: %d payload bytes each in %.3fs", count, elapsed)
	counters, err := p.Block(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if counters.Uploaded < uint64(count) || counters.Downloaded < uint64(count) || counters.Uploaded+counters.Downloaded > lease.Bytes {
		t.Fatalf("counters: %+v", counters)
	}
	if err := p.Remove(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if err := j.Finalize(g.ID, lease.ID); err != nil {
		t.Fatal(err)
	}
	if err := j.Forget(g.ID, lease.ID); err != nil {
		t.Fatal(err)
	}
	g.Policy.Revision++
	g.Policy.Upload, g.Policy.Download = limits.Rate{Mode: limits.Unlimited}, limits.Rate{Mode: limits.Unlimited}
	if err := j.Apply(g); err != nil {
		t.Fatal(err)
	}
	lease, err = j.Reserve(g.ID, selectors, 1<<20, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Install(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	results = runNativePeers(t, port)
	counters, err = p.Block(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if counters.Uploaded+counters.Downloaded > 1<<20 || counters.Uploaded+counters.Downloaded < 65536 {
		t.Fatalf("combined quota: %+v", counters)
	}
	t.Logf("shared bidirectional quota: %+v, payload: %+v", counters, results)
	if err := p.Remove(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if err := j.Finalize(g.ID, lease.ID); err != nil {
		t.Fatal(err)
	}
	if err := j.Forget(g.ID, lease.ID); err != nil {
		t.Fatal(err)
	}
	lease, err = j.Reserve(g.ID, selectors, MaxSliceBytes, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lease.ExpiresUnixMilli = time.Now().Add(300 * time.Millisecond).UnixMilli()
	if err := p.Install(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	select {
	case <-time.After(500 * time.Millisecond):
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ip", "netns", "exec", "native1", os.Args[0], "-test.run=^TestNativePacketPeer$")
	cmd.Env = append(os.Environ(), "PENHOON_NATIVE_PEER=10.239.1.1:"+strconv.Itoa(port), "PENHOON_NATIVE_CONNECT_ONLY=1")
	if output, err := cmd.CombinedOutput(); err == nil || bytes.Contains(output, []byte("NATIVE_CONNECTED")) {
		t.Fatalf("expired kernel authorization accepted traffic: %v %s", err, output)
	}
	if _, err := p.Block(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if err := p.Remove(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
}

func checkKernelDeadline(t *testing.T, p *Linux, lease Lease) {
	t.Helper()
	data, err := p.run(t.Context(), "list chain inet "+table+" "+sliceName(lease.ID)+"_up\n")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Nftables []struct {
			Rule *struct {
				Expr []struct{ Match *struct{ Right string } }
			}
		}
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	for _, item := range response.Nftables {
		if item.Rule == nil {
			continue
		}
		for _, expr := range item.Rule.Expr {
			if expr.Match == nil {
				continue
			}
			want := time.UnixMilli(lease.ExpiresUnixMilli).UTC().Format("2006-01-02 15:04:05")
			if expr.Match.Right != want {
				t.Fatalf("kernel expiry encoded in wrong units: got %q, want %q", expr.Match.Right, want)
			}
			return
		}
	}
	t.Fatal("kernel deadline missing")
}

func wireguardPair(t *testing.T, i int, namespace, device, peer, prefix string) {
	t.Helper()
	underlay := fmt.Sprintf("phu%d", i)
	outerPeer := fmt.Sprintf("outer%d", i)
	outerPrefix := fmt.Sprintf("172.29.%d.", i)
	kernelCommand(t, "ip", "link", "add", underlay, "type", "veth", "peer", "name", outerPeer)
	kernelCommand(t, "ip", "link", "set", outerPeer, "netns", namespace)
	kernelCommand(t, "ip", "address", "add", outerPrefix+"1/30", "dev", underlay)
	kernelCommand(t, "ip", "link", "set", underlay, "up")
	kernelCommand(t, "ip", "-n", namespace, "address", "add", outerPrefix+"2/30", "dev", outerPeer)
	kernelCommand(t, "ip", "-n", namespace, "link", "set", outerPeer, "up")
	kernelCommand(t, "ip", "link", "add", device, "type", "wireguard")
	kernelCommand(t, "ip", "-n", namespace, "link", "add", peer, "type", "wireguard")
	serverKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	serverPath, clientPath := filepath.Join(dir, "server.key"), filepath.Join(dir, "client.key")
	if err := os.WriteFile(serverPath, []byte(serverKey.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clientPath, []byte(clientKey.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(31820 + i)
	kernelCommand(t, "wg", "set", device, "private-key", serverPath, "listen-port", port, "peer", clientKey.PublicKey().String(), "allowed-ips", prefix+"2/32")
	kernelCommand(t, "ip", "netns", "exec", namespace, "wg", "set", peer, "private-key", clientPath, "peer", serverKey.PublicKey().String(), "allowed-ips", prefix+"1/32", "endpoint", outerPrefix+"1:"+port)
}
