package service

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/egtest"
)

// withFakeEgressKernel swaps the host stack for the in-memory stand-in, so a DB
// test converges a real egress without writing an ip rule on the machine.
func withFakeEgressKernel(t *testing.T) *egtest.Kernel {
	t.Helper()
	kernel := egtest.New()
	previous := egressManager
	egressManager = egress.New(kernel, egressDriverRegistry)
	t.Cleanup(func() { egressManager = previous })
	return kernel
}

func seedEgress(t *testing.T, row *model.Egress) *model.Egress {
	t.Helper()
	if err := database.GetDB().Create(row).Error; err != nil {
		t.Fatalf("seed egress: %v", err)
	}
	return row
}

func seedInbound(t *testing.T, inbound *model.Inbound) *model.Inbound {
	t.Helper()
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	return inbound
}

func wgKernelInbound(tag string, port int) *model.Inbound {
	return &model.Inbound{
		UserId: 1, Tag: tag, Remark: tag, Enable: true, Port: port,
		Protocol: model.WGKernel,
		Settings: `{"clients":[]}`,
	}
}

/*
The kick a core restart needs: the front is the core's, and it appears after the
process does — measured 20 ms on a bare config, 535 ms on a geoip/geosite one.

A single immediate pass would find no device and repair nothing, leaving every
attached inbound contained until the 10s drift tick.
*/
func TestEgressRestartKickWaitsForTheFrontDevice(t *testing.T) {
	initTestDB(t)
	kernel := withFakeEgressKernel(t)
	service := &EgressService{}

	// The schedule's SHAPE is production's, only faster, so a kick that stopped
	// after its first pass fails here rather than passing on a shortened list.
	previous := egressRestartKickDelays
	sped := make([]time.Duration, len(previous))
	for i, delay := range previous {
		sped[i] = delay / 50
	}
	egressRestartKickDelays = sped
	t.Cleanup(func() { egressRestartKickDelays = previous })

	inbound := seedInbound(t, wgKernelInbound("in-restart", 30080))
	row := seedEgress(t, &model.Egress{
		Type: "xray-tun", Enable: true, Target: "direct",
		Owner: "panel", IngressInboundId: &inbound.Id,
	})
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	device := egress.Device(row.Id)
	front := "v4 table " + strconv.Itoa(30000+row.Id) + " default dev " + device + " metric 100"
	if slices.Contains(kernel.Routes(), front) {
		t.Fatal("the front route cannot exist before the core created its device")
	}

	passes := kernel.Snapshots()
	kickEgressAfterCoreRestart()
	// The core is slower than the first pass, which is the whole reason the kick
	// is a short ladder and not one immediate call.
	waitFor(t, "a reconcile pass with the front still absent", func() bool { return kernel.Snapshots() > passes })
	kernel.AddLink(device, "net.ipv4.conf."+device+".rp_filter")
	kernel.AddAddr(device, mustEgressGateway(t, row.Id))

	waitFor(t, "the front route restored after the core came back", func() bool {
		return slices.Contains(kernel.Routes(), front)
	})
	// The kick outlives the call, and a pass that ran after this test swapped the
	// fake back would converge the real host against another test's rows.
	waitFor(t, "the kick to run out of passes", func() bool {
		return kernel.Snapshots() >= passes+len(sped)
	})
}

