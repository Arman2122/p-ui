package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The legacy "0.0.0.0:<port>-…" tag cleanup must run on a panel with no externalProxy.

MigrationRequirements returned as soon as the externalProxy scan came back empty,
and the tag cleanup sat after that return -- so the one migration step every
upgraded panel needs ran only on the panels that also happened to carry an
externalProxy entry. F385.
*/
func TestMigrationRequirementsCleansLegacyTagsWithoutExternalProxy(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	ib := &model.Inbound{
		Remark: "f385", Tag: "0.0.0.0:47443-legacy", Protocol: model.VLESS, Port: 47443, Enable: true,
		Settings: `{"clients":[]}`, StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: "{}",
	}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	(&InboundService{}).MigrationRequirements()

	var got model.Inbound
	if err := db.First(&got, ib.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Tag != "47443-legacy" {
		t.Fatalf("tag after MigrationRequirements = %q, want the legacy 0.0.0.0: prefix stripped", got.Tag)
	}
}

func TestLegacyTagCleanupKeepsCollidingTags(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	wanted := []string{"occupied", "0.0.0.0:occupied", "0.0.0.0:ambiguous", "0.0.0.0:0.0.0.0:ambiguous", "available"}
	var rows []model.Inbound
	for i, tag := range wanted {
		if tag == "available" {
			tag = "0.0.0.0:available"
		}
		row := model.Inbound{UserId: 1, Tag: tag, Port: 47600 + i, Protocol: model.VLESS, Settings: `{"clients":[]}`, StreamSettings: `{}`}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := (&InboundService{}).MigrationRequirements(); err != nil {
		t.Fatalf("tag collision aborted startup migration: %v", err)
	}
	for i, row := range rows {
		var stored model.Inbound
		if err := db.First(&stored, row.Id).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Tag != wanted[i] {
			t.Fatalf("tag %q became %q, want %q", row.Tag, stored.Tag, wanted[i])
		}
	}
}
