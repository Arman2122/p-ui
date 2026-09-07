package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An MTProto inbound running the WEB relay can be the fallback child of a VLESS
or Trojan TLS inbound, so Xray fronts the relay instead of a Caddy the operator
runs. Measured on the VM against Xray 26.7.28 and the real relay: the entry
these tests pin serves the cover page through Xray; the relay refuses a stream
without a PROXY header; a client that negotiates h2 with the master reaches the
relay as HTTP/2 and gets nothing, so the master may offer http/1.1 alone.
*/

const webRelayMasterStream = `{"network":"tcp","security":"tls","tlsSettings":{"alpn":["http/1.1"],"certificates":[{"certificateFile":"/tmp/c.pem","keyFile":"/tmp/k.pem"}]}}`

const webRelayChildSettings = `{"secret":"ee11","webEnable":true,"webHost":"web.example.com","webBackendEnable":true,"webBackendPort":2398,"webBindPort":8099,"webStatsPort":8100}`

func seedWebRelayMaster(t *testing.T, tag string, port int, stream string) *model.Inbound {
	t.Helper()
	seedInboundConflict(t, tag, "0.0.0.0", port, model.VLESS, stream, `{"clients":[],"decryption":"none"}`)
	return loadInboundByTag(t, tag)
}

func seedWebRelayChild(t *testing.T, tag string, port int) *model.Inbound {
	t.Helper()
	seedInboundConflict(t, tag, "0.0.0.0", port, model.MTProto, `{"network":"tcp","security":"none"}`, webRelayChildSettings)
	return loadInboundByTag(t, tag)
}

func TestAWebRelayChildRendersTheRelayFallback(t *testing.T) {
	setupConflictDB(t)
	master := seedWebRelayMaster(t, "fbw-master", 25201, webRelayMasterStream)
	child := seedWebRelayChild(t, "fbw-child", 25202)
	seedInboundConflict(t, "fbw-plain", "127.0.0.1", 25203, model.VLESS, `{"network":"ws","security":"none","wsSettings":{"path":"/ws"}}`, `{"clients":[],"decryption":"none"}`)
	plain := loadInboundByTag(t, "fbw-plain")

	svc := &FallbackService{}
	err := svc.SetByMaster(master.Id, []FallbackInput{
		{ChildId: child.Id, Name: "web.example.com", Alpn: "http/1.1", Xver: 2},
		{ChildId: plain.Id, Path: "/ws"},
	})
	if err != nil {
		t.Fatalf("SetByMaster: %v", err)
	}
	got, err := svc.BuildFallbacksJSON(nil, master.Id)
	if err != nil || len(got) != 2 {
		t.Fatalf("BuildFallbacksJSON = %v, %v; want the relay entry and the plain one", got, err)
	}
	rendered, _ := json.Marshal(got[0])
	const want = `{"alpn":"http/1.1","dest":"127.0.0.1:8099","name":"web.example.com","xver":2}`
	if string(rendered) != want {
		t.Fatalf("relay fallback = %s\nwant %s: the relay's loopback port, not the FakeTLS listen", rendered, want)
	}
	if dest := got[1]["dest"]; dest != "127.0.0.1:25203" {
		t.Fatalf("plain child dest = %v, want its own listen", dest)
	}

	// The relay child keeps its own links; the plain child is projected.
	rows := []*model.Inbound{child, plain}
	(&InboundService{}).annotateFallbackParents(database.GetDB(), rows)
	if child.FallbackParent != nil {
		t.Fatalf("the relay child was marked for projection through the master: %+v", child.FallbackParent)
	}
	if plain.FallbackParent == nil || plain.FallbackParent.MasterId != master.Id {
		t.Fatalf("the plain child lost its projection: %+v", plain.FallbackParent)
	}
}

