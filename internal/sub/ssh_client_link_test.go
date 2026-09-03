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
