package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The panel's own two listeners are not rows in inbounds, so nothing saw them.

An inbound on 2053 or 2096 — both defaults, both in Cloudflare's proxied-HTTPS
set, which is exactly why an operator picks them for a CDN-frontable inbound —
saved with a 200 and then produced a config Xray cannot load. A failed listener
is fatal to the whole process, not to that one inbound, so every client on the
box goes offline and CheckXrayRunningJob restarts it into the same failure every
couple of seconds while the panel shows the inbound as enabled.

The -test pre-flight cannot help here: it is LoadConfig plus core.New, and binds
no ports at all.
*/
func TestAnInboundCannotTakeThePanelsOwnPort(t *testing.T) {
	initTestDB(t)
	setting := &SettingService{}
	if err := setting.SetPort(2053); err != nil {
		t.Fatalf("set the panel port: %v", err)
	}
	if err := setting.setInt("subPort", 2096); err != nil {
		t.Fatalf("set the sub port: %v", err)
	}
	if err := setting.setBool("subEnable", true); err != nil {
		t.Fatalf("enable the sub server: %v", err)
	}

	for _, tc := range []struct {
		name string
		port int
		want string
	}{
		{name: "the web panel's port", port: 2053, want: "panel"},
		{name: "the subscription server's port", port: 2096, want: "subscription"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hit, err := (&InboundService{}).checkPortConflict(vlessOn(tc.port), 0)
			if err != nil {
				t.Fatalf("checkPortConflict: %v", err)
			}
			if hit == nil {
				t.Fatal("accepted; the config it produces is one Xray exits on, taking every client with it")
			}
			if hit.Tag != tc.want {
				t.Fatalf("conflict names %q, want %q", hit.Tag, tc.want)
			}
			// The operator must not be sent looking for a row that will never exist.
			if msg := hit.String(); strings.Contains(msg, "inbound") {
				t.Fatalf("message %q calls a panel listener an inbound", msg)
			}
		})
	}
}

// A UDP-only inbound legitimately shares the port: both listeners are TCP. The
// same coexistence the API-port guard already allows.
func TestAUdpOnlyInboundMayShareThePanelsPort(t *testing.T) {
	initTestDB(t)
	if err := (&SettingService{}).SetPort(2053); err != nil {
		t.Fatal(err)
	}

	hit, err := (&InboundService{}).checkPortConflict(&model.Inbound{
		UserId: 1, Port: 2053, Protocol: model.WGKernel, Enable: true,
		Settings: `{"clients":[]}`,
	}, 0)
	if err != nil {
		t.Fatalf("checkPortConflict: %v", err)
	}
	if hit != nil {
		t.Fatalf("a UDP-only inbound was refused the panel's TCP port: %s", hit)
	}
}

// Turning the subscription server off releases its port, and the guard has to
// agree or it reserves something nothing is listening on.
func TestADisabledSubscriptionServerDoesNotReserveItsPort(t *testing.T) {
	initTestDB(t)
	setting := &SettingService{}
	if err := setting.setInt("subPort", 2096); err != nil {
		t.Fatal(err)
	}
	if err := setting.setBool("subEnable", false); err != nil {
		t.Fatal(err)
	}

	hit, err := (&InboundService{}).checkPortConflict(vlessOn(2096), 0)
	if err != nil {
		t.Fatalf("checkPortConflict: %v", err)
	}
	if hit != nil {
		t.Fatalf("the subscription server is off and its port is still reserved: %s", hit)
	}
}

/*
PUI_PORT is what web.go actually binds when it is set, so reading the stored
setting alone is wrong in both directions: it misses the port in use, and it
reserves one nothing will listen on.
*/
func TestThePortEnvOverrideIsTheOneThatCounts(t *testing.T) {
	initTestDB(t)
	if err := (&SettingService{}).SetPort(2053); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUI_PORT", "8443")

	svc := &InboundService{}
	hit, err := svc.checkPortConflict(vlessOn(8443), 0)
	if err != nil {
		t.Fatalf("checkPortConflict: %v", err)
	}
	if hit == nil {
		t.Fatal("the port the panel is really bound to was not reserved")
	}
	if hit, err = svc.checkPortConflict(vlessOn(2053), 0); err != nil {
		t.Fatalf("checkPortConflict: %v", err)
	}
	if hit != nil {
		t.Fatalf("the overridden setting still reserves %d, which nothing is listening on: %s", 2053, hit)
	}
}

func vlessOn(port int) *model.Inbound {
	return &model.Inbound{
		UserId: 1, Port: port, Protocol: model.VLESS, Enable: true,
		Settings:       `{"clients":[],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
}