func TestAWebRelayFallbackIsRefusedWhereTheClientWouldGetNothing(t *testing.T) {
	setupConflictDB(t)
	child := seedWebRelayChild(t, "fbw-r-child", 25212)
	svc := &FallbackService{}
	cases := []struct {
		tag, stream, want string
		port              int
		item              FallbackInput
	}{
		{"fbw-r-xver", webRelayMasterStream, "xver 1 or 2", 25213, FallbackInput{ChildId: child.Id, Xver: 0}},
		{"fbw-r-dest", webRelayMasterStream, "leave Dest empty", 25214, FallbackInput{ChildId: child.Id, Xver: 2, Dest: "8099"}},
		{"fbw-r-reality", `{"network":"tcp","security":"reality","realitySettings":` + realityKeypair + `}`, "REALITY cannot front it", 25215, FallbackInput{ChildId: child.Id, Xver: 2}},
		{"fbw-r-h2", `{"network":"tcp","security":"tls","tlsSettings":{"alpn":["h2","http/1.1"]}}`, "http/1.1 alone", 25216, FallbackInput{ChildId: child.Id, Xver: 2}},
		{"fbw-r-noalpn", `{"network":"tcp","security":"tls","tlsSettings":{}}`, "http/1.1 alone", 25217, FallbackInput{ChildId: child.Id, Xver: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.tag, func(t *testing.T) {
			master := seedWebRelayMaster(t, tc.tag, tc.port, tc.stream)
			err := svc.SetByMaster(master.Id, []FallbackInput{tc.item})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("SetByMaster = %v, want a refusal naming %q", err, tc.want)
			}
			if rows, _ := svc.GetByMaster(master.Id); len(rows) != 0 {
				t.Fatalf("the refused fallback was stored anyway: %d row(s)", len(rows))
			}
		})
	}
	// xver 1 is a PROXY v1 header, which the relay accepts as it does v2.
	master := seedWebRelayMaster(t, "fbw-r-xver1", 25218, webRelayMasterStream)
	if err := svc.SetByMaster(master.Id, []FallbackInput{{ChildId: child.Id, Xver: 1}}); err != nil {
		t.Fatalf("xver 1 refused: %v", err)
	}
}

// An edit on either side that would strand the relay is refused, not applied:
// the master moving off http/1.1 alone, the child turning the WEB proxy off.
func TestAnEditThatStrandsAWebRelayFallbackIsRefused(t *testing.T) {
	setupConflictDB(t)
	master := seedWebRelayMaster(t, "fbw-e-master", 25221, webRelayMasterStream)
	child := seedWebRelayChild(t, "fbw-e-child", 25222)
	svc := &FallbackService{}
	if err := svc.SetByMaster(master.Id, []FallbackInput{{ChildId: child.Id, Name: "web.example.com", Alpn: "http/1.1", Xver: 2}}); err != nil {
		t.Fatalf("SetByMaster: %v", err)
	}
	is := &InboundService{}
	edited := *master
	edited.StreamSettings = `{"network":"tcp","security":"tls","tlsSettings":{"alpn":["h2","http/1.1"]}}`
	if err := is.checkFallbacksSurviveEdit(&edited); err == nil || !strings.Contains(err.Error(), "http/1.1 alone") || !strings.Contains(err.Error(), "Detach") {
		t.Fatalf("a master offering h2 over a relay child was allowed: %v", err)
	}
	same := *master
	if err := is.checkFallbacksSurviveEdit(&same); err != nil {
		t.Fatalf("an edit that keeps the front intact was refused: %v", err)
	}

	off := strings.Replace(webRelayChildSettings, `"webEnable":true`, `"webEnable":false`, 1)
	if err := svc.CheckChildEdit(child.Id, webRelayChildSettings, off); err == nil || !strings.Contains(err.Error(), "detach it before turning the WEB proxy off") {
		t.Fatalf("turning the WEB proxy off under a fallback was allowed: %v", err)
	}
	if err := svc.CheckChildEdit(child.Id, webRelayChildSettings, webRelayChildSettings); err != nil {
		t.Fatalf("an edit that keeps the relay was refused: %v", err)
	}
	if err := svc.SetByMaster(master.Id, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckChildEdit(child.Id, webRelayChildSettings, off); err != nil {
		t.Fatalf("with the fallback detached the WEB proxy may be turned off: %v", err)
	}
}