// mustEgressGateway is the /32 the core puts on the front it creates. A device
// without it is one the kernel never produces, and never the panel's front.
func mustEgressGateway(t *testing.T, id int) netip.Prefix {
	t.Helper()
	gateway, err := egress.Gateway(egress.DefaultGatewayBase, id)
	if err != nil {
		t.Fatalf("gateway for egress %d: %v", id, err)
	}
	return gateway
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

/*
A routing rule is how an exit is used now, and it must hold the row down.

The attach column this guard used to read is written NIL in production — the
routing migration nulled every one and both inbound write paths clear it — so a
guard that only asked it said "nothing references this" about an uplink live
rules route through, and deleting it moved that traffic to the server's own
identity while the panel reported success.
*/
func TestEgressDeleteAndDisableAreRefusedWhileARuleRoutesToIt(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	service := &EgressService{}

	uplink := seedEgress(t, &model.Egress{Type: "wg-client", Enable: true, Owner: "operator"})
	rule := &model.RoutingRule{
		Enable: true, Remark: "office traffic",
		IngressScope: model.RoutingScopeAll,
		DestKind:     model.RoutingDestExit, DestExitId: &uplink.Id,
	}
	if err := database.GetDB().Create(rule).Error; err != nil {
		t.Fatalf("seed routing rule: %v", err)
	}

	if err := service.Del(uplink.Id); !errors.Is(err, ErrEgressInUse) {
		t.Fatalf("Del = %v, want %v", err, ErrEgressInUse)
	}
	disabling := *uplink
	disabling.Enable = false
	if _, err := service.Update(&disabling); !errors.Is(err, ErrEgressInUse) {
		t.Fatalf("disabling Update = %v, want %v", err, ErrEgressInUse)
	}

	// The refusal must name the rule, or an operator cannot act on it.
	err := service.Del(uplink.Id)
	if want := strconv.Itoa(rule.Id); !strings.Contains(err.Error(), want) {
		t.Fatalf("Del error %q does not name rule %s", err, want)
	}

	// A rule pointing somewhere else must not hold it down.
	other := 0
	if err := database.GetDB().Model(rule).Updates(map[string]any{
		"dest_kind": model.RoutingDestDirect, "dest_exit_id": &other,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.Del(uplink.Id); err != nil {
		t.Fatalf("Del after the rule stopped naming it = %v, want nil", err)
	}
}

/*
The schema, not the service, is what closes the delete-racing-an-attach window.

checkNotReferenced and the DELETE it justifies are two statements apart, so an
UPDATE landing between them leaves an inbound referencing a row that is gone —
desired() then emits no rule for an id it never read, and that inbound egresses
with the server's own identity while the panel still reports it attached.
*/
/*
An egress target is resolved once, inside validate(), and never again. Delete the
outbound behind a live egress and the row still reads enabled, Selects still
answers "routed", and everything attached to it is quietly contained.

Preflight is the surface that already carries host facts to the operator, so the
answer goes there rather than into a new column and a new response field.
*/
func TestEgressPreflightNamesARowWhoseTargetDied(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	service := &EgressService{}

	healthy := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	dead := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "warp-since-deleted"})
	off := seedEgress(t, &model.Egress{Type: "xray-tun", Target: "warp-since-deleted"})

	report := service.Preflight(context.Background())
	var named []string
	for _, note := range report.Notes {
		if strings.Contains(note, "warp-since-deleted") {
			named = append(named, note)
		}
	}
	if len(named) != 1 {
		t.Fatalf("notes naming the dead target = %v, want exactly one; every note was %v", named, report.Notes)
	}
	want := "egress " + strconv.Itoa(dead.Id) + ` targets "warp-since-deleted", which is no longer an outbound or a balancer tag, so everything attached to it is contained rather than routed`
	if named[0] != want {
		t.Fatalf("note = %q, want %q", named[0], want)
	}
	// A disabled row installs nothing, which is why validate() lets a dead-target
	// egress still be switched off. Reporting it would be noise, not news.
	for _, note := range report.Notes {
		for _, quiet := range []int{healthy.Id, off.Id} {
			if strings.Contains(note, "egress "+strconv.Itoa(quiet)+" targets") {
				t.Fatalf("egress %d must not be reported: %q", quiet, note)
			}
		}
	}
}

/*
An uplink IS the destination, so it has no outbound tag to lose.

validate() has always known that — it returns before the target check for any
driver that is not an Injector. deadTargets() did not, so every enabled uplink
was reported as targeting "" and, worse, as containing the traffic attached to
it: the one row whose exit was working was the row the banner condemned.
*/
func TestEgressPreflightIsQuietAboutAnUplinksMissingTarget(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	service := &EgressService{}

	uplink := seedEgress(t, &model.Egress{
		Type: "wg-client", Enable: true, Remark: "US-sfo | Surfshark",
		Settings: `{"privateKey":"a","address":["10.2.0.2/32"],"publicKey":"b","endpoint":"us-sfo.example:51820"}`,
	})

	report := service.Preflight(context.Background())
	for _, note := range report.Notes {
		if strings.Contains(note, "egress "+strconv.Itoa(uplink.Id)+" targets") {
			t.Fatalf("an uplink has no outbound target to lose, but preflight said: %q", note)
		}
	}
}

func TestEgressSchemaConstraints(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	row := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	inbound := seedInbound(t, wgKernelInbound("in-constrained", 30090))

	t.Run("an inbound cannot reference an egress that does not exist", func(t *testing.T) {
		err := db.Exec("UPDATE inbounds SET egress_id = ? WHERE id = ?", row.Id+9000, inbound.Id).Error
		if err == nil {
			t.Fatal("the column accepted a reference to a row that is not there")
		}
	})
	t.Run("a referenced egress cannot be deleted behind the service's back", func(t *testing.T) {
		if err := db.Exec("UPDATE inbounds SET egress_id = ? WHERE id = ?", row.Id, inbound.Id).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("DELETE FROM egresses WHERE id = ?", row.Id).Error; err == nil {
			t.Fatal("an egress an inbound still selects was deleted, so that inbound now egresses direct")
		}
		if err := db.Exec("UPDATE inbounds SET egress_id = NULL WHERE id = ?", inbound.Id).Error; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("an id outside the band is refused", func(t *testing.T) {
		// Every kernel object is derived from the id, so a row out of band would put
		// a route in somebody else's table and a rule at somebody else's priority.
		err := db.Exec(`INSERT INTO egresses (id, type, enable, remark, target, settings, created_at, updated_at)
			VALUES (1000, 'xray-tun', true, '', 'direct', '', 0, 0)`).Error
		if err == nil {
			t.Fatal("egress id 1000 was accepted, and every resource it derives is out of the reserved band")
		}
	})
	t.Run("the migration is idempotent", func(t *testing.T) {
		if err := database.InitDB(); err != nil {
			t.Fatalf("a second migration pass: %v", err)
		}
	})
}

// desired is what the reconciler converges, so an inbound the panel does not
// serve locally must not contribute a rule for a device that is not on this host.
func TestEgressDesiredSkipsInboundsWithoutALocalDevice(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	service := &EgressService{}

	local := seedInbound(t, wgKernelInbound("in-local", 30040))
	node := 1
	remote := seedInbound(t, &model.Inbound{
		UserId: 1, Tag: "in-remote", Port: 30041, Protocol: model.WGKernel,
		NodeID: &node, Settings: `{"clients":[]}`,
	})
	stream := seedInbound(t, &model.Inbound{
		UserId: 1, Tag: "in-stream", Port: 30042, Protocol: model.VLESS, Settings: `{"clients":[]}`,
	})
	// A front per inbound, which is how routing provisions them — including for
	// the two the reconciler must then refuse to build an ingress rule for.
	fronts := make([]*model.Egress, 0, 3)
	for _, in := range []*model.Inbound{local, remote, stream} {
		id := in.Id
		fronts = append(fronts, seedEgress(t, &model.Egress{
			Type: "xray-tun", Enable: true, Target: "direct",
			Owner: "panel", IngressInboundId: &id,
		}))
	}

	desired, err := service.desired(context.Background(), fronts)
	if err != nil {
		t.Fatal(err)
	}
	if len(desired) != 3 {
		t.Fatalf("expected three desired egresses, got %+v", desired)
	}
	want := []string{"pwg" + strconv.Itoa(local.Id)}
	if !slices.Equal(desired[0].Ingress, want) {
		t.Fatalf("the local wgkernel inbound's front = %v, want %v", desired[0].Ingress, want)
	}
	// A node inbound is another host's business and a stream protocol has no
	// ingress device at all; neither may contribute an iif rule here.
	if len(desired[1].Ingress) != 0 {
		t.Fatalf("a node inbound's front got ingress %v, want none", desired[1].Ingress)
	}
	if len(desired[2].Ingress) != 0 {
		t.Fatalf("a stream inbound's front got ingress %v, want none", desired[2].Ingress)
	}
}

/*
An egress id names host-global kernel state — routing table 30000+id, ip rule
priority 31000+id and device peg<id> — so it must never be handed out twice.

The guard is a boot-time one: resyncPostgresSequences rewinds every sequence to
MAX(id), which would re-issue the id of a row somebody deleted, together with
whatever kernel state EgressService.Del could not take down.
*/
func TestAnEgressIDIsNeverHandedOutTwice(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	service := &EgressService{}

	issued := make([]int, 0, 4)
	for range 3 {
		row, err := service.Add(&model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		issued = append(issued, row.Id)
	}
	newest := issued[len(issued)-1]
	if err := service.Del(newest); err != nil {
		t.Fatalf("Del(%d): %v", newest, err)
	}

	// The reboot: InitDB replays initModels, and with it the sequence resync.
	if err := database.InitDB(); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	row, err := service.Add(&model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	if err != nil {
		t.Fatalf("Add after the reboot: %v", err)
	}
	if slices.Contains(issued, row.Id) {
		t.Fatalf("egress id %d was handed out twice (already issued %v) — it names routing table %d, rule priority %d and device %s",
			row.Id, issued, 30000+row.Id, 31000+row.Id, egress.Device(row.Id))
	}
}
