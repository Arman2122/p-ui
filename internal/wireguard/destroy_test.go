package wireguard_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/wireguard"
	"github.com/Arman2122/p-ui/internal/wireguard/wgtest"
)

// primed runs the baseline scrape, so everything a later scrape reports is
// traffic this test moved rather than the counters it started from.
func primed(t *testing.T, m *wireguard.Manager) {
	t.Helper()
	if billed, _ := m.CollectTraffic(t.Context()); totalBytes(billed) != 0 {
		t.Fatalf("the priming scrape billed %d bytes, want 0", totalBytes(billed))
	}
}

func TestRemovingADeviceBanksItsLastInterval(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t, peer(t, 1, "a@example.com", "10.9.0.2/32"))
	if err := m.Ensure(t.Context(), inst); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	primed(t, m)
	k.FeedTraffic(iface, key(t, 1), 5000, 3000)

	if err := m.Remove(t.Context(), 7); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	billed, _ := m.CollectTraffic(t.Context())
	if got := totalBytes(billed); got != 8000 {
		t.Fatalf("the client moved 8000 bytes and Remove billed %d; deleting the link zeroes every counter, so the interval has to be banked first", got)
	}
}

func TestRemovingADeviceKeepsWhatAPeerRemovalBanked(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t, peer(t, 1, "a@example.com", "10.9.0.2/32"))
	if err := m.Ensure(t.Context(), inst); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	primed(t, m)
	k.FeedTraffic(iface, key(t, 1), 4000, 2000)
	if err := m.RemovePeer(t.Context(), inst, "a@example.com", ""); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}

	if err := m.Remove(t.Context(), 7); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	billed, _ := m.CollectTraffic(t.Context())
	if got := totalBytes(billed); got != 6000 {
		t.Fatalf("RemovePeer banked 6000 bytes and Remove billed %d; dropping the record throws the bank away with it", got)
	}
}

// A second teardown pass over the same id is the ordinary case: DropInstance
// removes the device and the next supervise tick walks the same id again.
func TestASecondRemovalDoesNotLoseTheBankedInterval(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t, peer(t, 1, "a@example.com", "10.9.0.2/32"))
	if err := m.Ensure(t.Context(), inst); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	primed(t, m)
	k.FeedTraffic(iface, key(t, 1), 5000, 3000)

	if err := m.Remove(t.Context(), 7); err != nil {
		t.Fatalf("first Remove: %v", err)
	}
	if err := m.Remove(t.Context(), 7); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	if err := m.Reconcile(t.Context(), nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	billed, _ := m.CollectTraffic(t.Context())
	if got := totalBytes(billed); got != 8000 {
		t.Fatalf("a second teardown pass billed %d of the 8000 bytes the first one banked", got)
	}
}

func TestDisablingAnInboundBanksItsLastInterval(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t, peer(t, 1, "a@example.com", "10.9.0.2/32"))
	if err := m.Reconcile(t.Context(), []wireguard.Instance{inst}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	primed(t, m)
	k.FeedTraffic(iface, key(t, 1), 500000, 1500000)

	// A disabled inbound leaves DesiredInstances, so supervision reconciles it away.
	if err := m.Reconcile(t.Context(), nil); err != nil {
		t.Fatalf("Reconcile without the inbound: %v", err)
	}
	billed, _ := m.CollectTraffic(t.Context())
	if got := totalBytes(billed); got != 2000000 {
		t.Fatalf("the client moved 2000000 bytes before its inbound was disabled and was billed %d", got)
	}
}

