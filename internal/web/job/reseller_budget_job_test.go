package job

import (
	"context"
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestOwnerBudgetPassReturnsDisconnectedClients(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	user := model.User{Username: "budget-cut-owner", Role: "reseller"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResellerAccount{UserID: user.Id, Name: "Cut Co", TrafficLimit: 100, TrafficUsed: 100}).Error; err != nil {
		t.Fatal(err)
	}
	c := model.ClientRecord{Email: "budget-cut-client", OwnerUserID: &user.Id, Enable: true}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&core.ClientTraffic{Email: c.Email, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	if got := enforceProductQuotas(context.Background()); !slices.Equal(got, []string{c.Email}) {
		t.Fatalf("first pass returned %v, want the client it switched off", got)
	}
	if got := enforceProductQuotas(context.Background()); len(got) != 0 {
		t.Fatalf("second pass returned %v for an already paused client", got)
	}
	var events int64
	if err := db.Model(&model.ActivityEvent{}).Where("kind = ? AND audience_user_id = 0", "reseller.depleted").Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("depletion recorded %d times", events)
	}
	NewCorePolicyJob().Run()
	NewXrayTrafficJob().Run()
}
