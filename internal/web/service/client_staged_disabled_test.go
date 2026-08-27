package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A client created with its switch off must be stored off.

The campaign already closed this once, on the mechanism where GORM dropped a
zero-valued boolean from the INSERT and the column's `default:true` supplied one
instead. That fix was real, and the CHANGELOG says the guarantee holds. It did
not, because a second mechanism sat above it: three creation paths carried an
explicit

	if !client.Enable {
		client.Enable = true
	}

which overrode the caller before the database ever saw it. The arch guard stayed
green because it drives AddInboundClient, not the endpoint the client form uses.

Nothing depended on the forcing. Every caller already sets Enable: the LDAP job
in buildClient, the Telegram bot from its own draft state, the import path from
the exported record. What the forcing did was override three deliberate ones:

  - the panel's own enable switch on /panel/api/clients/add;
  - an import of a client that was disabled when it was exported, which came
    back enabled;
  - the bot's "add_client_submit_disable" button, which sets Enable=false and
    got an enabled client anyway.

F046 / F170.
*/

func TestAClientStagedDisabledIsStoredDisabled(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}

	ib := mkInbound(t, 23901, model.VLESS, clientsSettings(t, []model.Client{}))

	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{
		Client: model.Client{
			Email: "staged-off@x", ID: "aaaaaaaa-1111-4222-8333-444444444444",
			SubID: "sub-staged-off", Enable: false,
		},
		InboundIds: []int{ib.Id},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if rec := lookupClientRecord(t, "staged-off@x"); rec.Enable {
		t.Fatal("a client created with its switch off was stored enabled, so it is not stripped from the push to the core and can connect")
	}
}

// The bulk endpoint is the same promise, and it is also the import path.
func TestClientsStagedDisabledSurviveABulkCreate(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}

	ib := mkInbound(t, 23902, model.VLESS, clientsSettings(t, []model.Client{}))

	payloads := []ClientCreatePayload{
		{
			Client: model.Client{
				Email: "bulk-off@x", ID: "aaaaaaaa-1111-4222-8333-555555555555",
				SubID: "sub-bulk-off", Enable: false,
			},
			InboundIds: []int{ib.Id},
		},
		{
			Client: model.Client{
				Email: "bulk-on@x", ID: "aaaaaaaa-1111-4222-8333-666666666666",
				SubID: "sub-bulk-on", Enable: true,
			},
			InboundIds: []int{ib.Id},
		},
	}
	if _, _, err := svc.BulkCreate(inboundSvc, payloads); err != nil {
		t.Fatalf("BulkCreate: %v", err)
	}

	if rec := lookupClientRecord(t, "bulk-off@x"); rec.Enable {
		t.Error("a client staged disabled came back enabled from BulkCreate; this is also the import path, so a client exported while disabled returns enabled")
	}
	// The other half: the switch is honoured, not inverted.
	if rec := lookupClientRecord(t, "bulk-on@x"); !rec.Enable {
		t.Error("a client staged ENABLED came back disabled; removing the forcing must not flip the default")
	}
}

/*
And no creation path may reintroduce it.

A source guard rather than a behavioural one, because the property is "none of
these three functions overrides the caller" and a test per path would still miss
the fourth someone adds. The forcing was identical in all three, which is what
made it survive a closure written against one of them.
*/
func TestNoCreationPathForcesAClientEnabled(t *testing.T) {
	for _, name := range []string{"client_crud.go", "client_bulk.go", "client_portable.go"} {
		body := readServiceSource(t, name)
		// Normalised so indentation cannot hide it.
		flat := strings.Join(strings.Fields(body), " ")
		if strings.Contains(flat, "if !client.Enable { client.Enable = true }") {
			t.Errorf("%s forces a client enabled again; a client staged with its switch off is stored on, and the arch guard will not see it because it drives AddInboundClient", name)
		}
	}
}
