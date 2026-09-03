package job

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func seedResettableInbound(t *testing.T, port int, tag, email, period string, resetDay int) *model.Inbound {
	t.Helper()
	db := database.GetDB()
	settings, err := json.Marshal(map[string]any{
		"clients": []any{map[string]any{
			"id": fmt.Sprintf("22222222-3333-4444-5555-%012d", port), "email": email,
			"enable": true, "totalGB": 1,
		}},
		"decryption": "none",
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	inbound := &model.Inbound{
		UserId: 1, Enable: true, Tag: tag, Port: port, Protocol: model.VLESS,
		Listen: "0.0.0.0", Settings: string(settings),
		StreamSettings:  `{"network":"tcp","security":"none"}`,
		TrafficReset:    period,
		TrafficResetDay: resetDay,
	}
	if _, _, _, err := (&service.InboundService{}).AddInbound(inbound); err != nil {
		t.Fatalf("AddInbound %s: %v", tag, err)
	}
	if err := db.Model(core.ClientTraffic{}).Where("email = ?", email).
		Updates(map[string]any{"total": 100, "up": 60, "down": 60}).Error; err != nil {
		t.Fatalf("spend the client allowance: %v", err)
	}
	if err := db.Model(model.Inbound{}).Where("id = ?", inbound.Id).
		Updates(map[string]any{"up": 30, "down": 30}).Error; err != nil {
		t.Fatalf("spend the inbound allowance: %v", err)
	}
	return inbound
}

func trafficOf(t *testing.T, inboundId int, email string) (inboundUp, inboundDown, clientUp, clientDown int64) {
	t.Helper()
	db := database.GetDB()
	var ib model.Inbound
	if err := db.First(&ib, inboundId).Error; err != nil {
		t.Fatalf("reload inbound %d: %v", inboundId, err)
	}
	var ct core.ClientTraffic
	if err := db.Where("email = ?", email).First(&ct).Error; err != nil {
		t.Fatalf("reload client traffic %s: %v", email, err)
	}
	return ib.Up, ib.Down, ct.Up, ct.Down
}

/*
Run was covered only through monthlyResetDue; the query that picks the inbounds
and the two resets it fires were not. One daily inbound is reset with its
clients, a weekly one on the same box is untouched, and a monthly one whose
reset day is not today waits. Feature gap A1.
*/
func TestPeriodicTrafficResetRunResetsOnlyItsPeriod(t *testing.T) {
	initTestDB(t)
	daily := seedResettableInbound(t, 29130, "reset-daily", "daily@x", "daily", 1)
	weekly := seedResettableInbound(t, 29131, "reset-weekly", "weekly@x", "weekly", 1)
	today := time.Now().UTC().Day()
	monthly := seedResettableInbound(t, 29132, "reset-monthly", "monthly@x", "monthly", today%28+1)

	NewPeriodicTrafficResetJob("daily", time.UTC).Run()
	NewPeriodicTrafficResetJob("monthly", time.UTC).Run()

	if iu, id, cu, cd := trafficOf(t, daily.Id, "daily@x"); iu != 0 || id != 0 || cu != 0 || cd != 0 {
		t.Fatalf("daily inbound after the daily run = inbound %d/%d client %d/%d, want everything zeroed", iu, id, cu, cd)
	}
	for _, tt := range []struct {
		name  string
		id    int
		email string
	}{{"weekly", weekly.Id, "weekly@x"}, {"monthly on another day", monthly.Id, "monthly@x"}} {
		if iu, id, cu, cd := trafficOf(t, tt.id, tt.email); iu != 30 || id != 30 || cu != 60 || cd != 60 {
			t.Fatalf("%s inbound = inbound %d/%d client %d/%d, want untouched 30/30 and 60/60", tt.name, iu, id, cu, cd)
		}
	}
}
