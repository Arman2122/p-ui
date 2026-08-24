package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// coreRejectedInbounds are configurations the panel offers through its own form
// and xray-core refuses to load. Each was confirmed against the pinned binary:
// the core aborts the WHOLE config on any one of them, so a saved row here means
// every inbound of every protocol stops answering on the next restart.
var coreRejectedInbounds = []struct {
	name    string
	inbound model.Inbound
	wants   string
}{
	{
		name: "reality without a private key",
		inbound: model.Inbound{
			Enable: true, Port: 20001, Protocol: model.VLESS, Tag: "reality-empty-key",
			Settings: `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"a@b"}],"decryption":"none"}`,
			StreamSettings: `{"network":"tcp","security":"reality","realitySettings":` +
				`{"target":"example.com:443","serverNames":["example.com"],"shortIds":["aa"],"privateKey":""}}`,
		},
		wants: `empty "privateKey"`,
	},
	{
		name: "reality with no serverNames",
		inbound: model.Inbound{
			Enable: true, Port: 20011, Protocol: model.VLESS, Tag: "reality-no-servernames",
			Settings: `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"a@b"}],"decryption":"none"}`,
			StreamSettings: `{"network":"tcp","security":"reality","realitySettings":` +
				`{"target":"example.com:443","serverNames":[],"shortIds":["aa"],` +
				`"privateKey":"YIrCPHOtBRQVvz-x2ZaLRD3vwUEwOHZYZKUmDlM8UUA"}}`,
		},
		wants: `empty "serverNames"`,
	},
	{
		name: "reality with no shortIds",
		inbound: model.Inbound{
			Enable: true, Port: 20012, Protocol: model.VLESS, Tag: "reality-no-shortids",
			Settings: `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"a@b"}],"decryption":"none"}`,
			StreamSettings: `{"network":"tcp","security":"reality","realitySettings":` +
				`{"target":"example.com:443","serverNames":["example.com"],"shortIds":[],` +
				`"privateKey":"YIrCPHOtBRQVvz-x2ZaLRD3vwUEwOHZYZKUmDlM8UUA"}}`,
		},
		wants: `empty "shortIds"`,
	},
	{
		name: "reality with a shortId that is not even-length hex",
		inbound: model.Inbound{
			Enable: true, Port: 20013, Protocol: model.VLESS, Tag: "reality-odd-shortid",
			Settings: `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"a@b"}],"decryption":"none"}`,
			StreamSettings: `{"network":"tcp","security":"reality","realitySettings":` +
				`{"target":"example.com:443","serverNames":["example.com"],"shortIds":["abc"],` +
				`"privateKey":"YIrCPHOtBRQVvz-x2ZaLRD3vwUEwOHZYZKUmDlM8UUA"}}`,
		},
		wants: `invalid "shortIds[0]"`,
	},
	{
		// Pruned away entirely rather than blanked: the builder then takes its
		// client branch, which fails on a different field and is easy to misread.
		name: "reality with no target at all",
		inbound: model.Inbound{
			Enable: true, Port: 20014, Protocol: model.VLESS, Tag: "reality-no-target",
			Settings: `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"a@b"}],"decryption":"none"}`,
			StreamSettings: `{"network":"tcp","security":"reality","realitySettings":` +
				`{"serverNames":["example.com"],"shortIds":["aa"],` +
				`"privateKey":"YIrCPHOtBRQVvz-x2ZaLRD3vwUEwOHZYZKUmDlM8UUA"}}`,
		},
		wants: `please use "serverName" instead`,
	},
	{
		name: "tls with a blank certificate",
		inbound: model.Inbound{
			Enable: true, Port: 20002, Protocol: model.VLESS, Tag: "tls-blank-cert",
			Settings:       `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"a@b"}],"decryption":"none"}`,
			StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"certificates":[{"certificate":[],"key":[]}]}}`,
		},
		wants: "both file and bytes are empty",
	},
	{
		name: "shadowsocks 2022 chacha20 carrying a client",
		inbound: model.Inbound{
			Enable: true, Port: 20003, Protocol: model.Shadowsocks, Tag: "ss2022-chacha-multiuser",
			Settings: `{"method":"2022-blake3-chacha20-poly1305",` +
				`"password":"9a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9","network":"tcp,udp",` +
				`"clients":[{"email":"a@b","password":"1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"}]}`,
			StreamSettings: `{"network":"tcp","security":"none"}`,
		},
		wants: "only blake3-aes-*-gcm methods are supported",
	},
}

// TestValidateInboundForXrayRefusesWhatTheCoreRefuses pins the refusals to the
// core's own wording, so a builder change shows up here rather than as a dead
// box on the next restart.
func TestValidateInboundForXrayRefusesWhatTheCoreRefuses(t *testing.T) {
	for _, tc := range coreRejectedInbounds {
		t.Run(tc.name, func(t *testing.T) {
			inbound := tc.inbound
			err := validateInboundForXray(&inbound)
			if err == nil {
				t.Fatalf("validateInboundForXray accepted a config xray-core rejects")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error %q does not name the core's reason %q", err, tc.wants)
			}
		})
	}
}

// TestValidateInboundForXrayScope: the check answers for what this panel's own
// Xray would load and for nothing else, so a row that cannot abort the load
// cannot be refused by it either.
func TestValidateInboundForXrayScope(t *testing.T) {
	nodeID := 7
	base := coreRejectedInbounds[0].inbound

	disabled := base
	disabled.Enable = false
	if err := validateInboundForXray(&disabled); err != nil {
		t.Fatalf("a disabled inbound is never in the config, got %v", err)
	}

	onNode := base
	onNode.NodeID = &nodeID
	if err := validateInboundForXray(&onNode); err != nil {
		t.Fatalf("a node's inbound is never in this config, got %v", err)
	}

	notXray := model.Inbound{
		Enable: true, Port: 20004, Protocol: model.MTProto, Tag: "mtproto",
		Settings: `{"secret":"ee000102030405060708090a0b0c0d0e0f6578616d706c652e636f6d"}`,
	}
	if err := validateInboundForXray(&notXray); err != nil {
		t.Fatalf("a kind Xray does not serve is not its to judge, got %v", err)
	}

	good := model.Inbound{
		Enable: true, Port: 20005, Protocol: model.VLESS, Tag: "plain-vless",
		Settings:       `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"a@b"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := validateInboundForXray(&good); err != nil {
		t.Fatalf("an ordinary inbound must save: %v", err)
	}

	// The counterpart to the four REALITY refusals above: a complete keypair
	// still saves, so the guard cannot be satisfied by refusing REALITY itself.
	reality := model.Inbound{
		Enable: true, Port: 20006, Protocol: model.VLESS, Tag: "reality-complete",
		Settings: `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"a@b"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"reality","realitySettings":` +
			`{"target":"example.com:443","serverNames":["example.com"],"shortIds":["aa"],` +
			`"privateKey":"YIrCPHOtBRQVvz-x2ZaLRD3vwUEwOHZYZKUmDlM8UUA"}}`,
	}
	if err := validateInboundForXray(&reality); err != nil {
		t.Fatalf("a complete REALITY inbound must save: %v", err)
	}
}

