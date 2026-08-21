package database

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The fast path has to mark every seeder a fresh install must not run, and
ResetIpLimitNoFail2ban is the instance with the sharpest history: it used to
zero every client's IP limit, so the mark was the only thing standing between a
new install and a destructive migration meant for upgrades.

The seeder is retired and destroys nothing now, so that is no longer the reason
— but the rule is unchanged. A fresh database has nothing an upgrade migration
could be repairing, and a name missing from this list is one that gets run
against it.
*/
func TestFreshInstallFastPathMarksResetIpLimitSeeder(t *testing.T) {
	initTestDB(t)

	if err := db.Where("1 = 1").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
		t.Fatalf("reset seeder history: %v", err)
	}
	if err := db.Where("1 = 1").Delete(&model.User{}).Error; err != nil {
		t.Fatalf("reset users: %v", err)
	}

	if err := runSeeders(true); err != nil {
		t.Fatalf("runSeeders: %v", err)
	}

	var cnt int64
	if err := db.Model(&model.HistoryOfSeeders{}).
		Where("seeder_name = ?", "ResetIpLimitNoFail2ban").
		Count(&cnt).Error; err != nil {
		t.Fatalf("count seeder history: %v", err)
	}
	if cnt != 1 {
		t.Fatal("fresh-install fast path must mark ResetIpLimitNoFail2ban done, or a fresh install runs an upgrade migration against a database that never needed it")
	}
}

/*
The upgrade path, which is where this seeder did its damage.

It zeroed limitIp in the inbound settings JSON and limit_ip on the clients table
whenever fail2ban could not enforce at boot — and it could not, on any upgrade,
because install.sh and update.sh both start the panel before setup_fail2ban
runs. Silent, one-way, and recorded so it never retried: an operator's per-client
IP limits were simply gone.

PUI_ENABLE_FAIL2BAN=false reproduces the trigger exactly, and is the sharper of
the two real ones — the old probe returned false on that alone, fail2ban
installed or not. Without it this test would pass on any box that has
fail2ban and prove nothing.
*/
func TestUpgradeSeederKeepsConfiguredIpLimits(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_ENABLE_FAIL2BAN", "false")

	if err := db.Where("1 = 1").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
		t.Fatalf("reset seeder history: %v", err)
	}

	settings, err := json.Marshal(map[string]any{
		"clients": []any{map[string]any{
			"id": "11111111-2222-3333-4444-555555555555", "email": "capped@example.com", "limitIp": 3,
		}},
		"decryption": "none",
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	inbound := &model.Inbound{
		UserId: 1, Enable: true, Tag: "in-capped", Port: 28001, Protocol: model.VLESS,
		Listen: "0.0.0.0", Settings: string(settings),
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	if err := db.Create(&model.ClientRecord{
		Email: "capped@example.com", Enable: true, LimitIP: 3,
	}).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}

	if err := resetIpLimitsWithoutFail2ban(); err != nil {
		t.Fatalf("resetIpLimitsWithoutFail2ban: %v", err)
	}

	var storedLimit int
	if err := db.Model(&model.ClientRecord{}).
		Where("email = ?", "capped@example.com").Pluck("limit_ip", &storedLimit).Error; err != nil {
		t.Fatalf("read clients.limit_ip: %v", err)
	}
	if storedLimit != 3 {
		t.Fatalf("clients.limit_ip = %d after the upgrade seeder, want 3: an operator's configured cap was destroyed", storedLimit)
	}

	var reloaded model.Inbound
	if err := db.First(&reloaded, inbound.Id).Error; err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(reloaded.Settings), &parsed); err != nil {
		t.Fatalf("stored settings: %v", err)
	}
	clients, _ := parsed["clients"].([]any)
	if len(clients) != 1 {
		t.Fatalf("settings.clients = %v, want one entry", clients)
	}
	entry, _ := clients[0].(map[string]any)
	if limit, _ := entry["limitIp"].(float64); limit != 3 {
		t.Fatalf("settings limitIp = %v after the upgrade seeder, want 3", entry["limitIp"])
	}

	var cnt int64
	if err := db.Model(&model.HistoryOfSeeders{}).
		Where("seeder_name = ?", "ResetIpLimitNoFail2ban").Count(&cnt).Error; err != nil {
		t.Fatalf("count seeder history: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("the retired seeder recorded itself %d time(s), want 1; it has to spend its slot or it runs again next boot", cnt)
	}
}
