package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The Clients page is the path the inbound-save validator cannot see: the inbound
was already stored, legally, with no clients — xray-core accepts
2022-blake3-chacha20-poly1305 as a single-user inbound. Attaching the first
client is what flips it to multi-user and makes the core refuse the whole
config, so the refusal has to happen here.
*/
func TestAddInboundClientRefusesTheSingleUserSS2022Method(t *testing.T) {
	setupConflictDB(t)

	const chacha = `{"method":"2022-blake3-chacha20-poly1305",` +
		`"password":"9a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9","network":"tcp,udp"}`
	seedInboundConflict(t, "ss-chacha", "0.0.0.0", 24801, model.Shadowsocks, `{"network":"tcp"}`, chacha)
	seeded := loadInboundByTag(t, "ss-chacha")

	add := &model.Inbound{Id: seeded.Id, Protocol: model.Shadowsocks, Settings: clientsSettings(t, []model.Client{
		{Email: "first@ss", Enable: true, Password: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"},
	})}
	_, err := (&ClientService{}).AddInboundClient(&InboundService{}, add)
	if err == nil {
		t.Fatal("a client was attached to a single-user shadowsocks inbound; the core refuses that config whole")
	}
	if !strings.Contains(err.Error(), "single-user") {
		t.Fatalf("error %q does not say why the client cannot be attached", err)
	}

	if emails := settingsClientEmails(t, seeded.Id); len(emails) != 0 {
		t.Fatalf("the refused client was written to settings anyway: %v", emails)
	}
}

// The aes methods are the ones xray's multi-user branch accepts, so the same
// attach must go through: a guard that refuses every 2022 cipher would take the
// panel's only working Shadowsocks-2022 configuration with it.
func TestAddInboundClientAcceptsTheMultiUserSS2022Methods(t *testing.T) {
	setupConflictDB(t)

	const aes = `{"method":"2022-blake3-aes-128-gcm",` +
		`"password":"9a1b2c3d4e5f60718293a4b5c6d7e8f8","network":"tcp,udp"}`
	seedInboundConflict(t, "ss-aes", "0.0.0.0", 24802, model.Shadowsocks, `{"network":"tcp"}`, aes)
	seeded := loadInboundByTag(t, "ss-aes")

	add := &model.Inbound{Id: seeded.Id, Protocol: model.Shadowsocks, Settings: clientsSettings(t, []model.Client{
		{Email: "first@ssaes", Enable: true, Password: "1a2b3c4d5e6f708192a3b4c5d6e7f809"},
	})}
	if _, err := (&ClientService{}).AddInboundClient(&InboundService{}, add); err != nil {
		t.Fatalf("AddInboundClient on a multi-user method: %v", err)
	}
	if emails := settingsClientEmails(t, seeded.Id); len(emails) != 1 {
		t.Fatalf("client emails after the add = %v, want one", emails)
	}
}
