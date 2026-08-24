package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/routing"
)

/*
A rule the compiler drops must not save as though it were live.

Compiled.Refusals() had no production caller anywhere: the compile ran on every
config build and its Diags were discarded. So a rule the planner marks inert was
stored enabled, listed as active, and then silently not emitted — and for an
exit rule that means every client of that inbound leaves with the server's own
address instead of through the uplink, with nothing in the panel saying so.
*/
func TestARuleTheCompilerWouldDropIsRefusedAtSave(t *testing.T) {
	initTestDB(t)
	installCores(t, newPeerCore(&declaredCore{
		id: "tunnel", kind: "wgkernel", selector: core.SelectorInnerIP, device: "pwg8",
	}))
	inbound := seedPolicyInbound(t, "wgkernel", "wg-l3", 45701, "peer")

	// A kernel WireGuard ingress is matched inside Xray on a front, and the
	// front cannot read a user: the planner marks this pair inert.
	rule := &model.RoutingRule{
		Enable: true, Remark: "cannot match",
		IngressScope: model.RoutingScopeSelected,
		IngressIds:   ingressIdsJSON(t, inbound.Id),
		DestKind:     model.RoutingDestDirect,
		Criteria:     `{"user":["someone@example.com"]}`,
	}

	_, err := (&RoutingService{}).Add(context.Background(), rule)
	if err == nil {
		t.Fatal("stored a rule the compiler drops; the panel would list it as active and route nothing")
	}
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("error %v is not a refusal, so the controller cannot translate it", err)
	}
	if refusal.Key != routing.KeyCriterionUnsupported {
		t.Fatalf("refusal key %q, want %q", refusal.Key, routing.KeyCriterionUnsupported)
	}
	// The operator has to be told which criterion and which inbound.
	if refusal.Args["criterion"] != "user" || refusal.Args["subject"] != "wg-l3" {
		t.Fatalf("args %v name neither the criterion nor the inbound", refusal.Args)
	}

	var stored int64
	if err := database.GetDB().Model(&model.RoutingRule{}).Count(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatalf("%d rule(s) were written despite the refusal", stored)
	}
}

// The refusal happens before anything is provisioned, or a refused save leaves
// a front row behind for a rule that does not exist.
func TestARefusedSaveProvisionsNothing(t *testing.T) {
	initTestDB(t)
	installCores(t, newPeerCore(&declaredCore{
		id: "tunnel", kind: "wgkernel", selector: core.SelectorInnerIP, device: "pwg8",
	}))
	inbound := seedPolicyInbound(t, "wgkernel", "wg-l3b", 45702, "peer")

	_, err := (&RoutingService{}).Add(context.Background(), &model.RoutingRule{
		Enable: true, IngressScope: model.RoutingScopeSelected,
		IngressIds: ingressIdsJSON(t, inbound.Id),
		DestKind:   model.RoutingDestDirect,
		Criteria:   `{"user":["someone@example.com"]}`,
	})
	if err == nil {
		t.Fatal("the rule was accepted")
	}

	var fronts int64
	if err := database.GetDB().Model(&model.Egress{}).
		Where("owner = ?", model.EgressOwnerPanel).Count(&fronts).Error; err != nil {
		t.Fatal(err)
	}
	if fronts != 0 {
		t.Fatalf("%d front row(s) were provisioned for a rule that was refused", fronts)
	}
}

// A rule the compiler emits still saves, or the gate is a wall.
func TestARoutableRuleStillSaves(t *testing.T) {
	initTestDB(t)
	// The save converges the kernel objects on success, which the refusal path
	// never reaches — so only this case needs a kernel to converge against.
	withFakeEgressKernel(t)
	installCores(t, newProxyCore(&declaredCore{id: "proxy", kind: "vless"}))
	inbound := seedPolicyInbound(t, "vless", "vless-ok", 45703, "client")

	created, err := (&RoutingService{}).Add(context.Background(), &model.RoutingRule{
		Enable: true, IngressScope: model.RoutingScopeSelected,
		IngressIds: ingressIdsJSON(t, inbound.Id),
		DestKind:   model.RoutingDestBlock,
	})
	if err != nil {
		t.Fatalf("a routable rule was refused: %v", err)
	}
	if created.Id == 0 {
		t.Fatal("the rule was not stored")
	}
}

// The error carries the key and args rather than a sentence, because the panel
// ships two locales and the controller is where a key becomes text.
func TestARefusalCarriesItsKeyAndArgs(t *testing.T) {
	err := &RefusalError{Key: routing.KeyNoSnat, Args: map[string]string{"exit": "office"}}
	if got := err.Error(); !strings.Contains(got, routing.KeyNoSnat) || !strings.Contains(got, "office") {
		t.Fatalf("Error() = %q, want the key and the arg", got)
	}
}

func ingressIdsJSON(t *testing.T, ids ...int) string {
	t.Helper()
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
