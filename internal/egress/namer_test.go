package egress_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/egtest"
)

// namedTun is the shape of a client daemon's exit: the plane only names the
// device, and somebody else creates it.
type namedTun struct{}

func (namedTun) Type() string { return "daemon-tun" }
func (namedTun) DeviceName(id int) (string, error) {
	return "pvx" + string(rune('0'+id)), nil
}

func (d namedTun) Fill(e egress.Egress) (egress.Fill, error) {
	device, _ := d.DeviceName(e.ID)
	return egress.Fill{Device: device, Marked: true, Families: []egress.Family{egress.FamilyV4}}, nil
}

/*
A row whose device is outside the plane's own namespaces must still be torn
down cleanly. With the row gone there is no type to fill from, so the plane
used to see its own front as a foreign route: left in place, and reported as
defeating containment on every pass until the daemon happened to die.
*/
func TestATornDownNamedFrontIsWithdrawn(t *testing.T) {
	k := egtest.New()
	k.AddLink("pvx8")
	registry := egress.NewRegistry()
	if err := registry.Register(namedTun{}); err != nil {
		t.Fatal(err)
	}
	m := egress.New(k, registry)
	ctx := context.Background()
	if err := m.Reconcile(ctx, []egress.Egress{{ID: 8, Type: "daemon-tun", Enable: true}}); err != nil {
		t.Fatalf("bring up: %v", err)
	}
	if routes := strings.Join(k.Routes(), "\n"); !strings.Contains(routes, "v4 table 30008 default dev pvx8 metric 100") {
		t.Fatalf("the front was never installed:\n%s", routes)
	}

	err := m.Reconcile(ctx, nil)
	if errors.Is(err, egress.ErrForeignResource) {
		t.Fatalf("the plane called its own front foreign on teardown: %v", err)
	}
	if err != nil {
		t.Fatalf("tear down: %v", err)
	}
	for _, route := range k.Routes() {
		if strings.Contains(route, "table 30008") {
			t.Fatalf("a route outlived its row: %s", route)
		}
	}
}
