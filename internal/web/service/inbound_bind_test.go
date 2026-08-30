package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A bind nothing can serve must be refused at the save.

Port carried validate:"gte=0", so `"port": 0` reached the core over the API and
the Telegram bot (the form has always refused it). xray binds 0 to whatever the
OS hands out, changing on every restart, while the inbound list, every share
link and the port-conflict check all read 0. Listen had no rule at all — no
validate: tag, no custom validator — so a typo'd address was stored too.

xray-core refuses both, but validateInboundForXray asks it only about an inbound
that is enabled, local and xray-served; a disabled one, or any other core's, was
never asked by anything.

The one bind that legitimately takes no port stays legal, because the listen
field's own hint tells operators to write it: a unix socket path, absolute or
abstract. F009 / F039.
*/
func TestAddInboundRefusesABindNothingCanServe(t *testing.T) {
	initTestDB(t)
	svc := InboundService{}

	const stream = `{"network":"tcp","security":"none"}`
	// A client per case: an accepted one is really stored, and the next case
	// would then be refused for the email rather than for its bind.
	settings := func(name string) string {
		return `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"` + name + `@bind"}],"decryption":"none"}`
	}

	cases := []struct {
		name    string
		listen  string
		port    int
		refused bool
		why     string
	}{
		{
			name: "port zero", listen: "0.0.0.0", port: 0, refused: true,
			why: "the OS picks the port, so the inbound serves on one that changes every restart and nothing in the panel knows it",
		},
		{
			name: "a listen that is not an address", listen: "vpn.exmaple.com", port: 20141, refused: true,
			why: "xray refuses to listen on a domain; stored, it is an inbound that can never come up",
		},
		{
			name: "an absolute socket path with no port", listen: "/run/xray/in.sock", port: 0, refused: false,
			why: "a unix socket takes no port, and the listen field's hint tells operators to write exactly this",
		},
		{
			name: "an abstract socket with no port", listen: "@xray/in.sock", port: 0, refused: false,
			why: "the abstract half of the same documented feature",
		},
		{
			name: "an ordinary bind", listen: "0.0.0.0", port: 20142, refused: false,
			why: "what every inbound on a working panel looks like",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inbound := &model.Inbound{
				UserId: 1, Enable: true, Port: tc.port, Protocol: model.VLESS,
				Tag: "bind-" + tc.name, Listen: tc.listen,
				Settings: settings(tc.name), StreamSettings: stream,
			}
			_, _, _, err := svc.AddInbound(inbound)
			if tc.refused && err == nil {
				t.Fatalf("AddInbound stored it: %s", tc.why)
			}
			if !tc.refused && err != nil {
				t.Fatalf("AddInbound refused a valid bind (%s): %v", tc.why, err)
			}
		})
	}
}

// And the same rule on the edit path, which is where an operator clears a field
// on something that was working.
func TestUpdateInboundRefusesAPortOfZero(t *testing.T) {
	initTestDB(t)
	svc := InboundService{}

	created, _, _, err := svc.AddInbound(&model.Inbound{
		UserId: 1, Enable: true, Port: 20143, Protocol: model.VLESS, Tag: "bindable", Listen: "0.0.0.0",
		Settings:       `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"bind2@b"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}

	cleared := *created
	cleared.Port = 0
	_, _, _, err = svc.UpdateInbound(&cleared)
	if err == nil {
		t.Fatal("an inbound was moved to port 0, where the OS reassigns it on every restart")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Fatalf("the refusal does not name the port, so nothing on the form can point at it: %v", err)
	}

	reloaded, err := svc.GetInbound(created.Id)
	if err != nil {
		t.Fatalf("GetInbound: %v", err)
	}
	if reloaded.Port != 20143 {
		t.Fatalf("the refused edit was committed anyway: port=%d", reloaded.Port)
	}
}
