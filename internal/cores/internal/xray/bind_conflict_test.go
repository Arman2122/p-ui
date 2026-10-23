package xray

import (
	"encoding/json"
	"strings"
	"testing"

	engine "github.com/Arman2122/p-ui/internal/xray"
)

func bindConfig(t *testing.T, inbounds string) *engine.Config {
	t.Helper()
	var config engine.Config
	if err := json.Unmarshal([]byte(`{"inbounds":[`+inbounds+`]}`), &config); err != nil {
		t.Fatal(err)
	}
	return &config
}

func TestGeneratedBindConflictMatrix(t *testing.T) {
	const tcp = `{"tag":"tcp","port":8443,"listen":"0.0.0.0","protocol":"vless"}`
	const local = `{"tag":"local","port":8443,"listen":"127.0.0.1","protocol":"mixed"}`
	const udp = `{"tag":"udp","port":8443,"listen":"0.0.0.0","protocol":"hysteria"}`
	const six = `{"tag":"six","port":8443,"listen":"::","protocol":"vless","streamSettings":{"sockopt":{"v6only":true}}}`
	for _, tc := range []struct {
		name, desired, running string
		want                   int
	}{
		{"wildcard and loopback", tcp + "," + local, "", 1},
		{"TCP and UDP coexist", tcp + "," + udp, "", 0},
		{"SOCKS alias also binds UDP", `{"tag":"relay","port":8443,"protocol":"socks","settings":{"udp":true}},` + udp, "", 1},
		{"legacy tunnel network", `{"tag":"relay","port":8443,"protocol":"dokodemo-door","settings":{"network":"udp"}},` + udp, "", 1},
		{"legacy network overrides renamed field", `{"tag":"relay","port":8443,"protocol":"tunnel","settings":{"allowedNetwork":"tcp","network":"udp"}},` + tcp, "", 0},
		{"null legacy network preserves current field", `{"tag":"relay","port":8443,"protocol":"tunnel","settings":{"allowedNetwork":"udp","network":null}},` + udp, "", 1},
		{"HTTP3 is UDP", `{"tag":"h3","port":8443,"protocol":"vless","streamSettings":{"network":"xhttp","security":"tls","tlsSettings":{"alpn":["h3"]}}},` + udp, "", 1},
		{"HTTP3 case variants", `{"tag":"h3","port":8443,"protocol":"VLESS","streamSettings":{"network":"XHTTP","security":"TLS","tlsSettings":{"alpn":["h3"]}}},` + udp, "", 1},
		{"method overrides network", `{"tag":"mkcp","port":8443,"protocol":"vless","streamSettings":{"network":"tcp","method":"mKCP"}},` + udp, "", 1},
		{"network array", `{"tag":"tunnel","port":8443,"protocol":"tunnel","settings":{"allowedNetwork":["UDP"]}},` + udp, "", 1},
		{"TUN does not bind port", `{"tag":"device","port":8443,"protocol":"tun"},` + tcp, "", 0},
		{"IPv6 only and IPv4 coexist", six + "," + local, "", 0},
		{"IPv6 only and dual wildcard overlap", six + "," + tcp, "", 1},
		{"dual wildcard and IPv4 overlap", `{"tag":"six","port":8443,"listen":"::0","protocol":"vless"},` + local, "", 1},
		{"expanded wildcard and IPv4 overlap", `{"tag":"six","port":8443,"listen":"0:0:0:0:0:0:0:0","protocol":"vless"},` + local, "", 1},
		{"IPv4 mapped address aliases", `{"tag":"mapped","port":8443,"listen":"::ffff:127.0.0.1","protocol":"vless"},` + local, "", 1},
		{"separate concrete IPs", `{"tag":"other","port":8443,"listen":"127.0.0.2","protocol":"vless"},` + local, "", 0},
		{"Unix paths ignore numeric form ports", `{"tag":"unix","port":8443,"listen":"/tmp/test.sock","protocol":"vless"},` + tcp, "", 0},
		{"established pair", tcp + "," + local, local + "," + tcp, 0},
		{"renamed pair", strings.ReplaceAll(tcp+","+local, `"tag":"`, `"tag":"renamed-`), tcp + "," + local, 0},
		{"changed socket requires new proof", tcp + "," + tcp, tcp + "," + local, 1},
		{"changed port requires new proof", strings.ReplaceAll(tcp+","+local, "8443", "8444"), tcp + "," + local, 1},
		{"third listener is not excused", tcp + "," + local + "," + local, tcp + "," + local, 2},
		{"UDP addition needs new proof", `{"tag":"relay","port":8443,"protocol":"socks","settings":{"udp":true}},` + udp, tcp + "," + udp, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var running *engine.Config
			if tc.running != "" {
				running = bindConfig(t, tc.running)
			}
			conflicts := engine.BindConflicts(bindConfig(t, tc.desired), running, (&Core{}).Transports)
			if len(conflicts) != tc.want {
				t.Fatalf("conflicts = %v, want %d", conflicts, tc.want)
			}
		})
	}
}
