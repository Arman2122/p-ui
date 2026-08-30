package sub

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A switched-off client keeps its link, and the page says it has ended.

This campaign first WITHHELD those links: a disabled client's account is gone
from the core, so the link cannot connect, and the panel's file-share path had
always skipped them. The operator's call was the other way, and the reason is
better than the one I had: the body has to keep parsing for every app that
fetches it, and a subscription that suddenly serves nothing reads as a broken
URL rather than as an ended account.

So the truth goes on the PAGE — per entry, next to the link — because the page
is the one surface a person actually looks at. The body is unchanged for every
client whatever its state. F249.
*/
func TestASubscriptionStillServesADisabledClientsLink(t *testing.T) {
	initSubDB(t)

	seedSubInbound(t, "mixedsub", "live", 24851, 1,
		`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	dead := seedSubInbound(t, "mixedsub", "dead", 24852, 2,
		`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	disableSubClient(t, dead, "dead@e")

	links, emails, _, _, err := NewSubService("").GetSubs("mixedsub", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}

	if len(links) != 2 {
		t.Fatalf("links = %d, want 2: a subscription that stops serving an entry reads as a broken URL, not as an ended account", len(links))
	}
	var sawDead bool
	for _, email := range emails {
		if email == "dead@e" {
			sawDead = true
		}
	}
	if !sawDead {
		t.Fatalf("emails = %v, want the disabled client among them", emails)
	}
}

// And the page marks it, which is the whole of what changed.
func TestTheSubscriptionPageMarksAnEndedEntry(t *testing.T) {
	initSubDB(t)

	seedSubInbound(t, "pagesub", "page-live", 24871, 1,
		`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	dead := seedSubInbound(t, "pagesub", "page-dead", 24872, 2,
		`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	disableSubClient(t, dead, "page-dead@e")

	svc := NewSubService("req.example.com")
	links, emails, lastOnline, traffic, err := svc.GetSubs("pagesub", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	page := svc.BuildPageData("pagesub", "req.example.com", traffic, lastOnline,
		links, emails, "", "", "", "/", "", "")

	if len(page.Ended) != len(page.Result) {
		t.Fatalf("Ended has %d entries for %d links; the page reads them by index", len(page.Ended), len(page.Result))
	}
	var markedDead, markedLive int
	for i, email := range page.Emails {
		switch {
		case email == "page-dead@e" && page.Ended[i]:
			markedDead++
		case email == "page-live@e" && !page.Ended[i]:
			markedLive++
		}
	}
	if markedDead == 0 {
		t.Fatalf("the disabled client's entry is not marked ended: emails=%v ended=%v", page.Emails, page.Ended)
	}
	if markedLive == 0 {
		t.Fatalf("the working client's entry was marked ended: emails=%v ended=%v", page.Emails, page.Ended)
	}
}

// The traffic totals count it either way — dropping the email would take the
// client out of Subscription-Userinfo as well, which is the trade F311 warned
// about.
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
		t.Fatalf("aggregate up = %d, want %d: the disabled client was dropped from the totals", traffic.Up, deadUpBytes)
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
