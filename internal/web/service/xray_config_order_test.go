package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The generated inbound array must have a defined order.

GetAllInbounds ran a bare Find with no ORDER BY, so the array followed
PostgreSQL's heap order. Updating any unrelated row can move it in the heap, and
the same set of inbounds then renders in a different sequence.

GetInbounds two hundred lines up already orders by id; this one did not, and the
difference had nothing behind it.

Not sold as an outage fix, because it is not one: ComputeHotDiff keys inbounds
by tag, so a pure reorder never caused a restart. What it costs is a config
export that will not diff against yesterday's, and a wasted `xray -test` fork on
every operator-triggered reconcile because configUnchanged keeps coming back
false. F230.
*/
func TestTheGeneratedInboundOrderSurvivesAnUnrelatedUpdate(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	for i, tag := range []string{"order-a", "order-b", "order-c"} {
		inbound := durableTestInbound(nil, tag, 24601+i)
		if err := db.Create(inbound).Error; err != nil {
			t.Fatalf("seed %s: %v", tag, err)
		}
	}

	before := renderedTags(t)
	if len(before) < 3 {
		t.Fatalf("want at least the three seeded inbounds, got %v", before)
	}

	// Rewrite the middle row. In PostgreSQL an UPDATE writes a new tuple, which
	// is what moves it in the heap and reshuffles an unordered read.
	var middle model.Inbound
	if err := db.Where("tag = ?", "order-b").First(&middle).Error; err != nil {
		t.Fatalf("load order-b: %v", err)
	}
	middle.Remark = "touched"
	if err := db.Save(&middle).Error; err != nil {
		t.Fatalf("save order-b: %v", err)
	}

	after := renderedTags(t)
	if len(before) != len(after) {
		t.Fatalf("the inbound count changed: %v -> %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("the generated inbound order moved after an unrelated update:\n before %v\n after  %v", before, after)
		}
	}
}

func renderedTags(t *testing.T) []string {
	t.Helper()
	cfg, err := (&XrayService{}).GetXrayConfig(false)
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}
	tags := make([]string, 0, len(cfg.InboundConfigs))
	for i := range cfg.InboundConfigs {
		tags = append(tags, cfg.InboundConfigs[i].Tag)
	}
	return tags
}