func TestARevokedClientIsBilledInFullWhenItComesBack(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	a := peer(t, 1, "a@example.com", "10.9.0.2/32")
	b := peer(t, 2, "b@example.com", "10.9.0.3/32")
	both := instance(t, a, b)
	if err := m.Ensure(t.Context(), both); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	primed(t, m)
	k.FeedTraffic(iface, key(t, 1), 3000, 3000)
	if billed, _ := m.CollectTraffic(t.Context()); totalBytes(billed) != 6000 {
		t.Fatalf("the first interval billed %d, want 6000", totalBytes(billed))
	}

	if err := m.Ensure(t.Context(), instance(t, b)); err != nil {
		t.Fatalf("Ensure without the revoked client: %v", err)
	}
	m.CollectTraffic(t.Context())
	if err := m.Ensure(t.Context(), both); err != nil {
		t.Fatalf("Ensure with the client back: %v", err)
	}
	// The kernel gives a re-added peer fresh counters, so the whole reading is new.
	k.FeedTraffic(iface, key(t, 1), 4000, 4000)
	billed, _ := m.CollectTraffic(t.Context())
	if got := totalBytes(billed); got != 8000 {
		t.Fatalf("the re-added client moved 8000 bytes and was billed %d; the revoked peer's baseline outlived it", got)
	}
}

// The only peer's revocation empties the readings map, and an empty reading ages
// no baseline out, so this window never closes on its own.
func TestTheOnlyClientIsBilledInFullWhenItComesBack(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	a := peer(t, 1, "a@example.com", "10.9.0.2/32")
	if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	primed(t, m)
	k.FeedTraffic(iface, key(t, 1), 3000, 3000)
	if billed, _ := m.CollectTraffic(t.Context()); totalBytes(billed) != 6000 {
		t.Fatalf("the first interval billed %d, want 6000", totalBytes(billed))
	}

	if err := m.Ensure(t.Context(), instance(t)); err != nil {
		t.Fatalf("Ensure with no clients: %v", err)
	}
	for range 50 {
		m.CollectTraffic(t.Context())
	}
	if err := m.Ensure(t.Context(), instance(t, a)); err != nil {
		t.Fatalf("Ensure with the client back: %v", err)
	}
	k.FeedTraffic(iface, key(t, 1), 4000, 4000)
	billed, _ := m.CollectTraffic(t.Context())
	if got := totalBytes(billed); got != 8000 {
		t.Fatalf("the re-added client moved 8000 bytes and was billed %d after 50 empty scrapes", got)
	}
}

func TestTwoClientsSharingOneAllowedIPDoNotOscillate(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	inst := instance(t,
		peer(t, 1, "a@example.com", "10.9.0.2/32"),
		peer(t, 2, "b@example.com", "10.9.0.2/32"),
	)

	err := m.Ensure(t.Context(), inst)
	want := `clients "a@example.com" and "b@example.com" share allowed-IP 10.9.0.2/32`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Ensure = %v, want it to name %s", err, want)
	}
	if got := served(t, k); !slices.Equal(got, []string{key(t, 1).String()}) {
		t.Fatalf("device serves %v, want the first claimant alone", got)
	}

	configures := k.Configures
	if err := m.Ensure(t.Context(), inst); err == nil {
		t.Fatalf("second Ensure = nil, want the same rejection")
	}
	if k.Configures != configures {
		t.Fatalf("configures = %d, was %d; the kernel moves a shared prefix to the later peer, so pushing both rewrites the device every pass forever", k.Configures, configures)
	}
}

// The fake has to land where the kernel lands, or the engine looks converged on a
// state no real device ever holds.
func TestASharedAllowedIPMovesToTheLaterPeer(t *testing.T) {
	k := wgtest.New()
	m := wireguard.NewManager(k)
	if err := m.Ensure(t.Context(), instance(t, peer(t, 1, "a@example.com", "10.9.0.2/32"))); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := m.AddPeer(t.Context(), instance(t), peer(t, 2, "b@example.com", "10.9.0.2/32")); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if got := k.AllowedIPs(iface, key(t, 1)); len(got) != 0 {
		t.Fatalf("the earlier peer still holds %v; the kernel moves the prefix rather than duplicating it", got)
	}
	if got := k.AllowedIPs(iface, key(t, 2)); len(got) != 1 || got[0].String() != "10.9.0.2/32" {
		t.Fatalf("the later peer holds %v, want [10.9.0.2/32]", got)
	}
}
