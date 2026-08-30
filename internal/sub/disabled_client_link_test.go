package sub

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A disabled client's link must not be handed out.

Disabling a client — by hand, by running out of traffic, by expiring — removes
its account from the core, so the link cannot connect. getSubs read
client.Enable only to set a flag on the Subscription-Userinfo header, and
appended the link regardless; the JSON and Clash renderers did not read it at
all. The header flag never reaches the subscriber on a body request either: that
header carries upload, download, total and expire, and nothing else.

So a depleted user's app refreshed, took a profile full of servers that all
looked healthy, and failed on every one with no reason given. FileShare — the
same question, in the same package — has always skipped them.

Counted in the traffic totals either way: dropping the email would under-report a
multi-client subscription, which is how F311 was traded for a different bug.
F249.
*/
func TestASubscriptionWithholdsADisabledClientsLink(t *testing.T) {
	initSubDB(t)

	live := seedSubInbound(t, "mixedsub", "live", 24851, 1,
		`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	dead := seedSubInbound(t, "mixedsub", "dead", 24852, 2,
		`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	_ = live

	// Disable the second client in both places the panel keeps it.
	disableSubClient(t, dead, "dead@e")

	links, emails, _, _, err := NewSubService("").GetSubs("mixedsub", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}

	for i, link := range links {
		if strings.Contains(link, "dead") {
			t.Fatalf("entry %d is a link for a client the core has removed: %q", i, link)
		}
	}
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1 (the enabled client's)", len(links))
	}
	if len(emails) != 1 || emails[0] != "live@e" {
		t.Fatalf("emails = %v, want [live@e]", emails)
	}
}

// And the disabled client still counts toward the traffic header, or a
// multi-client subscription starts under-reporting the moment one is disabled.
func TestADisabledClientStillCountsInTheTrafficTotals(t *testing.T) {
	initSubDB(t)

	seedSubInbound(t, "countsub", "counted-live", 24861, 1,
		`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	dead := seedSubInbound(t, "countsub", "counted-dead", 24862, 2,
		`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	disableSubClient(t, dead, "counted-dead@e")

	const deadUpBytes int64 = 9_876_543
	if err := database.GetDB().Model(&model.ClientRecord{}).
		Where("email = ?", "counted-dead@e").Update("total_gb", 0).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := database.GetDB().Exec(
		`INSERT INTO client_traffics (inbound_id, enable, email, up, down, total, expiry_time)
		 VALUES (?, false, ?, ?, 0, 0, 0)`, dead.Id, "counted-dead@e", deadUpBytes).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	_, _, _, traffic, err := NewSubService("").GetSubs("countsub", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	if traffic.Up != deadUpBytes {
		t.Fatalf("aggregate up = %d, want %d: the disabled client was dropped from the totals as well as the links", traffic.Up, deadUpBytes)
	}
}

func disableSubClient(t *testing.T, inbound *model.Inbound, email string) {
	t.Helper()
	db := database.GetDB()
	settings := strings.Replace(inbound.Settings, `"enable":true`, `"enable":false`, 1)
	if settings == inbound.Settings {
		t.Fatal("the seed no longer carries an enable flag; this test would prove nothing")
	}
	if err := db.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
		Update("settings", settings).Error; err != nil {
		t.Fatalf("disable in settings: %v", err)
	}
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", email).
		Update("enable", false).Error; err != nil {
		t.Fatalf("disable the record: %v", err)
	}
}
