package coretun

import (
	"errors"
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
)

// The plane must never route a family the device may not hold, and must name
// exactly the device the core answers with, or the route lands on nothing.
func TestFillRoutesOnlyIntoTheNamedDeviceOverIPv4(t *testing.T) {
	d := New("openvpn", func(id int) (string, error) { return "pvx" + itoa(id), nil })
	fill, err := d.Fill(egress.Egress{ID: 7, Type: "openvpn", Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	if fill.Device != "pvx7" || !fill.Marked || fill.Addr.IsValid() {
		t.Fatalf("fill = %+v; want the core's device, marked, with no address to insist on", fill)
	}
	if !slices.Equal(fill.Families, []egress.Family{egress.FamilyV4}) {
		t.Fatalf("families = %v; want IPv4 alone until the daemon's address is known", fill.Families)
	}
	if fill.Sysctls["net.ipv4.conf.pvx7.rp_filter"] != "2" {
		t.Fatalf("sysctls = %v; want loose reverse-path filtering on the device", fill.Sysctls)
	}
	failing := New("ocserv", func(int) (string, error) { return "", errors.New("no such exit") })
	if _, err := failing.Fill(egress.Egress{ID: 3}); err == nil {
		t.Fatal("a device the core cannot name was routed into anyway")
	}
}

func itoa(i int) string { return string(rune('0' + i)) }
