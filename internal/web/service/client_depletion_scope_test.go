package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/mtproto"
)

func settingsEmails(t *testing.T, settings string) []string {
	t.Helper()
	var parsed struct {
		Clients []struct {
			Email string `json:"email"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	out := make([]string, 0, len(parsed.Clients))
	for _, c := range parsed.Clients {
		out = append(out, c.Email)
	}
	return out
}

/*
A client's client_traffics row is unique per email across every inbound and
core, so its inbound_id names only one of the inbounds the client sits on.
Every depletion filter must therefore cut the client off on the sibling
inbounds too, otherwise the shared quota only ever bites on one of them.
*/
func TestDepletionAppliesToSiblingInbounds(t *testing.T) {
	setupConflictDB(t)
	svc := &InboundService{}

	seedInboundConflict(t, "mt-quota-owner", "", 46201, model.MTProto, "",
		`{"clients":[{"email":"multi","secret":"`+mtprotoTestSecretA+`","enable":true}]}`)
	owner := loadInboundByTag(t, "mt-quota-owner")
	seedInboundConflict(t, "mt-quota-sibling", "", 46202, model.MTProto, "",
		`{"clients":[{"email":"multi","secret":"`+mtprotoTestSecretB+`","enable":true},`+
			`{"email":"solo","secret":"`+mtprotoTestSecretC+`","enable":true}]}`)
	sibling := loadInboundByTag(t, "mt-quota-sibling")

	// The one shared row is depleted and still carries the first inbound's id.
	seedClientTraffic(t, owner.Id, "multi", false)
	seedClientTraffic(t, sibling.Id, "solo", true)

	t.Run("mtprotoReconcileJob", func(t *testing.T) {
		instances, err := svc.DesiredMtprotoInstances()
		if err != nil {
			t.Fatalf("DesiredMtprotoInstances: %v", err)
		}
		if len(instances) != 1 || instances[0].Id != sibling.Id {
			t.Fatalf("want only inbound %d serving, got %+v", sibling.Id, instances)
		}
		want := []mtproto.SecretEntry{{Name: "solo", Secret: mtprotoTestSecretC}}
		if !reflect.DeepEqual(instances[0].Secrets, want) {
			t.Fatalf("sibling secrets: got %+v, want %+v", instances[0].Secrets, want)
		}
	})

	t.Run("interactivePush", func(t *testing.T) {
		built, err := svc.buildInboundForLocalRuntime(database.GetDB(), sibling)
		if err != nil {
			t.Fatalf("buildInboundForLocalRuntime: %v", err)
		}
		want := []string{"solo"}
		if got := settingsEmails(t, built.Settings); !reflect.DeepEqual(got, want) {
			t.Fatalf("pushed clients: got %v, want %v", got, want)
		}
	})

	t.Run("xrayConfigRender", func(t *testing.T) {
		clientSvc := ClientService{}
		seedInboundConflict(t, "vl-quota-owner", "", 46203, model.VLESS, "",
			`{"clients":[],"decryption":"none"}`)
		vlOwner := loadInboundByTag(t, "vl-quota-owner")
		if err := clientSvc.SyncInbound(nil, vlOwner.Id, []model.Client{
			{Email: "vmulti", ID: "44444444-4444-4444-4444-444444444444", Enable: true},
		}); err != nil {
			t.Fatalf("sync owner: %v", err)
		}
		seedInboundConflict(t, "vl-quota-sibling", "", 46204, model.VLESS, "",
			`{"clients":[],"decryption":"none"}`)
		vlSibling := loadInboundByTag(t, "vl-quota-sibling")
		if err := clientSvc.SyncInbound(nil, vlSibling.Id, []model.Client{
			{Email: "vmulti", ID: "44444444-4444-4444-4444-444444444444", Enable: true},
			{Email: "vsolo", ID: "55555555-5555-5555-5555-555555555555", Enable: true},
		}); err != nil {
			t.Fatalf("sync sibling: %v", err)
		}
		seedClientTraffic(t, vlOwner.Id, "vmulti", false)
		seedClientTraffic(t, vlSibling.Id, "vsolo", true)

		cfg, err := (&XrayService{}).RenderInbound(vlSibling)
		if err != nil {
			t.Fatalf("RenderInbound: %v", err)
		}
		want := []string{"vsolo"}
		if got := settingsEmails(t, string(cfg.Settings)); !reflect.DeepEqual(got, want) {
			t.Fatalf("rendered clients: got %v, want %v", got, want)
		}
	})
}
