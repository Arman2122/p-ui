package service

import (
	"fmt"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A quota reset re-pushes limits to EVERY local MTProto inbound carrying the email.

The reset used to look up one inbound -- the first whose secrets named the
client -- and re-apply only that one, so a client sold on two MTProto inbounds
kept the stale budget on the second sidecar until the next reconcile. The
lookup now answers with all of them. F218.
*/
func TestMtprotoInboundIdsForEmailCoverEveryInboundCarryingIt(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	const email = "shared@example.com"
	const secret = "ee1234567890abcdef1234567890abcd7777772e636c6f7564666c6172652e636f6d"
	var want []int
	for i, port := range []int{47101, 47102} {
		ib := &model.Inbound{
			Remark: fmt.Sprintf("f218-%d", i), Tag: fmt.Sprintf("mtproto-%d", port), Protocol: model.MTProto, Port: port, Enable: true,
			Settings:       fmt.Sprintf(`{"clients":[{"email":%q,"subId":"sub%d","enable":true,"secret":%q}]}`, email, i, secret),
			StreamSettings: "{}", Sniffing: "{}",
		}
		if err := db.Create(ib).Error; err != nil {
			t.Fatalf("seed inbound %d: %v", i, err)
		}
		want = append(want, ib.Id)
	}

	got := (&InboundService{}).localMtprotoInboundIdsForEmail(email)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("inbound ids carrying %s = %v, want both %v", email, got, want)
	}
}
