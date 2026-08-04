package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCapabilityRules(t *testing.T) {
	tests := []struct {
		name  string
		cap   string
		facts Facts
		want  bool
	}{
		{"tls on vless over tcp", CapTLS, Facts{Protocol: "vless", Stream: map[string]string{"network": "tcp"}}, true},
		{"tls on hysteria needs no transport", CapTLS, Facts{Protocol: "hysteria"}, true},
		{"tls not on wireguard", CapTLS, Facts{Protocol: "wireguard", Stream: map[string]string{"network": "tcp"}}, false},
		{"tls not on an ineligible transport", CapTLS, Facts{Protocol: "vless", Stream: map[string]string{"network": "kcp"}}, false},

		{"reality on vless over tcp", CapReality, Facts{Protocol: "vless", Stream: map[string]string{"network": "tcp"}}, true},
		{"reality not on vmess", CapReality, Facts{Protocol: "vmess", Stream: map[string]string{"network": "tcp"}}, false},
		{"reality not over ws", CapReality, Facts{Protocol: "vless", Stream: map[string]string{"network": "ws"}}, false},

		{"vision on tcp+tls", CapTLSFlow, Facts{Protocol: "vless", Stream: map[string]string{"network": "tcp", "security": "tls"}}, true},
		{"vision on tcp+reality", CapTLSFlow, Facts{Protocol: "vless", Stream: map[string]string{"network": "tcp", "security": "reality"}}, true},
		{"vision not on bare tcp", CapTLSFlow, Facts{Protocol: "vless", Stream: map[string]string{"network": "tcp", "security": "none"}}, false},
		{
			name: "vision on xhttp when vlessenc is set",
			cap:  CapTLSFlow,
			facts: Facts{
				Protocol: "vless",
				Stream:   map[string]string{"network": "xhttp"},
				Settings: map[string]string{"decryption": "mlkem768x25519plus.native.0rtt.KEY"},
			},
			want: true,
		},
		{
			name:  "vision not on xhttp when encryption is the none sentinel",
			cap:   CapTLSFlow,
			facts: Facts{Protocol: "vless", Stream: map[string]string{"network": "xhttp"}, Settings: map[string]string{"encryption": "none"}},
			want:  false,
		},
		{"vision not on trojan", CapTLSFlow, Facts{Protocol: "trojan", Stream: map[string]string{"network": "tcp", "security": "tls"}}, false},

		{"fallbacks on trojan tcp+tls", CapFallbacks, Facts{Protocol: "trojan", Stream: map[string]string{"network": "tcp", "security": "tls"}}, true},
		{
			name:  "fallbacks are stricter than vision: no xhttp",
			cap:   CapFallbacks,
			facts: Facts{Protocol: "vless", Stream: map[string]string{"network": "xhttp"}, Settings: map[string]string{"decryption": "mlkem768x25519plus.native.0rtt.KEY"}},
			want:  false,
		},

		{"sniffing on vless", CapSniffing, Facts{Protocol: "vless"}, true},
		{"sniffing not on mtproto", CapSniffing, Facts{Protocol: "mtproto"}, false},
		{"sniffing on a core this build does not know", CapSniffing, Facts{Protocol: "openvpn"}, true},

		{"ss2022 by method prefix", CapSS2022, Facts{Protocol: "shadowsocks", Settings: map[string]string{"method": "2022-blake3-aes-128-gcm"}}, true},
		{"legacy shadowsocks cipher is not 2022", CapSS2022, Facts{Protocol: "shadowsocks", Settings: map[string]string{"method": "aes-256-gcm"}}, false},
		{"ss2022 needs the shadowsocks protocol", CapSS2022, Facts{Protocol: "vless", Settings: map[string]string{"method": "2022-blake3-aes-128-gcm"}}, false},

		{"stream on trojan", CapStream, Facts{Protocol: "trojan"}, true},
		{"no stream on mtproto", CapStream, Facts{Protocol: "mtproto"}, false},

		{"an unknown capability is never permitted", "teleport", Facts{Protocol: "vless"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Can(tc.cap, tc.facts); got != tc.want {
				t.Errorf("Can(%q) = %t, want %t", tc.cap, got, tc.want)
			}
		})
	}
}

func TestFactsFromJSONKeepsOnlyScalars(t *testing.T) {
	facts := FactsFromJSON(
		"vless",
		`{"decryption":"mlkem768x25519plus.native.0rtt.KEY","clients":[{"flow":"xtls-rprx-vision"}]}`,
		`{"network":"xhttp","security":"none","xhttpSettings":{"path":"/x"}}`,
	)
	if facts.Settings["decryption"] == "" {
		t.Error("scalar settings field was dropped")
	}
	if _, ok := facts.Settings["clients"]; ok {
		t.Error("array field must not be addressable by a rule")
	}
	if _, ok := facts.Stream["xhttpSettings"]; ok {
		t.Error("nested object must not be addressable by a rule")
	}
	if !Can(CapTLSFlow, facts) {
		t.Error("xhttp + vlessenc must permit vision")
	}
}

