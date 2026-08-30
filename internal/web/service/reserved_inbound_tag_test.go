package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An inbound may not claim a tag the panel already generates.

Inbound and outbound tags share one namespace in the produced Xray config, and
tagExists only counts rows in `inbounds` — so tags belonging to objects with no
row were free for the taking. The one that bites is `panel-egress`:
injectPanelEgress walks the generated inbounds, finds the tag already present,
logs a Warning and skips injection. The panel's own SOCKS bridge and its routing
rule vanish from the config, and the only trace is that one line. F033.
*/
func TestAnInboundCannotClaimAReservedTag(t *testing.T) {
	initTestDB(t)

	cases := []struct {
		name    string
		tag     string
		refused bool
		owner   string
	}{
		{
			name:    "the panel's own egress bridge",
			tag:     PanelEgressInboundTag,
			refused: true,
			owner:   "egress",
		},
		{name: "the template's api outbound", tag: "api", refused: true, owner: "template"},
		{name: "the template's direct outbound", tag: "direct", refused: true, owner: "template"},
		{name: "the template's blackhole", tag: "blocked", refused: true, owner: "template"},
		/*
			A routing front's Xray inbound is synthesised at compile time, tagged
			with its device name, and appended to the config — it is never a row in
			`inbounds`, so tagExists cannot see it. An inbound tagged peg1 therefore
			saved, and the compile then produced two inbounds with the same tag,
			which xray-core refuses outright: every inbound of every protocol stops
			answering. F231.
		*/
		{name: "a routing front's device name", tag: "peg1", refused: true, owner: "front"},
		{name: "the highest front id", tag: "peg999", refused: true, owner: "front"},
		// Round-tripped, exactly as the egress package reads its own devices: a
		// near miss belongs to somebody else and stays the operator's to use.
		{name: "a near miss with a leading zero", tag: "peg007", refused: false},
		{name: "a front id past the band", tag: "peg1000", refused: false},
		{name: "the bare prefix", tag: "peg", refused: false},
		{name: "an ordinary tag", tag: "edge-tokyo-1", refused: false},
		{name: "a tag that merely contains a reserved word", tag: "my-direct-line", refused: false},
		{name: "empty means generate one", tag: "", refused: false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inbound := &model.Inbound{Tag: c.tag, Port: 26101, Protocol: model.VLESS}
			_, err := (&InboundService{}).resolveInboundTag(inbound, 0)
			if c.refused {
				if err == nil {
					t.Fatalf("the tag %q was accepted; the panel generates it and would be overwritten", c.tag)
				}
				if !strings.Contains(err.Error(), c.owner) {
					t.Fatalf("the refusal must name what owns the tag, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("an operator tag must be accepted: %v", err)
			}
		})
	}
}

/*
And the reservation follows the template rather than a second hardcoded list.

An operator who renames the api outbound must not be left with the stale name
reserved and the live one free — that is the drift reservedAPIPort already avoids
by reading the template, and the same reasoning applies here.
*/
func TestTheReservedSetFollowsTheTemplate(t *testing.T) {
	initTestDB(t)

	custom := `{"inbounds":[{"tag":"management","port":62789}],"outbounds":[{"tag":"straight"},{"tag":"void"}]}`
	setSettingRow(t, "xrayTemplateConfig", custom)

	for _, tag := range []string{"management", "straight", "void"} {
		if reservedTagOwner(tag) == "" {
			t.Fatalf("%q is declared by the template and must be reserved", tag)
		}
	}
	// And a name the template no longer uses is the operator's again.
	if owner := reservedTagOwner("blocked"); owner != "" {
		t.Fatalf("this template does not declare \"blocked\"; reserving it anyway is the drift a second hardcoded list causes (owner=%q)", owner)
	}
	// The panel's own bridge is not in the template and stays reserved regardless.
	if reservedTagOwner(PanelEgressInboundTag) == "" {
		t.Fatal("the panel's egress bridge is injected, not templated, so it must be reserved whatever the template says")
	}
}

// setSettingRow writes one settings key directly. The template has a getter but
// no setter on SettingService; adding one for a test would be new production
// surface for no production caller.
func setSettingRow(t *testing.T, key, value string) {
	t.Helper()
	db := database.GetDB()
	if err := db.Where("key = ?", key).Delete(&model.Setting{}).Error; err != nil {
		t.Fatalf("clear %s: %v", key, err)
	}
	if err := db.Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}
