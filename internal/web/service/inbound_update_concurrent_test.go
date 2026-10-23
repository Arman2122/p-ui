package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"

	"gorm.io/gorm"
)

func TestInboundUpdateKeepsConcurrentTrafficAndRenewal(t *testing.T) {
	setupBulkDB(t)
	ib := seedRenewableNeighbour(t, 23203, nil)
	form := *ib
	form.Remark = "edited"
	commitTickBetweenReadAndWrite(t, func(tx *gorm.DB) error {
		if err := autoRenewTick(tx); err != nil {
			return err
		}
		if err := (&InboundService{}).addInboundTraffic(tx, []*xray.Traffic{{IsInbound: true, Tag: ib.Tag, Up: 100, Down: 50}}); err != nil {
			return err
		}
		return tx.Model(&model.Inbound{}).Where("id = ?", ib.Id).Update("enable", false).Error
	}, func() {
		if _, _, _, err := (&InboundService{}).UpdateInbound(&form); err != nil {
			t.Errorf("UpdateInbound: %v", err)
		}
	})
	requireNeighbourRenewed(t, ib.Id)
	saved, err := (&InboundService{}).GetInbound(ib.Id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Up != 100 || saved.Down != 50 || saved.Remark != "edited" || saved.Enable {
		t.Fatalf("inbound after edit: up=%d down=%d remark=%q enable=%v, want 100/50/edited/false", saved.Up, saved.Down, saved.Remark, saved.Enable)
	}
}
