//go:build linux

package native

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vishvananda/netlink"

	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func TestNativeQueueReceiver(t *testing.T) {
	path := os.Getenv("PENHOON_NATIVE_QUEUE_CAPTURE")
	if path == "" {
		t.Skip("native queue receiver subprocess")
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	address := "10.239.1.2:31411"
	network := "udp4"
	if os.Getenv("PENHOON_NATIVE_QUEUE_IPV6") == "1" {
		address = "[fd99:239:1::2]:31411"
		network = "udp6"
	}
	conn, err := net.ListenPacket(network, address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = os.Stdout.WriteString("READY\n")
	buffer := make([]byte, 65536)
	record := make([]byte, 12)
	for {
		n, _, err := conn.ReadFrom(buffer)
		if err != nil {
			return
		}
		binary.LittleEndian.PutUint64(record, uint64(time.Now().UnixNano()))
		binary.LittleEndian.PutUint32(record[8:], uint32(n))
		if _, err := file.Write(record); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeQueuedStopIsolated(t *testing.T) {
	if os.Getenv("PENHOON_NATIVE_QUEUE_TEST") != "1" {
		t.Skip("requires private network/mount namespaces")
	}
	for _, kind := range []string{"net", "mnt"} {
		self, err := os.Readlink("/proc/self/ns/" + kind)
		initial, e := os.Readlink("/proc/1/ns/" + kind)
		if err != nil || e != nil || self == initial || os.Geteuid() != 0 {
			t.Fatal("refusing host namespace")
		}
	}
	kernelCommand(t, "mount", "--make-rprivate", "/")
	kernelCommand(t, "mount", "-t", "tmpfs", "-o", "mode=0755", "native-queue-test", "/run")
	if err := os.Mkdir("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	kernelCommand(t, "ip", "link", "set", "lo", "up")
	ipv6 := os.Getenv("PENHOON_NATIVE_QUEUE_IPV6") == "1"
	if ipv6 {
		kernelCommand(t, "sysctl", "-w", "net.ipv6.conf.all.forwarding=1")
	}
	identity := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	dir := t.TempDir()
	journal, err := Open(filepath.Join(dir, "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store, err := state.Open(filepath.Join(dir, "usage.db"), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	plane, err := NewLinux(identity.InstallationID, journal.JournalID())
	if err != nil {
		t.Fatal(err)
	}
	configureTestNetwork(t, plane)
	if err := plane.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	controller, err := NewController(journal, store, "native", plane)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	clockShift := time.Duration(0)
	if os.Getenv("PENHOON_NATIVE_CLOCK_SHIFT") == "1" {
		clockShift = time.Hour
		controller.SetClock(shiftedNativeClock{offset: clockShift})
	}
	kernelCommand(t, "ip", "netns", "add", "queue-peer")
	kernelCommand(t, "ip", "link", "add", "phvqueue", "type", "veth", "peer", "name", "peer0")
	kernelCommand(t, "ip", "link", "set", "peer0", "netns", "queue-peer")
	kernelCommand(t, "ip", "link", "set", "phvqueue", "alias", plane.owner)
	if err := plane.Guard(t.Context(), []string{"phvqueue"}); err != nil {
		t.Fatal(err)
	}
	kernelCommand(t, "ip", "address", "add", "10.239.1.1/30", "dev", "phvqueue")
	kernelCommand(t, "ip", "link", "set", "phvqueue", "up")
	kernelCommand(t, "ip", "-n", "queue-peer", "address", "add", "10.239.1.2/30", "dev", "peer0")
	kernelCommand(t, "ip", "-n", "queue-peer", "link", "set", "peer0", "up")
	clientAddress := netip.MustParseAddr("10.239.1.2")
	target := "10.239.1.2:31411"
	network := "udp4"
	if ipv6 {
		kernelCommand(t, "ip", "-6", "address", "add", "fd99:239:1::1/126", "dev", "phvqueue", "nodad")
		kernelCommand(t, "ip", "-n", "queue-peer", "-6", "address", "add", "fd99:239:1::2/126", "dev", "peer0", "nodad")
		peerData, err := exec.CommandContext(t.Context(), "ip", "-n", "queue-peer", "-j", "link", "show", "peer0").Output()
		if err != nil {
			t.Fatal(err)
		}
		var peers []struct{ Address string }
		if err := json.Unmarshal(peerData, &peers); err != nil || len(peers) != 1 {
			t.Fatalf("peer MAC: %v", err)
		}
		host, err := netlink.LinkByName("phvqueue")
		if err != nil {
			t.Fatal(err)
		}
		kernelCommand(t, "ip", "-6", "neigh", "replace", "fd99:239:1::2", "lladdr", peers[0].Address, "nud", "permanent", "dev", "phvqueue")
		kernelCommand(t, "ip", "-n", "queue-peer", "-6", "neigh", "replace", "fd99:239:1::1", "lladdr", host.Attrs().HardwareAddr.String(), "nud", "permanent", "dev", "peer0")
		clientAddress = netip.MustParseAddr("fd99:239:1::2")
		target = "[fd99:239:1::2]:31411"
		network = "udp6"
	}
	grant := testGrant(4 * MaxSliceBytes)
	grant.Policy.Upload = limits.Rate{Mode: limits.Unlimited}
	grant.Policy.Download = limits.Rate{Mode: limits.Limited, BitsPerSecond: 65536, BurstBytes: 65536}
	autonomous := os.Getenv("PENHOON_NATIVE_QUEUE_EXPIRY") == "1"
	queueDevice := downOut
	if autonomous {
		grant.Policy.ExpiresUnixMilli = time.Now().Add(clockShift + 3*time.Second).UnixMilli()
	}
	assignment := Assignment{Grant: grant, Selectors: []Selector{{Device: "phvqueue", Address: clientAddress}}}
	if err := controller.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(dir, "packets.bin")
	command := exec.CommandContext(t.Context(), "ip", "netns", "exec", "queue-peer", os.Args[0], "-test.run=^TestNativeQueueReceiver$")
	command.Env = append(os.Environ(), "PENHOON_NATIVE_QUEUE_CAPTURE="+capture, "GORACE=atexit_sleep_ms=0")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "READY\n" {
			t.Fatalf("queue receiver: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queue receiver not ready")
	}
	conn, err := net.Dial(network, target)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if os.Getenv("PENHOON_NATIVE_ROUTE_DRIFT") == "1" {
		kernelCommand(t, "ip", "route", "replace", clientAddress.String()+"/32", "dev", "phvqueue", "metric", "32000")
		_, _ = conn.Write(make([]byte, 1000))
		time.Sleep(300 * time.Millisecond)
		data, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != 0 {
			t.Fatal("native route drift bypassed download gate")
		}
		if err := plane.AddRoute(t.Context(), "phvqueue", netip.PrefixFrom(clientAddress, clientAddress.BitLen())); err != nil {
			t.Fatal(err)
		}
	}
	for range 300 {
		if _, err := conn.Write(make([]byte, 1000)); err != nil {
			t.Fatal(err)
		}
	}
	link, err := netlink.LinkByName(queueDevice)
	if err != nil {
		t.Fatal(err)
	}
	classes, err := netlink.ClassList(link, netlink.HANDLE_NONE)
	if err != nil {
		t.Fatal(err)
	}
	var queued uint32
	for _, class := range classes {
		if stats := class.Attrs().Statistics; stats != nil && stats.Queue != nil {
			queued += stats.Queue.Backlog
		}
	}
	if queued < 16000 {
		t.Fatalf("did not establish retained downlink queue: %d", queued)
	}
	var stopped time.Time
	if os.Getenv("PENHOON_NATIVE_GUARD_LOSS") == "1" {
		before, err := journal.Records()
		if err != nil {
			t.Fatal(err)
		}
		kernelCommand(t, "nft", "delete", "table", "inet", table)
		if err := controller.Collect(t.Context()); err == nil {
			t.Fatal("missing guard did not fail collection")
		}
		if err := controller.Reconcile(t.Context(), []Assignment{assignment}); !errors.Is(err, ErrBusy) {
			t.Fatalf("quarantined controller reopened authority: %v", err)
		}
		after, err := journal.Records()
		if err != nil || len(before) != 1 || len(after) != 1 || before[0].Reserved != after[0].Reserved || after[0].Lease == nil || before[0].Lease.ID != after[0].Lease.ID {
			t.Fatalf("missing guard altered uncertain reservation: %v", err)
		}
		stopped = time.Now()
		time.Sleep(2 * time.Second)
	} else if autonomous {
		stopped = time.UnixMilli(grant.Policy.ExpiresUnixMilli).Add(-clockShift)
		time.Sleep(time.Until(stopped.Add(2 * time.Second)))
	} else {
		if os.Getenv("PENHOON_NATIVE_QUEUE_REDUCE") == "1" {
			assignment.Grant.Policy.Download.BitsPerSecond /= 2
		} else {
			assignment.Grant.Policy.Enabled = false
		}
		assignment.Grant.Policy.Revision++
		if err := controller.Reconcile(t.Context(), []Assignment{assignment}); err != nil {
			t.Fatal(err)
		}
		stopped = time.Now()
		time.Sleep(2 * time.Second)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var late uint64
	packets := len(data) / 12
	for len(data) >= 12 {
		stamp := int64(binary.LittleEndian.Uint64(data))
		size := binary.LittleEndian.Uint32(data[8:])
		if stamp > stopped.Add(100*time.Millisecond).UnixNano() {
			late += uint64(size)
		}
		data = data[12:]
	}
	if late != 0 {
		t.Fatalf("queued downlink survived disabled apply: %d bytes after stop grace", late)
	}
	if autonomous {
		counters, err := plane.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var downloaded uint64
		for _, count := range counters {
			downloaded += count.Downloaded
		}
		packetSize := uint64(1028)
		if ipv6 {
			packetSize = 1048
		}
		if packets == 0 || downloaded != uint64(packets)*packetSize {
			t.Fatalf("post-queue traffic lost or double-counted: packets=%d downloaded=%d", packets, downloaded)
		}
		t.Logf("post-queue expiry admitted %d packets and counted %d inner IP bytes exactly once", packets, downloaded)
	}
}
