//go:build linux

package shaping

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"testing"
)

// Temporary: converges the tree an accuracy run measures, then prints the readback.
func TestZZRate(t *testing.T) {
	if os.Getenv("PUI_SHAPING_RATE") != "1" {
		t.Skip("rate")
	}
	down, _ := strconv.ParseInt(os.Getenv("PUI_SHAPE_DOWN"), 10, 64)
	up, _ := strconv.ParseInt(os.Getenv("PUI_SHAPE_UP"), 10, 64)
	client := netip.MustParsePrefix(os.Getenv("PUI_SHAPE_CLIENT"))

	m := NewManager(hostPlane(), DefaultNamespaces())
	w := []DeviceWant{{Device: "pwg901", Subjects: []Subject{
		{ID: "alice", Keys: []Key{{Prefix: client}}, Limits: Limits{DownBps: down, UpBps: up}},
	}}}
	if err := m.Converge(context.Background(), w); err != nil {
		t.Fatalf("Converge: %v", err)
	}
	enforced, err := m.Enforced(context.Background(), w[0])
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	fmt.Printf("CONFIGURED down=%d up=%d ENFORCED down=%d up=%d\n",
		down, up, enforced["alice"].DownBps, enforced["alice"].UpBps)
}
