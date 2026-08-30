package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/routing"
)

/*
Viewing the config must not retire a front the running core still uses.

`GetXrayConfig` is named like a getter and reads like one, but the compile it
runs ends in `reapFronts`, which DELETES every panel-owned egress row no current
rule names -- and EgressService.Del tears down the `ip rule`, the routing table
and the blackhole that go with it.

Two callers reach it without applying anything: the "view config" button, and
the pre-flight that checks whether a newly downloaded Xray binary can load the
config. So opening a settings page could delete kernel state the RUNNING core is
still forwarding through, and the update pre-flight could do it to a panel that
is mid-upgrade.

Driven through GetConfigJson rather than GetXrayConfig on purpose: its signature
does not change, so reverting the fix leaves this test compiling and RED. A test
written against GetXrayConfig(false) would fail to compile instead, which
certifies nothing. F054.
*/
func TestViewingTheConfigDoesNotRetireAFront(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	db := database.GetDB()

	// An inbound, and a panel-owned front for it that no routing rule names --
	// exactly the row reapFronts exists to collect.
	ib := &model.Inbound{
		Tag: "tun-in", Enable: true, Listen: "0.0.0.0", Port: 34100,
		Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`,
		Settings: `{"clients":[],"decryption":"none"}`,
	}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	front := &model.Egress{
		Type: "xray-tun", Enable: true, Owner: model.EgressOwnerPanel,
		Remark: "front for tun-in", IngressInboundId: &ib.Id,
	}
	if err := db.Create(front).Error; err != nil {
		t.Fatalf("seed front: %v", err)
	}

	if _, err := (&ServerService{}).GetConfigJson(); err != nil {
		t.Fatalf("GetConfigJson: %v", err)
	}

	var count int64
	if err := database.GetDB().Model(&model.Egress{}).Where("id = ?", front.Id).Count(&count).Error; err != nil {
		t.Fatalf("re-count the front: %v", err)
	}
	if count == 0 {
		t.Fatal("viewing the config deleted a panel-owned egress front; EgressService.Del also removes its ip rule, routing table and blackhole, so a read tore down kernel state the running core is still forwarding through")
	}
}

/*
A disabled inbound keeps its front, and its id.

Subjects() reads enabled inbounds only, so a disabled one has no subject, its
front is not in the keep set, and reapFronts retired it. Re-enabling then created
a fresh row with a fresh id — and every resource the egress derives is computed
from that id: the routing table number, the device name, the fwmark.

So a disable/enable cycle renamed the device and moved the table, and burned one
of the 999 ids the entire scheme has. An operator toggling an inbound a few
times a week walks through the band.

The row costs nothing while the inbound is down, because nothing renders that
inbound and nothing selects its device. F070.
*/
func TestADisabledInboundKeepsItsFront(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	db := database.GetDB()

	ib := &model.Inbound{
		Tag: "tun-dormant", Enable: false, Listen: "0.0.0.0", Port: 34110,
		Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`,
		Settings: `{"clients":[],"decryption":"none"}`,
	}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	front := &model.Egress{
		Type: "xray-tun", Enable: true, Owner: model.EgressOwnerPanel,
		Remark: "front for tun-dormant", IngressInboundId: &ib.Id,
	}
	if err := db.Create(front).Error; err != nil {
		t.Fatalf("seed front: %v", err)
	}

	if err := (&RoutingService{}).reapFronts(map[int]int{}); err != nil {
		t.Fatalf("reapFronts: %v", err)
	}

	var count int64
	if err := db.Model(&model.Egress{}).Where("id = ?", front.Id).Count(&count).Error; err != nil {
		t.Fatalf("re-count the front: %v", err)
	}
	if count == 0 {
		t.Fatal("the front of a disabled inbound was retired; re-enabling mints a new id, which renames its device and moves its routing table, and every cycle spends one of the 999 ids the scheme has")
	}
}

// And a front whose ingress is enabled and named by nothing is still collected,
// which is the whole reason reapFronts exists.
func TestAnEnabledInboundNoRuleNamesStillLosesItsFront(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	db := database.GetDB()

	ib := &model.Inbound{
		Tag: "tun-awake", Enable: true, Listen: "0.0.0.0", Port: 34111,
		Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`,
		Settings: `{"clients":[],"decryption":"none"}`,
	}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	front := &model.Egress{
		Type: "xray-tun", Enable: true, Owner: model.EgressOwnerPanel,
		Remark: "front for tun-awake", IngressInboundId: &ib.Id,
	}
	if err := db.Create(front).Error; err != nil {
		t.Fatalf("seed front: %v", err)
	}

	if err := (&RoutingService{}).reapFronts(map[int]int{}); err != nil {
		t.Fatalf("reapFronts: %v", err)
	}

	var count int64
	if err := db.Model(&model.Egress{}).Where("id = ?", front.Id).Count(&count).Error; err != nil {
		t.Fatalf("re-count the front: %v", err)
	}
	if count != 0 {
		t.Fatal("a front no rule names, on an inbound that is up, survived: its ip rule keeps selecting the device into a table holding only a blackhole, so that inbound forwards nothing")
	}
}

/*
A front past the id band is refused, not stored — and the schema is what does it.

Every resource an egress derives is computed from its id: the routing table
number, the `ip rule` priority, the device name, the fwmark. EgressService.Add
checks the band in Go and deletes the row when the sequence overshoots. A panel
front does not go through Add, which is what F070 reports.

It turns out not to matter: `ck_egresses_id_band` (db.go) is a CHECK constraint
on the table itself, so the INSERT fails before any Go guard could run and no
out-of-band row can exist by any path. A Go check here would be unreachable
code, so there is none — this test is what says the protection is real, and
where it lives.

Driven by moving the sequence, which is the only way the condition arises. F070.
*/
func TestAFrontPastTheIdBandIsRefused(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	db := database.GetDB()

	ib := &model.Inbound{
		Tag: "tun-overflow", Enable: true, Listen: "0.0.0.0", Port: 34112,
		Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`,
		Settings: `{"clients":[],"decryption":"none"}`,
	}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	if err := db.Exec("ALTER SEQUENCE egresses_id_seq RESTART WITH 1000").Error; err != nil {
		t.Skipf("cannot move the egress id sequence on this database: %v", err)
	}

	subjects := []routing.Subject{{
		InboundID: ib.Id,
		Tag:       ib.Tag,
		Handle:    core.IngressHandle{Device: "pux1"},
	}}
	rules := []*model.RoutingRule{{
		Id: 1, Enable: true, IngressScope: model.RoutingScopeAll,
		DestKind: "direct", Criteria: "{}",
	}}

	_, err := (&RoutingService{}).ensureFronts(subjects, rules, false)
	if err == nil {
		t.Fatal("a front was created with an id outside 1..999; every resource it derives — its routing table, its ip rule priority, its device name — then belongs to something else on the box")
	}
	if !strings.Contains(err.Error(), "ck_egresses_id_band") {
		t.Fatalf("the refusal did not come from the id-band constraint, so something else stopped it and the band may not be guarded at all: %v", err)
	}

	var count int64
	if err := db.Model(&model.Egress{}).Where("ingress_inbound_id = ?", ib.Id).Count(&count).Error; err != nil {
		t.Fatalf("count fronts: %v", err)
	}
	if count != 0 {
		t.Fatal("the out-of-band row was left behind, exactly what EgressService.Add deletes")
	}
}
