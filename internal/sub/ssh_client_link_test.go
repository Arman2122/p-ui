package sub

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An SSH client's subscription answered 404: the user the subscription hands
the core for rendering carried only the WireGuard credentials, so the SSH
renderer saw no username and no password, produced no link, and a
subscription with no links is "not found" to every app.
*/
func TestAnSSHClientsSubscriptionCarriesItsLink(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()
	settings := fmt.Sprintf(`{"transport":"tcp","clients":[{"username":"usfq7ryi","password":"secret-12","email":%q,"subId":"sshsub","enable":true}]}`, "sshuser@e")
	ib := &model.Inbound{UserId: 1, Tag: "ssh-sub", Enable: true, Listen: "203.0.113.5", Port: 24901, Protocol: model.SSH, Remark: "ssh", Settings: settings, StreamSettings: "{}"}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	client := &model.ClientRecord{Email: "sshuser@e", SubID: "sshsub", Password: "secret-12", Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: client.Id, InboundId: ib.Id}).Error; err != nil {
		t.Fatalf("seed link: %v", err)
	}
	if err := db.Create(&model.ClientCredential{ClientId: client.Id, InboundId: ib.Id, Key: model.CredentialUsername, Value: "usfq7ryi"}).Error; err != nil {
		t.Fatalf("seed username: %v", err)
	}

	links, emails, _, _, err := NewSubService("").GetSubs("sshsub", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	if len(links) != 1 || !strings.HasPrefix(links[0], "ssh://usfq7ryi:secret-12@203.0.113.5:24901") {
		t.Fatalf("links = %v (emails %v); the client's ssh:// link is missing, which the sub server answers with 404", links, emails)
	}
}

// seedSSHClient plants one SSH inbound with one client attached: the settings
// clients array, the record, the join row and the per-inbound username.
func seedSSHClient(t *testing.T, tag, remark string, port int, subID, email, username string) *model.Inbound {
	t.Helper()
	db := database.GetDB()
	settings := fmt.Sprintf(`{"transport":"tcp","clients":[{"username":%q,"password":"secret-12","email":%q,"subId":%q,"enable":true}]}`, username, email, subID)
	ib := &model.Inbound{
		UserId: 1, Tag: tag, Enable: true, Listen: "203.0.113.5", Port: port,
		Protocol: model.SSH, Remark: remark, Settings: settings, StreamSettings: "{}",
	}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	client := &model.ClientRecord{Email: email, SubID: subID, Password: "secret-12", Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: client.Id, InboundId: ib.Id}).Error; err != nil {
		t.Fatalf("seed join: %v", err)
	}
	if err := db.Create(&model.ClientCredential{ClientId: client.Id, InboundId: ib.Id, Key: model.CredentialUsername, Value: username}).Error; err != nil {
		t.Fatalf("seed username: %v", err)
	}
	return ib
}

/*
The name a client app shows for an SSH account is the client's own, composed
the way every other protocol's is.

The core stamped the inbound's tag into the URI fragment, so every account in
a subscription appeared under a name like "in-24902-tcp": the same name for
every client of that inbound, and one nobody recognises.
*/
func TestAnSSHLinkIsNamedAfterTheClientNotTheInboundTag(t *testing.T) {
	initSubDB(t)
	ib := seedSSHClient(t, "in-24902-tcp", "Tokyo", 24902, "sshname", "named@e", "usfq7ryj")

	links, _, _, _, err := NewSubService("").GetSubs("sshname", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links = %v, want exactly one", links)
	}
	if !strings.HasSuffix(links[0], "#Tokyo-named%40e") {
		t.Fatalf("the link is not named after the client: %s", links[0])
	}
	if strings.Contains(links[0], ib.Tag) {
		t.Fatalf("the link still carries the inbound tag as its name: %s", links[0])
	}
}

/*
An SSH inbound's Host rows are the way in the operator published, and its link
must point there.

Every other generator fans out one link per Host row; this one resolved the
inbound's own address and ignored the rows, so a client behind a CDN was
handed the origin address the Host row exists to replace, from the same page
that lists the hosts.
*/
func TestAnSSHLinkFollowsItsHosts(t *testing.T) {
	initSubDB(t)
	ib := seedSSHClient(t, "ssh-hosted", "Edge", 24903, "sshhost", "hosted@e", "usfq7ryk")
	seedHost(t, &model.Host{InboundId: ib.Id, SortOrder: 1, Remark: "A", Address: "a.cdn.com", Port: 2096})
	seedHost(t, &model.Host{InboundId: ib.Id, SortOrder: 2, Remark: "B", Address: "b.cdn.com", Port: 8443})

	entries, _, _, _, err := NewSubService("").GetSubs("sshhost", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want one per client", entries)
	}
	// One client, one entry, one line per host: the shape every other
	// multi-host generator produces.
	links := strings.Split(entries[0], "\n")
	if len(links) != 2 {
		t.Fatalf("links = %v, want one per host", links)
	}
	for i, want := range []string{"@a.cdn.com:2096", "@b.cdn.com:8443"} {
		if !strings.Contains(links[i], want) {
			t.Fatalf("link %d does not point at its host (%s): %s", i, want, links[i])
		}
	}
	for _, link := range links {
		if strings.Contains(link, "203.0.113.5") || strings.Contains(link, "24903") {
			t.Fatalf("the link leaks the origin the host exists to replace: %s", link)
		}
	}
}
