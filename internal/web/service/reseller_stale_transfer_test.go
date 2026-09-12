package service

import (
	"context"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestResellerTransferUsesFreshBaselineAndRetainsCursors(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	for _, tc := range []struct {
		name        string
		freshBefore int64
		freshAfter  int64
	}{
		{"local-baseline", 0, 110},
		{"fresh-master-baseline", 150, 160},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := model.User{Username: "stale-transfer-" + tc.name, Role: "reseller"}
			if err := db.Create(&owner).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.ResellerAccount{UserID: owner.Id, TrafficLimit: 5}).Error; err != nil {
				t.Fatal(err)
			}
			client := model.ClientRecord{Email: owner.Username, Enable: true, TotalGB: 2000}
			if err := db.Create(&client).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&core.ClientTraffic{Email: client.Email, Enable: true, Up: 100, Total: 2000}).Error; err != nil {
				t.Fatal(err)
			}
			stale := model.ClientGlobalTraffic{Email: client.Email, MasterGuid: "old-master", Up: 1000, UpdatedAt: time.Now().Add(-25 * time.Hour).UnixMilli()}
			if err := db.Create(&stale).Error; err != nil {
				t.Fatal(err)
			}
			if tc.freshBefore > 0 {
				if err := db.Create(&model.ClientGlobalTraffic{Email: client.Email, MasterGuid: "existing-master", Up: tc.freshBefore, UpdatedAt: time.Now().UnixMilli()}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Model(&client).Update("owner_user_id", owner.Id).Error; err != nil {
				t.Fatal(err)
			}
			fresh := model.ClientGlobalTraffic{Email: client.Email, MasterGuid: "new-master", Up: tc.freshAfter, UpdatedAt: time.Now().UnixMilli()}
			if err := db.Create(&fresh).Error; err != nil {
				t.Fatal(err)
			}
			used, args := UsedBytesExpr(db)
			var canonical int64
			if err := db.Table("client_traffics").Select(used, args...).Where("email = ?", client.Email).Scan(&canonical).Error; err != nil {
				t.Fatal(err)
			}
			if canonical != tc.freshAfter {
				t.Fatalf("canonical usage=%d, want %d", canonical, tc.freshAfter)
			}
			if err := (&ResellerService{}).EnforceBudgets(context.Background()); err != nil {
				t.Fatal(err)
			}
			var account model.ResellerAccount
			if err := db.First(&account, "user_id = ?", owner.Id).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&client, client.Id).Error; err != nil {
				t.Fatal(err)
			}
			if account.TrafficUsed != 10 || !account.BudgetBlocked || client.Enable {
				t.Fatalf("fresh usage charged=%d blocked=%v client enabled=%v, want 10/true/false", account.TrafficUsed, account.BudgetBlocked, client.Enable)
			}

			var cursor model.ResellerUsageSource
			if err := db.Where("client_id = ? AND source = ?", client.Id, "master:old-master").First(&cursor).Error; err != nil {
				t.Fatal(err)
			}
			if cursor.Up != 1000 || cursor.Down != 0 || cursor.TotalBytes != 0 {
				t.Fatalf("stale source replay cursor=%+v, want baseline 1000/0 without transferred spend", cursor)
			}
			// Returning or recreating a stale source must not bill its pre-transfer counters.
			if err := db.Model(&stale).Updates(map[string]any{"up": 1000, "down": 0, "updated_at": time.Now().UnixMilli()}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Delete(&stale).Error; err != nil {
				t.Fatal(err)
			}
			stale.Id, stale.UpdatedAt = 0, time.Now().UnixMilli()
			if err := db.Create(&stale).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&account, "user_id = ?", owner.Id).Error; err != nil {
				t.Fatal(err)
			}
			if account.TrafficUsed != 10 {
				t.Fatalf("returning stale source rebilled historical counters: %d, want 10", account.TrafficUsed)
			}
			if err := db.Model(&stale).Update("up", 1020).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&account, "user_id = ?", owner.Id).Error; err != nil {
				t.Fatal(err)
			}
			if account.TrafficUsed != 20 {
				t.Fatalf("returning source fresh growth charged=%d, want greatest post-transfer total 20", account.TrafficUsed)
			}
		})
	}
}
