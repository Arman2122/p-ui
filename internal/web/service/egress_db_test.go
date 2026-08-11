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

func TestEgressAttachRefusals(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	service := &EgressService{}

	enabled := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	disabled := seedEgress(t, &model.Egress{Type: "xray-tun", Target: "direct"})
	node := 1
	cases := []struct {
		name    string
		inbound *model.Inbound
		egress  *model.Egress
		want    error
	}{
		{
			name:    "a protocol with no ingress device cannot be selected on",
			inbound: &model.Inbound{UserId: 1, Tag: "in-vless", Port: 30001, Protocol: model.VLESS, Settings: `{"clients":[]}`},
			egress:  enabled,
			want:    ErrEgressNoIngressDevice,
		},
		{
			name: "an inbound on a node is out of scope while the id is a global key",
			inbound: &model.Inbound{
				UserId: 1, Tag: "in-node", Port: 30002, Protocol: model.WGKernel,
				NodeID: &node, Settings: `{"clients":[]}`,
			},
			egress: enabled,
			want:   ErrEgressMasterLocal,
		},
		{
			name:    "a disabled egress installs no containment, so attaching would egress direct",
			inbound: wgKernelInbound("in-disabled", 30003),
			egress:  disabled,
			want:    ErrEgressDisabled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inbound := seedInbound(t, tc.inbound)
			err := service.Attach(inbound.Id, tc.egress.Id)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Attach = %v, want %v", err, tc.want)
			}
			stored := &model.Inbound{}
			if err := database.GetDB().First(stored, inbound.Id).Error; err != nil {
				t.Fatal(err)
			}
			if stored.EgressID != nil {
				t.Fatalf("a refused attach must leave the inbound unattached, got egressId %d", *stored.EgressID)
			}
		})
	}
}

// Attach is synchronous because a tick that caught up later leaves the inbound
// egressing with the server's own identity in the meantime.
func TestEgressAttachInstallsTheRuleBeforeItReturns(t *testing.T) {
	initTestDB(t)
	kernel := withFakeEgressKernel(t)
	service := &EgressService{}

	row := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	inbound := seedInbound(t, wgKernelInbound("in-wg", 30010))

	if err := service.Attach(inbound.Id, row.Id); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	device := "pwg" + strconv.Itoa(inbound.Id)
	table, prio := 30000+row.Id, 31000+row.Id
	wantRules := []string{
		egress.RuleSpec{Family: egress.FamilyV4, Priority: prio, Iif: device, Table: table}.String(),
		egress.RuleSpec{Family: egress.FamilyV6, Priority: prio, Iif: device, Table: table}.String(),
	}
	if got := kernel.Rules(); !slices.Equal(got, wantRules) {
		t.Fatalf("rules after attach = %v, want %v", got, wantRules)
	}
	// The blackhole is part of the table's identity: a rule pointing at a table
	// with no match falls through to main and out with the server's address. The
	// preflight probe's own reversible rule points at no egress table, so it is
	// not one of the rules this ordering is about.
	ops := kernel.Ops()
	pointsAtTheTable := func(op string) bool {
		return strings.HasPrefix(op, "rule+") && strings.HasSuffix(op, "lookup "+strconv.Itoa(table))
	}
	firstRule := slices.IndexFunc(ops, pointsAtTheTable)
	firstRoute := slices.IndexFunc(ops, func(op string) bool { return strings.HasPrefix(op, "route+") })
	if firstRule < 0 || firstRoute < 0 || firstRoute > firstRule {
		t.Fatalf("containment must be installed before any rule points at it, ops were %v", ops)
	}

	if err := service.Attach(inbound.Id, 0); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if got := kernel.Rules(); len(got) != 0 {
		t.Fatalf("detach must remove the rules, still holding %v", got)
	}
	stored := &model.Egress{}
	if err := database.GetDB().First(stored, row.Id).Error; err != nil {
		t.Fatalf("detach must not touch the egress row: %v", err)
	}
}

