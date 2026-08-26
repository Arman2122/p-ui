package service

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
)

/*
The masquerade exemption is only as good as the list handed to it.

EnsureHostForwarding builds the device list from the inbounds and passes it to
EnsureMasquerade; the front list has to come from the egress rows, because a
front IS a panel-owned row and nothing else names those devices. An operator's
own uplink must stay out of it: that is a destination the panel dials, not a
front it re-originates through, and exempting it would leave real forwarded
traffic untranslated. F066.
*/
func TestPanelFrontDevicesNamesTheFrontsAndNothingElse(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	// The row carries a foreign key to its ingress, so the inbound has to exist.
	ingress := durableTestInbound(nil, "front-ingress", 24951)
	if err := db.Create(ingress).Error; err != nil {
		t.Fatalf("seed ingress: %v", err)
	}
	front := &model.Egress{
		Type: "xray-tun", Enable: true, Owner: model.EgressOwnerPanel,
		Remark: "front", IngressInboundId: &ingress.Id,
	}
	if err := db.Create(front).Error; err != nil {
		t.Fatalf("seed front: %v", err)
	}
	uplink := &model.Egress{
		Type: "wg-client", Enable: true, Owner: model.EgressOwnerOperator, Remark: "uplink",
	}
	if err := db.Create(uplink).Error; err != nil {
		t.Fatalf("seed uplink: %v", err)
	}

	devices, err := panelFrontDevices()
	if err != nil {
		t.Fatalf("panelFrontDevices: %v", err)
	}

	if !slices.Contains(devices, egress.Device(front.Id)) {
		t.Fatalf("the front %s is missing, so its traffic is still translated and every source criterion on that inbound matches nothing: %v",
			egress.Device(front.Id), devices)
	}
	if slices.Contains(devices, egress.Device(uplink.Id)) {
		t.Fatalf("an operator's own uplink %s was exempted; forwarded traffic leaving through it would stop being translated: %v",
			egress.Device(uplink.Id), devices)
	}
}

/*
And the list actually reaches the ruleset.

Asserted on the source, because the alternative is calling EnsureHostForwarding
in a test — which shells out to nft and would write a real table on whatever box
the suite runs on. The property is "the fronts argument is the one this function
just computed", and the call site is where that is either true or not.
*/
func TestTheFrontListReachesTheMasqueradeCall(t *testing.T) {
	body, err := os.ReadFile("routing.go")
	if err != nil {
		t.Fatalf("read routing.go: %v", err)
	}
	source := string(body)

	start := strings.Index(source, "func (s *RoutingService) EnsureHostForwarding(")
	if start < 0 {
		t.Fatal("EnsureHostForwarding was renamed; this guard is now vacuous")
	}
	end := strings.Index(source[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of EnsureHostForwarding; this guard is now vacuous")
	}
	body2 := source[start : start+end]

	if !strings.Contains(body2, "panelFrontDevices()") {
		t.Fatal("EnsureHostForwarding never asks which devices are fronts")
	}
	if !strings.Contains(body2, "egress.EnsureMasquerade(ctx, devices, fronts)") {
		t.Fatal("the front list is computed and not passed: every front is translated again, and a source criterion on an L3 inbound matches nothing")
	}
}