func TestMalformedJSONYieldsNoFacts(t *testing.T) {
	facts := FactsFromJSON("vless", "{not json", "")
	if len(facts.Settings) != 0 || len(facts.Stream) != 0 {
		t.Fatalf("unparseable config produced facts: %+v", facts)
	}
	if Can(CapTLSFlow, facts) {
		t.Error("unparseable config must not grant a capability")
	}
}

// goldenCase is one row of the cross-language truth table.
type goldenCase struct {
	Name   string          `json:"name"`
	Facts  Facts           `json:"facts"`
	Expect map[string]bool `json:"expect"`
}

type goldenTable struct {
	Rules map[string]Rule `json:"rules"`
	Cases []goldenCase    `json:"cases"`
}

// goldenMatrix enumerates the combinations both languages must agree on. It is
// a union of focused sub-matrices rather than one cross-product: the full
// product is 1728 rows of mostly-redundant JSON, and a fixture regenerated on
// every rule change has to stay reviewable in a diff.
func goldenMatrix() []goldenCase {
	protocols := []string{"vless", "vmess", "trojan", "shadowsocks", "hysteria", "wireguard", "mtproto", "tunnel", "openvpn"}
	networks := []string{"tcp", "ws", "grpc", "xhttp", "kcp", ""}
	securities := []string{"tls", "reality", "none", ""}

	var cases []goldenCase
	add := func(name string, facts Facts) {
		cases = append(cases, goldenCase{Name: name, Facts: facts, Expect: ResolveAll(facts)})
	}

	// Transport eligibility: every protocol against every transport/security.
	for _, protocol := range protocols {
		for _, network := range networks {
			for _, security := range securities {
				add(protocol+"/"+network+"/"+security, Facts{
					Protocol: protocol,
					Stream:   map[string]string{"network": network, "security": security},
				})
			}
		}
	}
	// VLESS encryption: the only place settings feed a transport rule.
	for _, network := range []string{"tcp", "xhttp"} {
		for _, security := range securities {
			for _, encryption := range []string{"", "none", "mlkem768x25519plus.native.0rtt.KEY"} {
				add("vless-enc/"+network+"/"+security+"/"+encryption, Facts{
					Protocol: "vless",
					Stream:   map[string]string{"network": network, "security": security},
					Settings: map[string]string{"decryption": encryption},
				})
				add("vless-enc-legacy/"+network+"/"+security+"/"+encryption, Facts{
					Protocol: "vless",
					Stream:   map[string]string{"network": network, "security": security},
					Settings: map[string]string{"encryption": encryption},
				})
			}
		}
	}
	// Shadowsocks ciphers drive the only prefix rule.
	for _, protocol := range []string{"shadowsocks", "vless"} {
		for _, method := range []string{"", "aes-256-gcm", "2022-blake3-aes-128-gcm", "2022-blake3-chacha20-poly1305"} {
			add("method/"+protocol+"/"+method, Facts{
				Protocol: protocol,
				Stream:   map[string]string{"network": "tcp"},
				Settings: map[string]string{"method": method},
			})
		}
	}
	return cases
}

func goldenPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "frontend", "src", "test", "golden", "fixtures", "capabilities", "resolve.json"))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	return path
}

// TestCapabilityGoldenTableIsCurrent pins the rule table and its answers into a
// fixture the TypeScript evaluator reads. Change a clause in Go, leave the
// frontend alone, and the vitest twin goes red — which is the only thing that
// stops a fourth copy of these rules appearing.
//
// Regenerate deliberately with PUI_UPDATE_GOLDEN=1.
func TestCapabilityGoldenTableIsCurrent(t *testing.T) {
	path := goldenPath(t)
	want := goldenTable{Rules: CapabilityRules(), Cases: goldenMatrix()}
	encoded, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatalf("encode golden table: %v", err)
	}
	encoded = append(encoded, '\n')

	if os.Getenv("PUI_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture dir: %v", err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("write golden table: %v", err)
		}
		t.Log("golden capability table regenerated")
		return
	}

	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden table (regenerate with PUI_UPDATE_GOLDEN=1): %v", err)
	}
	if string(existing) != string(encoded) {
		t.Fatalf("golden capability table is stale — the Go rules changed but %s did not.\nRegenerate with PUI_UPDATE_GOLDEN=1 and make sure the TypeScript evaluator still agrees.", path)
	}
	if len(want.Cases) == 0 {
		t.Fatal("golden matrix is empty; the cross-language check is certifying nothing")
	}
}
