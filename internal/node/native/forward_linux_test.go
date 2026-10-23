//go:build linux

package native

import (
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vishvananda/netlink"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestNativeForwardingServiceHelper(t *testing.T) {
	if os.Getenv("PENHOON_NATIVE_FORWARD_TEST") != "1" {
		t.Skip("native forwarding subprocess")
	}
	for _, kind := range []string{"net", "mnt"} {
		self, err := os.Readlink("/proc/self/ns/" + kind)
		initial, e := os.Readlink("/proc/1/ns/" + kind)
		if err != nil || e != nil || self == initial || os.Geteuid() != 0 {
			t.Fatal("refusing host namespace")
		}
	}
	plane, err := NewLinux(uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	configureTestNetwork(t, plane)
	if err := plane.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	link, err := netlink.LinkByName("phvtest")
	if err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetAlias(link, plane.owner); err != nil {
		t.Fatal(err)
	}
	if err := plane.Guard(t.Context(), []string{"phvtest"}); err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatal(err)
	}
	grant := testGrant(8 << 20)
	grant.Policy.Upload = limits.Rate{Mode: limits.Limited, BitsPerSecond: 100 << 20, BurstBytes: 65536}
	grant.Policy.Download = grant.Policy.Upload
	lease := Lease{ID: uuid.NewString(), Grant: grant, Class: 1, Bytes: 8 << 20, ExpiresUnixMilli: time.Now().Add(time.Minute).UnixMilli(), Selectors: []Selector{{Device: "phvtest", Address: netip.MustParseAddr("10.239.1.2")}}}
	if err := plane.Install(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	ctx, cancel := signal.NotifyContext(t.Context(), syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
	counts, err := plane.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	count := counts[sliceName(lease.ID)]
	if count.Uploaded == 0 || count.Downloaded == 0 {
		t.Fatalf("missing forwarded accounting: %+v", count)
	}
	fmt.Printf("NATIVE_FORWARD_USAGE up=%d down=%d\n", count.Uploaded, count.Downloaded)
}
