package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// seedAwgSubscription writes an awgkernel inbound and its one client, with or
// without obfuscation, through the same rows production writes.
func seedAwgSubscription(t *testing.T, subId string, port int, tag string, obfuscated bool) *model.Inbound {
	t.Helper()
	db := database.GetDB()

	settings := map[string]any{
		"secretKey": fileShareServerKey,
		"dns":       "9.9.9.9",
		"clients": []map[string]any{{
			"email": "u@" + tag, "enable": true, "subId": subId,
			"privateKey": "clientPrivateKey=", "allowedIPs": []string{"10.9.0.4/32"},
		}},
	}
	if obfuscated {
		settings["awg"] = map[string]any{"jc": 4, "jmin": 40, "jmax": 70, "s1": 20, "s2": 30}
	}
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	in := &model.Inbound{Port: port, Protocol: model.AWGKernel, Enable: true, Tag: tag, Settings: string(body)}
	if err := db.Create(in).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	rec := &model.ClientRecord{
		Email: "u@" + tag, SubID: subId, Enable: true,
		PrivateKey: "clientPrivateKey=", AllowedIPs: "10.9.0.4/32",
	}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: in.Id}).Error; err != nil {
		t.Fatalf("create link: %v", err)
	}
	return in
}

/*
Neither a mihomo wireguard proxy nor an xray wireguard outbound has anywhere to
put jc/jmin/jmax/s1..s4, so for an obfuscated inbound both used to emit an entry
that looks correct and never completes a handshake — while the same client's raw
link, which does carry the parameters, works. The operator sees "Clash is broken
for some users" and finds no error anywhere.

Nothing beats something that silently does not work: a missing proxy is
diagnosable. The plain inbound is the control — the omission is about the
obfuscation, not about the kind.
*/
func TestPlainRenderersOmitAnObfuscatedAwgInbound(t *testing.T) {
	initSubDB(t)

	obfuscated := seedAwgSubscription(t, "awgsub", 51830, "awg-obf", true)
	plain := seedAwgSubscription(t, "plainsub", 51831, "awg-plain", false)

	t.Run("clash", func(t *testing.T) {
		clash := NewSubClashService(false, "", false, NewSubService(""))
		for _, tc := range []struct {
			name    string
			subId   string
			inbound *model.Inbound
			want    bool
		}{
			{name: "obfuscated is omitted", subId: "awgsub", inbound: obfuscated},
			{name: "plain is still served", subId: "plainsub", inbound: plain, want: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				body, _, err := clash.GetClash(tc.subId, "example.com")
				if err != nil {
					t.Fatalf("GetClash: %v", err)
				}
				if got := strings.Contains(body, tc.inbound.Tag); got != tc.want {
					t.Fatalf("clash config contains the inbound = %v, want %v:\n%s", got, tc.want, body)
				}
			})
		}
	})

	t.Run("json", func(t *testing.T) {
		jsonSvc := NewSubJsonService("", "", "", NewSubService(""))
		for _, tc := range []struct {
			name  string
			subId string
			want  bool
		}{
			{name: "obfuscated is omitted", subId: "awgsub"},
			{name: "plain is still served", subId: "plainsub", want: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				body, _, err := jsonSvc.GetJson(tc.subId, "example.com", false)
				if err != nil {
					t.Fatalf("GetJson: %v", err)
				}
				if got := strings.Contains(body, `"protocol": "wireguard"`); got != tc.want {
					t.Fatalf("json config carries a wireguard outbound = %v, want %v:\n%s", got, tc.want, body)
				}
			})
		}
	})
}
