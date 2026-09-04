package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An OpenConnect inbound is a routing subject, selected by the pool it hands
addresses from.

It was refused until now for a true reason -- ocserv makes one tun per session,
so no interface name outlives a connection -- and the pool is the handle that
does. This asserts the answer the editor renders, and the one it renders when
the pool cannot be read, because "routable" and "blocked, and here is why" are
the only two honest answers and an empty row is neither.
*/
func TestAnOpenConnectInboundIsRoutableByItsPool(t *testing.T) {
	initTestDB(t)

	seedRoutingInbound(t, "ocs-pool", 4443, model.OCServ, `{"subnet":"10.218.0.0/24"}`, true, nil)
	seedRoutingInbound(t, "ocs-broken", 4444, model.OCServ, `{"subnet":"not-a-prefix"}`, true, nil)

	subjects, err := (&InboundService{}).GetRoutingSubjects()
	if err != nil {
		t.Fatalf("GetRoutingSubjects: %v", err)
	}
	got := map[string]RoutingSubject{}
	for _, s := range subjects {
		got[s.Tag] = s
	}
	if subject := got["ocs-pool"]; !subject.Routable || subject.ReasonKey != "" {
		t.Fatalf("an OpenConnect inbound with a readable pool is %+v, want routable", subject)
	}
	if subject := got["ocs-broken"]; subject.Routable || subject.ReasonKey != "pages.xray.subjects.reasonNoPool" {
		t.Fatalf("an OpenConnect inbound whose pool cannot be read is %+v, want blocked with a reason", subject)
	}
}

/*
And the pool reaches the kernel plan, which is the half a subject view cannot
show: the front the routing compile provisions carries the prefix as its source
selector, so the rule the manager installs is `from <pool>`.

Without this the front exists, the editor calls the inbound routable, and no
packet is ever selected into its table.
*/
func TestTheFrontOfAPoolIngressCarriesThePrefix(t *testing.T) {
	initTestDB(t)
	inbound := seedRoutingInbound(t, "ocs-front", 4445, model.OCServ, `{"subnet":"10.218.0.0/24"}`, true, nil)

	front := &model.Egress{
		Type: "xray-tun", Enable: true, Owner: model.EgressOwnerPanel,
		Remark: inbound.Tag, IngressInboundId: &inbound.Id, Target: "direct",
	}
	if err := database.GetDB().Create(front).Error; err != nil {
		t.Fatalf("seed front: %v", err)
	}

	rows, err := (&EgressService{}).GetAll()
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	desired, err := (&EgressService{}).desired(context.Background(), rows)
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	for _, row := range desired {
		if row.ID != front.Id {
			continue
		}
		if len(row.Sources) != 1 || row.Sources[0].String() != "10.218.0.0/24" {
			t.Fatalf("the front's sources are %v, want the inbound's pool", row.Sources)
		}
		if len(row.Ingress) != 0 {
			t.Fatalf("the front also claims devices %v, and this inbound has none that outlive a session", row.Ingress)
		}
		return
	}
	t.Fatalf("no plan for the front row %d: %+v", front.Id, desired)
}

/*
Two pools that overlap are refused at save.

Untidy while nothing selected on them, a hijack now: each attached inbound
installs its own `from <pool>` rule at its own priority, and over the addresses
both cover the lower-priority rule never sees a packet. One inbound's clients
would leave through the other's exit, for part of the range and silently.
*/
func TestAnOverlappingAddressPoolIsRefused(t *testing.T) {
	initTestDB(t)
	seedRoutingInbound(t, "ocs-first", 4446, model.OCServ, `{"subnet":"10.218.0.0/24"}`, true, nil)

	clash := &model.Inbound{
		UserId: 1, Remark: "ocs-second", Tag: "ocs-second", Port: 4447,
		Protocol: model.OCServ, Settings: `{"subnet":"10.218.0.0/16"}`, Enable: true,
	}
	err := (&InboundService{}).checkIngressPoolConflict(context.Background(), clash, 0)
	if err == nil {
		t.Fatal("a pool covering another inbound's pool was accepted; its clients would leave through the wrong exit")
	}
	if !strings.Contains(err.Error(), "10.218.0.0/24") || !strings.Contains(err.Error(), "ocs-first") {
		t.Fatalf("the refusal must name the pool and the inbound holding it: %v", err)
	}

	// A pool of its own is fine, and so is editing the inbound that holds one.
	apart := *clash
	apart.Settings = `{"subnet":"10.219.0.0/24"}`
	if err := (&InboundService{}).checkIngressPoolConflict(context.Background(), &apart, 0); err != nil {
		t.Fatalf("a pool overlapping nothing was refused: %v", err)
	}
}