/*
Attach on a host that already runs an egress — the panel's steady state.

Every enabled xray-tun egress makes Xray put Gateway(base, id) on peg<id>, so by
the time a second attach is attempted those addresses are on the box. Preflight
walks every host address against the gateway base, and counting the panel's own
fronts as foreign refuses every attach from the first front onwards.
*/
func TestEgressAttachOnAHostWhoseFrontsAreAlreadyUp(t *testing.T) {
	initTestDB(t)
	kernel := withFakeEgressKernel(t)
	service := &EgressService{}

	first := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	second := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	for _, row := range []*model.Egress{first, second} {
		device := egress.Device(row.Id)
		gateway, err := egress.Gateway(egress.DefaultGatewayBase, row.Id)
		if err != nil {
			t.Fatalf("gateway for egress %d: %v", row.Id, err)
		}
		kernel.AddLink(device, "net.ipv4.conf."+device+".rp_filter")
		kernel.AddAddr(device, gateway)
	}

	inbound := seedInbound(t, wgKernelInbound("in-steady", 30050))
	if err := service.Attach(inbound.Id, second.Id); err != nil {
		t.Fatalf("Attach on a host carrying the panel's own fronts: %v", err)
	}
	stored := &model.Inbound{}
	if err := database.GetDB().First(stored, inbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EgressID == nil || *stored.EgressID != second.Id {
		t.Fatalf("egressId = %v, want %d", stored.EgressID, second.Id)
	}
}

/*
One unhealthy row must not revert an attach to a healthy one.

Reconcile converges the whole host and joins every row's failure, so reverting on
any error at all hands one permanently broken egress — or one id left behind by a
row somebody deleted — a veto over every attach on the box.
*/
func TestEgressAttachSurvivesAnUnrelatedRowFailing(t *testing.T) {
	initTestDB(t)
	kernel := withFakeEgressKernel(t)
	service := &EgressService{}

	broken := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	healthy := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	kernel.AddLink(egress.Device(healthy.Id))
	kernel.AddAddr(egress.Device(healthy.Id), mustEgressGateway(t, healthy.Id))
	kernel.Fail = map[string]error{
		"route+ v4 table " + strconv.Itoa(30000+broken.Id) + " blackhole default metric 4096": errors.New("the host refused it"),
	}

	inbound := seedInbound(t, wgKernelInbound("in-healthy", 30060))
	if err := service.Attach(inbound.Id, healthy.Id); err != nil {
		t.Fatalf("Attach = %v, want it to survive an unrelated row's failure", err)
	}
	stored := &model.Inbound{}
	if err := database.GetDB().First(stored, inbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EgressID == nil || *stored.EgressID != healthy.Id {
		t.Fatalf("egressId = %v, want %d — an unrelated failure reverted the attachment", stored.EgressID, healthy.Id)
	}
}

// An attach whose OWN rule did not reach the host is still reverted: attached but
// unrouted means egressing with the server's identity while the panel says otherwise.
func TestEgressAttachRevertsWhenItsOwnRuleIsRefused(t *testing.T) {
	initTestDB(t)
	kernel := withFakeEgressKernel(t)
	service := &EgressService{}

	row := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	inbound := seedInbound(t, wgKernelInbound("in-refused", 30070))
	device := "pwg" + strconv.Itoa(inbound.Id)
	kernel.Fail = map[string]error{
		"rule+ v6 prio " + strconv.Itoa(31000+row.Id) + " iif " + device + " lookup " + strconv.Itoa(30000+row.Id): errors.New("the host refused it"),
	}

	err := service.Attach(inbound.Id, row.Id)
	if !errors.Is(err, ErrEgressNotRouted) {
		t.Fatalf("Attach = %v, want %v", err, ErrEgressNotRouted)
	}
	stored := &model.Inbound{}
	if err := database.GetDB().First(stored, inbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EgressID != nil {
		t.Fatalf("a half-installed attachment must be reverted, egressId is %d", *stored.EgressID)
	}
	// The v4 rule installed before v6 was refused, so the revert has to reach the
	// host too: "detached but routed" is the mirror of what the revert prevents.
	if got := kernel.Rules(); len(got) != 0 {
		t.Fatalf("kernel rules = %v, want none — the database says this inbound is attached to nothing", got)
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

	row := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	inbound := seedInbound(t, wgKernelInbound("in-restart", 30080))
	if err := service.Attach(inbound.Id, row.Id); err != nil {
		t.Fatalf("Attach: %v", err)
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

func TestEgressDeleteAndDisableAreRefusedWhileAttached(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	service := &EgressService{}

	row := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	inbound := seedInbound(t, wgKernelInbound("in-held", 30020))
	if err := service.Attach(inbound.Id, row.Id); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	if err := service.Del(row.Id); !errors.Is(err, ErrEgressInUse) {
		t.Fatalf("Del = %v, want %v", err, ErrEgressInUse)
	}
	disabling := *row
	disabling.Enable = false
	if _, err := service.Update(&disabling); !errors.Is(err, ErrEgressInUse) {
		t.Fatalf("disabling Update = %v, want %v", err, ErrEgressInUse)
	}
}

/*
An ordinary inbound edit must not detach the egress.

The update payload does not carry egressId — attach is its own operation — so a
caller that never learned about egresses, or a frontend built before the column
existed, must leave the attachment exactly where it was.
*/
func TestInboundUpdateKeepsTheEgressAttachment(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	egressService := &EgressService{}

	row := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	inbound := seedInbound(t, wgKernelInbound("in-edited", 30030))
	if err := egressService.Attach(inbound.Id, row.Id); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	edit := wgKernelInbound("in-edited", 30031)
	edit.Id = inbound.Id
	edit.Remark = "renamed"
	inboundService := &InboundService{}
	if _, _, err := inboundService.UpdateInbound(edit); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	stored := &model.Inbound{}
	if err := database.GetDB().First(stored, inbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EgressID == nil || *stored.EgressID != row.Id {
		t.Fatalf("an edit that never mentioned the egress silently detached it, egressId is now %v", stored.EgressID)
	}
	if stored.Remark != "renamed" {
		t.Fatalf("the edit itself must still land, remark is %q", stored.Remark)
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

	row := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	local := seedInbound(t, wgKernelInbound("in-local", 30040))
	node := 1
	remote := seedInbound(t, &model.Inbound{
		UserId: 1, Tag: "in-remote", Port: 30041, Protocol: model.WGKernel,
		NodeID: &node, Settings: `{"clients":[]}`,
	})
	stream := seedInbound(t, &model.Inbound{
		UserId: 1, Tag: "in-stream", Port: 30042, Protocol: model.VLESS, Settings: `{"clients":[]}`,
	})
	// Straight to the column: both of these are exactly what Attach refuses, and
	// the reconciler must reach the same answer about rows already in the table.
	for _, id := range []int{local.Id, remote.Id, stream.Id} {
		err := database.GetDB().Model(&model.Inbound{}).Where("id = ?", id).
			Update("egress_id", row.Id).Error
		if err != nil {
			t.Fatal(err)
		}
	}

	desired, err := service.desired([]*model.Egress{row})
	if err != nil {
		t.Fatal(err)
	}
	if len(desired) != 1 {
		t.Fatalf("expected one desired egress, got %+v", desired)
	}
	want := []string{"pwg" + strconv.Itoa(local.Id)}
	if !slices.Equal(desired[0].Ingress, want) {
		t.Fatalf("ingress devices = %v, want %v", desired[0].Ingress, want)
	}
}

/*
An inbound edited off wgkernel has no ingress device, so desired() drops it from
the egress and the reconciler withdraws its rule — but the column it is counted
by is still set, and checkNotReferenced counts by column alone. The egress is
then refused for delete AND for disable, and the UI offers no way to detach.
*/
func TestAProtocolChangeReleasesTheEgress(t *testing.T) {
	initTestDB(t)
	kernel := withFakeEgressKernel(t)
	egressSvc := &EgressService{}
	inboundSvc := &InboundService{}

	row := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	inbound := seedInbound(t, wgKernelInbound("in-morphing", 30090))
	if err := egressSvc.Attach(inbound.Id, row.Id); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(kernel.Rules()) == 0 {
		t.Fatal("the attachment installed no rule")
	}

	edited := &model.Inbound{
		Id: inbound.Id, UserId: 1, Tag: inbound.Tag, Remark: inbound.Remark, Enable: true,
		Listen: inbound.Listen, Port: inbound.Port, Protocol: model.VLESS,
		Settings: `{"clients":[]}`,
	}
	if _, _, err := inboundSvc.UpdateInbound(edited); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	stored := &model.Inbound{}
	if err := database.GetDB().First(stored, inbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EgressID != nil {
		t.Fatalf("egressId = %d after the inbound stopped being selectable", *stored.EgressID)
	}
	if err := egressSvc.Del(row.Id); err != nil {
		t.Fatalf("Del = %v; nothing selects this egress any more", err)
	}
}

// The error has to name the inbounds, or an operator has no way to find them:
// the picker only renders for a protocol that still has an ingress device.
func TestAReferencedEgressNamesTheInboundsHoldingIt(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	egressSvc := &EgressService{}

	row := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	inbound := seedInbound(t, wgKernelInbound("in-named", 30091))
	if err := egressSvc.Attach(inbound.Id, row.Id); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	err := egressSvc.Del(row.Id)
	if !errors.Is(err, ErrEgressInUse) || !strings.Contains(err.Error(), "["+strconv.Itoa(inbound.Id)+"]") {
		t.Fatalf("Del = %v, want it to name inbound %d", err, inbound.Id)
	}
}

/*
A refused attach reverts the row, and the host has to come back with it.

Reconcile has already installed the rejected attachment by then, so without a
second pass the panel reports one state and the kernel routes another: "detached
but routed" is the mirror image of the thing verifyAttachment exists to prevent.
*/
func TestARefusedAttachTakesItsKernelStateBackToo(t *testing.T) {
	initTestDB(t)
	kernel := withFakeEgressKernel(t)
	service := &EgressService{}

	first := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	second := seedEgress(t, &model.Egress{Type: "xray-tun", Enable: true, Target: "direct"})
	inbound := seedInbound(t, wgKernelInbound("in-moving", 30092))
	if err := service.Attach(inbound.Id, second.Id); err != nil {
		t.Fatalf("Attach to the second egress: %v", err)
	}

	device := "pwg" + strconv.Itoa(inbound.Id)
	kernel.Fail = map[string]error{
		"rule+ v6 prio " + strconv.Itoa(31000+first.Id) + " iif " + device + " lookup " + strconv.Itoa(30000+first.Id): errors.New("the host refused it"),
	}
	if err := service.Attach(inbound.Id, first.Id); !errors.Is(err, ErrEgressNotRouted) {
		t.Fatalf("Attach = %v, want %v", err, ErrEgressNotRouted)
	}

	stored := &model.Inbound{}
	if err := database.GetDB().First(stored, inbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EgressID == nil || *stored.EgressID != second.Id {
		t.Fatalf("egressId = %v, want the attachment reverted to %d", stored.EgressID, second.Id)
	}
	want := []string{
		"v4 prio " + strconv.Itoa(31000+second.Id) + " iif " + device + " lookup " + strconv.Itoa(30000+second.Id),
		"v6 prio " + strconv.Itoa(31000+second.Id) + " iif " + device + " lookup " + strconv.Itoa(30000+second.Id),
	}
	if got := kernel.Rules(); !slices.Equal(got, want) {
		t.Fatalf("kernel rules = %v, want %v — the database says egress %d and the host must agree", got, want, second.Id)
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