// TestAddInboundRefusesConfigXrayCoreRejects drives the save boundary itself:
// without the call in AddInbound each of these is persisted and takes the core
// down on the next restart, with the save reported as a success.
func TestAddInboundRefusesConfigXrayCoreRejects(t *testing.T) {
	initTestDB(t)
	svc := InboundService{}

	for _, tc := range coreRejectedInbounds {
		t.Run(tc.name, func(t *testing.T) {
			inbound := tc.inbound
			inbound.UserId = 1
			if _, _, _, err := svc.AddInbound(&inbound); err == nil {
				t.Fatal("AddInbound stored a config xray-core refuses to load")
			}
			var stored int64
			if err := database.GetDB().Model(model.Inbound{}).
				Where("port = ?", tc.inbound.Port).Count(&stored).Error; err != nil {
				t.Fatalf("count inbounds: %v", err)
			}
			if stored != 0 {
				t.Fatalf("the refused inbound was persisted anyway (%d rows)", stored)
			}
		})
	}
}

// TestUpdateInboundRefusesConfigXrayCoreRejects: the same row can be reached by
// editing a working inbound onto a broken setting, which is how F005 and F321
// are actually hit — the operator clears a field on something that was fine.
func TestUpdateInboundRefusesConfigXrayCoreRejects(t *testing.T) {
	initTestDB(t)
	svc := InboundService{}

	good := model.Inbound{
		UserId: 1, Enable: true, Port: 20101, Protocol: model.VLESS, Tag: "editable",
		Settings:       `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"edit@b"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	created, _, _, err := svc.AddInbound(&good)
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}

	broken := *created
	broken.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":` +
		`{"target":"example.com:443","serverNames":["example.com"],"shortIds":["aa"],"privateKey":""}}`
	if _, _, _, err := svc.UpdateInbound(&broken); err == nil {
		t.Fatal("UpdateInbound stored a config xray-core refuses to load")
	}

	reloaded, err := svc.GetInbound(created.Id)
	if err != nil {
		t.Fatalf("GetInbound: %v", err)
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(reloaded.StreamSettings), &stream); err != nil {
		t.Fatalf("stored streamSettings: %v", err)
	}
	if stream["security"] != "none" {
		t.Fatalf("the refused edit was committed anyway: security=%v", stream["security"])
	}
}

/*
TestSetInboundEnableRefusesABrokenRow closes the one save that does not go
through UpdateInbound. A broken row can legitimately be stored while disabled —
that is the escape hatch validateInboundForXray deliberately leaves — so
enabling it is where it becomes the core's problem, and the toggle reports
success while the restart it asks for is silently refused forever.
*/
func TestSetInboundEnableRefusesABrokenRow(t *testing.T) {
	initTestDB(t)
	svc := InboundService{}

	broken := coreRejectedInbounds[0].inbound
	broken.UserId = 1
	broken.Port = 20201
	broken.Tag = "enable-broken"
	broken.Enable = false
	created, _, _, err := svc.AddInbound(&broken)
	if err != nil {
		t.Fatalf("a disabled row must still save, that is the escape hatch: %v", err)
	}

	if _, err := svc.SetInboundEnable(created.Id, true); err == nil {
		t.Fatal("a config xray-core refuses was enabled; the restart it asks for is then refused with only a log line")
	}

	reloaded, err := svc.GetInbound(created.Id)
	if err != nil {
		t.Fatalf("GetInbound: %v", err)
	}
	if reloaded.Enable {
		t.Fatal("the refused toggle was written anyway")
	}

	// Disabling one is never refused, or a broken row could not be parked.
	if _, err := svc.SetInboundEnable(created.Id, false); err != nil {
		t.Fatalf("disabling must always be allowed: %v", err)
	}
}

/*
TestSaveAcceptsACertificateNotIssuedYet is one half of a deliberate split.

Pointing an inbound at a path certbot has not written yet is the ordinary order
of operations, and the save boundary is the wrong place to break it: the file can
appear at any time between the save and the restart. The restart pre-flight is
the hard gate — internal/xray's TestConfig refuses to swap a running core for one
that cannot read the certificate — so a file that never appears cannot become an
outage either way.

The shape of the certificate is still judged here. An empty one has no path that
could be missing, which is what keeps F321 closed.
*/
func TestSaveAcceptsACertificateNotIssuedYet(t *testing.T) {
	initTestDB(t)
	svc := InboundService{}

	pending := model.Inbound{
		UserId: 1, Enable: true, Port: 20301, Protocol: model.VLESS, Tag: "cert-pending",
		Settings: `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"pending@b"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"certificates":[` +
			`{"certificateFile":"/etc/letsencrypt/live/example.com/fullchain.pem",` +
			`"keyFile":"/etc/letsencrypt/live/example.com/privkey.pem"}]}}`,
	}
	if _, _, _, err := svc.AddInbound(&pending); err != nil {
		t.Fatalf("an inbound whose certificate is not issued yet must save: %v", err)
	}

	// The structural refusal is untouched, which is what keeps F321 closed: a
	// blank entry carries no path, so nothing about it is "not there yet".
	// (Probing the pinned core showed this is the ONLY certificate shape its
	// builder rejects — a real certificate with no key beside it, and even a
	// tlsSettings with no certificates array at all, both build.)
	blank := pending
	blank.Id = 0
	blank.Port = 20302
	blank.Tag = "cert-blank"
	blank.Settings = `{"clients":[{"id":"11111111-2222-3333-4444-555555555555","email":"blank@b"}],"decryption":"none"}`
	blank.StreamSettings = `{"network":"tcp","security":"tls","tlsSettings":{"certificates":[{"certificate":[],"key":[]}]}}`
	_, _, _, err := svc.AddInbound(&blank)
	if err == nil {
		t.Fatal("the blank certificate the TLS radio seeds was accepted")
	}
	if !strings.Contains(err.Error(), "both file and bytes are empty") {
		t.Fatalf("error %q does not name the core's reason", err)
	}
}
