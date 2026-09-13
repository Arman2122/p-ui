package sub

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A TUIC or AnyTLS client had no link anywhere: the panel hides links for those
kinds, and the subscription rendered a share only for a kind that serves a file.
The URI reaches the list now, named once, after the remark the operator set.
*/
func TestASingboxClientsSubscriptionCarriesItsLinkOnce(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()
	const uuid, password = "c0ffee00-0000-4000-8000-000000000001", "secret-password-1"
	settings := fmt.Sprintf(`{"certFile":"/etc/p-ui/tuic.crt","keyFile":"/etc/p-ui/tuic.key","serverName":"tuic.example.com","clients":[{"email":"tu@e","uuid":%q,"password":%q,"subId":"tuicsub","enable":true}]}`, uuid, password)
	ib := &model.Inbound{UserId: 1, Tag: "tuic-sub", Enable: true, Listen: "203.0.113.5", Port: 24443, Protocol: model.Protocol("tuic"), Remark: "Berlin", Settings: settings, StreamSettings: "{}"}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	client := &model.ClientRecord{Email: "tu@e", SubID: "tuicsub", UUID: uuid, Password: password, Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: client.Id, InboundId: ib.Id}).Error; err != nil {
		t.Fatalf("seed link: %v", err)
	}
	links, emails, _, _, err := NewSubService("").GetSubs("tuicsub", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	if len(links) != 1 || !strings.HasPrefix(links[0], "tuic://"+uuid+":"+password+"@203.0.113.5:24443?") {
		t.Fatalf("links = %v (emails %v); the client's tuic:// link is missing", links, emails)
	}
	if strings.Count(links[0], "#") != 1 || !strings.Contains(links[0], "#Berlin") {
		t.Fatalf("link = %q; want exactly one fragment carrying the remark", links[0])
	}
}
