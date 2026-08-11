package wireguard

import (
	"context"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

// reconciled builds a rig whose device is up and serving n clients, which is the
// only state in which the kernel has an identity to report.
func reconciled(t *testing.T, users int) (*rig, *Core, core.Instance) {
	t.Helper()
	r := newRig()
	c := &Core{mgr: r.mgr}
	inst := r.instance(users)
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return r, c, inst
}

// TestShapingTargetsLeavesAWiderPrefixUnshaped pins the one identity rule the
// conformance rig cannot drive: every peer it builds already carries a /32.
func TestShapingTargetsLeavesAWiderPrefixUnshaped(t *testing.T) {
	r := newRig()
	c := &Core{mgr: r.mgr}
	inst := r.instance(2)
	// A site-to-site peer is a real configuration and not an identity: its prefix
	// answers for everyone inside it, so shaping on it shapes strangers.
	inst.Users[1].Credentials[core.CredAllowedIPs] = []any{"10.9.0.0/24"}
	if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	target, err := c.ShapingTargets(context.Background(), inst)
	if err != nil {
		t.Fatalf("ShapingTargets: %v", err)
	}
	if key, keyed := target.Keys[inst.Users[1].Email]; keyed {
		t.Errorf("%q is keyed by %v; a prefix wider than one address is not an identity and the client must be left unshaped", inst.Users[1].Email, key.Prefixes)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.11/32")}
	if got := target.Keys[inst.Users[0].Email].Prefixes; !slices.Equal(got, want) {
		t.Errorf("the other client is keyed by %v, want %v; one unusable peer must not cost its neighbours their limits", got, want)
	}
}

// TestShapingTargetsGoesQuietWithoutADevice holds the retryable half of the
// contract: no device is a state to wait out, not a failure to report.
func TestShapingTargetsGoesQuietWithoutADevice(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) (*Core, core.Instance)
	}{
		{
			name: "the inbound has never been reconciled",
			setup: func(t *testing.T) (*Core, core.Instance) {
				t.Helper()
				r := newRig()
				return &Core{mgr: r.mgr}, r.instance(1)
			},
		},
		{
			name: "the device went away underneath a live inbound",
			setup: func(t *testing.T) (*Core, core.Instance) {
				t.Helper()
				r, c, inst := reconciled(t, 1)
				if err := r.kernel.DeleteLink(context.Background(), iface); err != nil {
					t.Fatalf("delete link: %v", err)
				}
				return c, inst
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, inst := tc.setup(t)
			target, err := c.ShapingTargets(context.Background(), inst)
			if err != nil {
				t.Fatalf("an absent device is retryable, not an error: %v", err)
			}
			if target.Device != "" || len(target.Keys) != 0 {
				t.Errorf("got device %q with %d keys, want neither; a target built without a device attaches limits to something that is not there", target.Device, len(target.Keys))
			}
		})
	}
}

// TestSessionsLeavesOutATunnelThatWentQuiet keeps the report to live tunnels: an
// observed session is live by definition, so a dead one counts against a cap forever.
func TestSessionsLeavesOutATunnelThatWentQuiet(t *testing.T) {
	r, c, inst := reconciled(t, 2)
	r.kernel.FeedSession(iface, r.keys[clients[0]], netip.MustParseAddr("203.0.113.9"), time.Now().Add(-time.Minute))
	r.kernel.FeedSession(iface, r.keys[clients[1]], netip.MustParseAddr("203.0.113.10"), time.Now().Add(-time.Hour))

	got, err := c.Sessions(context.Background())
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %v, want only the tunnel that handshook a minute ago", got)
	}
	if got[0].Email != inst.Users[0].Email || got[0].Source.String() != "203.0.113.9" {
		t.Errorf("got %q from %s, want %q from 203.0.113.9", got[0].Email, got[0].Source, inst.Users[0].Email)
	}
}
