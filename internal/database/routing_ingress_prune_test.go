package database

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An inbound deleted by a path that never ran the routing prune left its id
in routing_rules.ingress_ids, and resyncPostgresSequences then let a new
inbound be created on that id, inheriting a rule it never asked for. The
startup sweep now drops deleted ids from every selected-scope rule and
switches off a rule left naming nothing. Feature gap O6.
*/
func TestStartupSweepDropsDeletedInboundsFromRoutingRules(t *testing.T) {
	initTestDB(t)
	mk := func(tag string, port int) *model.Inbound {
		t.Helper()
		ib := &model.Inbound{UserId: 1, Remark: tag, Port: port, Protocol: model.VLESS, Tag: tag, Settings: `{"clients":[]}`, StreamSettings: `{"network":"tcp"}`, Enable: true}
		if err := db.Create(ib).Error; err != nil {
			t.Fatalf("create inbound %s: %v", tag, err)
		}
		return ib
	}
	survivor := mk("stays", 24301)
	doomed := mk("goes", 24302)
	rules := []*model.RoutingRule{
		{Enable: true, Remark: "both", IngressScope: model.RoutingScopeSelected, IngressIds: `[` + strconv.Itoa(survivor.Id) + `,` + strconv.Itoa(doomed.Id) + `]`, DestKind: model.RoutingDestOutbound, DestTag: "direct"},
		{Enable: true, Remark: "only the doomed", IngressScope: model.RoutingScopeSelected, IngressIds: `[` + strconv.Itoa(doomed.Id) + `]`, DestKind: model.RoutingDestOutbound, DestTag: "direct"},
	}
	for _, r := range rules {
		if err := db.Create(r).Error; err != nil {
			t.Fatalf("create rule %q: %v", r.Remark, err)
		}
	}
	if err := db.Delete(&model.Inbound{}, doomed.Id).Error; err != nil {
		t.Fatalf("delete inbound: %v", err)
	}

	if err := PruneOrphansForTest(); err != nil {
		t.Fatalf("PruneOrphansForTest: %v", err)
	}
	var both, alone model.RoutingRule
	if err := db.First(&both, rules[0].Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := db.First(&alone, rules[1].Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if both.IngressIds != `[`+strconv.Itoa(survivor.Id)+`]` || !both.Enable {
		t.Fatalf("rule naming both = ingress %q enable %v, want only the survivor and still on", both.IngressIds, both.Enable)
	}
	if alone.IngressIds != "[]" || alone.Enable || !strings.Contains(alone.Remark, "deleted") {
		t.Fatalf("rule naming only the deleted inbound = ingress %q enable %v remark %q, want emptied, off and saying why", alone.IngressIds, alone.Enable, alone.Remark)
	}
}
