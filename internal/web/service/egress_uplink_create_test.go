package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An uplink selects no ingress, and the UI said so with a zero.

Inbound ids are autoincrement, so no row is ever 0 — but ingress_inbound_id
carries a foreign key onto inbounds(id), and a JSON body with
`"ingressInboundId": 0` binds to a non-nil *int that reaches the column. Postgres
refused every one with a 23503, which made the panel's only path to authoring an
uplink exit fail on every attempt.

Both halves are covered: the payload no longer sends the zero (frontend test),
and a body that still does is normalised here, because the next caller sends
JSON too.
*/
func TestAddUplinkNormalisesAZeroIngressSelection(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	zero := 0
	row := &model.Egress{
		Type:             "wg-client",
		Remark:           "provider-uplink",
		Enable:           true,
		Owner:            "operator",
		IngressInboundId: &zero,
		Settings: `{"privateKey":"cHJpdmF0ZUtleUZvclRlc3RpbmdQdXJwb3Nlc09ubHk=",` +
			`"publicKey":"cHVibGljS2V5Rm9yVGVzdGluZ1B1cnBvc2VzT25seUFB","endpoint":"198.51.100.7:51820",` +
			`"address":["10.6.0.2/32"]}`,
	}

	created, err := (&EgressService{}).Add(row)
	if err != nil {
		t.Fatalf("creating an uplink exit must not be refused: %v", err)
	}

	var stored model.Egress
	if err := database.GetDB().First(&stored, created.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.IngressInboundId != nil {
		t.Fatalf("ingress_inbound_id = %d, want NULL: an uplink selects no inbound", *stored.IngressInboundId)
	}
}

// A real selection is untouched — the normalisation is about zero, not about
// clearing the field.
func TestAddKeepsARealIngressSelection(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	inbound := &model.Inbound{
		UserId: 1, Enable: true, Tag: "in-front", Port: 27001, Protocol: model.VLESS,
		Listen: "0.0.0.0", Settings: `{"clients":[],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	row := &model.Egress{
		Type:             "wg-client",
		Remark:           "uplink-with-ingress",
		Enable:           true,
		Owner:            "operator",
		IngressInboundId: &inbound.Id,
		Settings: `{"privateKey":"cHJpdmF0ZUtleUZvclRlc3RpbmdQdXJwb3Nlc09ubHk=",` +
			`"publicKey":"cHVibGljS2V5Rm9yVGVzdGluZ1B1cnBvc2VzT25seUFB","endpoint":"198.51.100.7:51820",` +
			`"address":["10.6.0.2/32"]}`,
	}
	created, err := (&EgressService{}).Add(row)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	var stored model.Egress
	if err := database.GetDB().First(&stored, created.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.IngressInboundId == nil || *stored.IngressInboundId != inbound.Id {
		t.Fatalf("ingress_inbound_id = %v, want %d", stored.IngressInboundId, inbound.Id)
	}
}
