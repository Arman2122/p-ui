package sub

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The Clash proxy group's order is shuffled on every fetch, when asked.

Order is what a mihomo group acts on: a url-test or fallback group takes the
first entry that works, so a fixed order sends every client to whichever host
happens to sort first, and the rest of the fleet carries nothing.

The panel used to carry a per-HOST "shuffle host" boolean for this, which no
renderer read and never could have: shuffling is a property of the list, not of
one member of it. This is the same capability mihomo documents as
shuffle-proxies-order, put where order actually lives. F141.
*/
func groupOrder(t *testing.T, yaml string) []string {
	t.Helper()
	_, after, ok := strings.Cut(yaml, "proxy-groups:")
	if !ok {
		t.Fatalf("no proxy-groups in the rendered config:\n%s", yaml)
	}
	_, list, ok := strings.Cut(after, "proxies:")
	if !ok {
		t.Fatalf("the group carries no proxies list:\n%s", yaml)
	}
	var names []string
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			if len(names) > 0 {
				break
			}
			continue
		}
		names = append(names, strings.Trim(strings.TrimPrefix(line, "- "), `"`))
	}
	return names
}

func seedShuffleFleet(t *testing.T) {
	t.Helper()
	seedSubDB(t)
	ib := seedSubInbound(t, "sh1", "shuffle-in", 45611, 0, `{"network":"tcp","security":"none"}`)
	for i, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		seedHost(t, &model.Host{
			InboundId: ib.Id, SortOrder: i, Remark: name,
			Address: name + ".example.com", Port: 8443,
		})
	}
}

func TestTheClashGroupOrderIsShuffledPerFetch(t *testing.T) {
	seedShuffleFleet(t)
	svc := NewSubClashService(false, "", true, NewSubService(""))

	first, _, err := svc.GetClash("sh1", "req.example.com")
	if err != nil {
		t.Fatalf("GetClash: %v", err)
	}
	baseline := groupOrder(t, first)
	if len(baseline) < 8 {
		t.Fatalf("expected the whole fleet in the group, got %v", baseline)
	}
	if baseline[len(baseline)-1] != "DIRECT" {
		t.Fatalf("DIRECT must stay last, where an operator looks for the escape hatch; got %v", baseline)
	}

	// Eight entries: the chance of twenty independent shuffles all matching is
	// 1/40320^20, so a run of identical orders means nothing is shuffling.
	moved := false
	for range 20 {
		next, _, err := svc.GetClash("sh1", "req.example.com")
		if err != nil {
			t.Fatalf("GetClash: %v", err)
		}
		order := groupOrder(t, next)
		if len(order) != len(baseline) {
			t.Fatalf("the fleet changed size between fetches: %v vs %v", order, baseline)
		}
		if order[len(order)-1] != "DIRECT" {
			t.Fatalf("DIRECT moved out of last place: %v", order)
		}
		if !slicesEqual(order, baseline) {
			moved = true
		}
	}
	if !moved {
		t.Fatalf("twenty fetches all returned the same order (%v); every client lands on whichever host sorts first", baseline)
	}
}

// And the default is off: an operator who never asked keeps a stable profile,
// because a client diffing its saved config would otherwise see churn.
func TestTheClashGroupOrderIsStableWhenNotAsked(t *testing.T) {
	seedShuffleFleet(t)
	svc := NewSubClashService(false, "", false, NewSubService(""))

	first, _, err := svc.GetClash("sh1", "req.example.com")
	if err != nil {
		t.Fatalf("GetClash: %v", err)
	}
	baseline := groupOrder(t, first)
	for range 10 {
		next, _, err := svc.GetClash("sh1", "req.example.com")
		if err != nil {
			t.Fatalf("GetClash: %v", err)
		}
		if order := groupOrder(t, next); !slicesEqual(order, baseline) {
			t.Fatalf("the order moved without being asked: %v vs %v", order, baseline)
		}
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
