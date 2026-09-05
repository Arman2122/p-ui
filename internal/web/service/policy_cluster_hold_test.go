package service

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A cap is decided from what is live NOW. An address the local cores stopped
reporting has ended — the core that held it answered, and it was not in the
answer — so a device that changes networks keeps its one slot instead of being
named over for the two minutes its old address used to stay counted.
*/
func TestAnAddressTheCoresStoppedReportingFreesItsSlotAtOnce(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	base := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(base))
	seedPolicyInbound(t, "vless", "vless-roam", 45601, "roamer")
	setLimitIP(t, "roamer", 1)

	base.sessions = observe("roamer", time.Now().Add(-time.Minute), "198.51.100.1")
	if _, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background())); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	// The phone moved networks: the old address is gone from the core's answer.
	base.sessions = observe("roamer", time.Now(), "198.51.100.2")
	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := overAddresses(verdicts, "roamer"); len(got) != 0 {
		t.Fatalf("the old address is live nowhere, so the new one holds the only slot; over = %v", got)
	}
	if got := storedAddresses(t, "roamer"); !slices.Contains(got, "198.51.100.1") {
		t.Fatalf("the old address is history, not forgotten: stored %v", got)
	}
}

/*
The other half: an address ANOTHER node reported inside the window still holds
its slot here, which is what makes the cap cluster-wide rather than per node.
*/
func TestAnAddressAnotherNodeHoldsKeepsItsSlot(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	base := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(base))
	seedPolicyInbound(t, "vless", "vless-cluster", 45602, "shared")
	setLimitIP(t, "shared", 1)

	now := time.Now()
	// The node's report: seen seconds ago, holding its slot these ten minutes.
	entries, err := json.Marshal([]model.ClientIpEntry{{
		IP: "203.0.113.7", Timestamp: now.Add(-5 * time.Second).Unix(), FirstSeen: now.Add(-10 * time.Minute).Unix(),
	}})
	if err != nil {
		t.Fatalf("marshal entries: %v", err)
	}
	attribution := &model.NodeClientIp{NodeGuid: "node-elsewhere", Email: "shared", Ips: string(entries)}
	if err := database.GetDB().Create(attribution).Error; err != nil {
		t.Fatalf("seed node attribution: %v", err)
	}
	// And the union row the sync wrote from it.
	union := &model.InboundClientIps{ClientEmail: "shared", Ips: string(entries)}
	if err := database.GetDB().Create(union).Error; err != nil {
		t.Fatalf("seed union row: %v", err)
	}
	base.sessions = observe("shared", now, "198.51.100.9")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if got, want := overAddresses(verdicts, "shared"), []string{"198.51.100.9"}; !slices.Equal(got, want) {
		t.Fatalf("over %v, want the local arrival %v: the node's device came first and holds the slot", got, want)
	}
}
