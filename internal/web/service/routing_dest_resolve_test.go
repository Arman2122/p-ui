package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func destRule(kind, tag string) *model.RoutingRule {
	return &model.RoutingRule{
		Enable: true, IngressScope: model.RoutingScopeAll, IngressIds: "[]",
		DestKind: kind, DestTag: tag, Criteria: "{}",
	}
}

/*
A destination tag was validated for shape and never for existence.

Xray does not reject an unknown outboundTag — it falls back to the first
outbound — so a rule pointing at an outbound somebody deleted or renamed keeps
matching and sends that traffic somewhere the operator never chose. Nothing
downstream catches it either: the compile emits the tag verbatim, and the
ongoing dead-target sweep covers egress rows only.

Driven at destResolves rather than through Add: convergence needs a Linux host,
and the refusal has to hold on every platform the panel is developed on.
*/
func TestARuleMustNameADestinationThatExists(t *testing.T) {
	initTestDB(t)

	t.Run("a ghost outbound tag is refused, and the refusal names it", func(t *testing.T) {
		err := destResolves(destRule(model.RoutingDestOutbound, "deleted-vps"))
		if err == nil {
			t.Fatal("a rule aimed at an outbound that does not exist was accepted")
		}
		if !strings.Contains(err.Error(), "deleted-vps") {
			t.Fatalf("the refusal must name the tag, got %v", err)
		}
	})

	t.Run("a ghost balancer tag is refused too", func(t *testing.T) {
		if err := destResolves(destRule(model.RoutingDestBalancer, "no-such-balancer")); err == nil {
			t.Fatal("a rule aimed at a balancer that does not exist was accepted")
		}
	})

	/* direct and block carry no tag, so the check must not reach for one. */
	for _, kind := range []string{model.RoutingDestDirect, model.RoutingDestBlock, model.RoutingDestExit} {
		t.Run(kind+" names no outbound tag and is never refused here", func(t *testing.T) {
			if err := destResolves(destRule(kind, "")); err != nil {
				t.Fatalf("destResolves(%s) = %v, want nil", kind, err)
			}
		})
	}

	t.Run("the gate is wired into the save path, not merely defined", func(t *testing.T) {
		_, err := (&RoutingService{}).Add(t.Context(), destRule(model.RoutingDestOutbound, "deleted-vps"))
		if err == nil {
			t.Fatal("Add accepted a rule aimed at an outbound that does not exist")
		}
		if !strings.Contains(err.Error(), "deleted-vps") {
			t.Fatalf("Add must refuse with the tag named, got %v", err)
		}
	})
}
