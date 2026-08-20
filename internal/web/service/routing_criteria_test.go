package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
compileRule copies criteria values into the emitted Xray rule verbatim, and the
rule editor types port, ip, domain and sourcePort as bare strings on both sides.
So one unreadable character reaches xray-core, which refuses the whole routing
section and with it the whole config: every client on the box drops, and the
pending-restart flag re-arms and retries the same broken config every tick.

Checked against Xray 26.7.28 on the test VM — each of these exits the core.
*/
func TestRoutingCriteriaTheCoreCannotReadAreRefused(t *testing.T) {
	tests := []struct {
		name     string
		criteria string
		wants    string
	}{
		{name: "a port that is not a number", criteria: `{"port":"abc"}`, wants: "invalid port"},
		{name: "a source port that is not a number", criteria: `{"sourcePort":"http"}`, wants: "invalid port"},
		{name: "an ip that is not an address", criteria: `{"ip":["not-an-ip"]}`, wants: "not-an-ip"},
		{name: "a source that is not an address", criteria: `{"source":["192.168.0.0/33"]}`, wants: "192.168.0.0/33"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := &model.RoutingRule{
				Enable: true, IngressScope: model.RoutingScopeSelected, IngressIds: `[1]`,
				DestKind: model.RoutingDestDirect, Criteria: tc.criteria,
			}
			err := normalizeRule(rule)
			if err == nil {
				t.Fatal("a rule the core cannot read was accepted; it takes every inbound down on the next restart")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error %q does not carry the core's reason %q", err, tc.wants)
			}
		})
	}
}

// The criteria an operator actually writes have to keep working, or the guard
// has replaced one outage with another.
func TestOrdinaryRoutingCriteriaStillSave(t *testing.T) {
	tests := []struct {
		name     string
		criteria string
	}{
		{name: "no criteria at all", criteria: `{}`},
		{name: "a port list and a range", criteria: `{"port":"80,443,1000-2000"}`},
		{name: "domains by every prefix xray takes", criteria: `{"domain":["example.com","full:a.example.com","regexp:.*\\.example\\.com$"]}`},
		{name: "a cidr and a bare address", criteria: `{"ip":["10.0.0.0/8","1.2.3.4"]}`},
		{name: "both transports", criteria: `{"network":"tcp,udp"}`},
		{name: "a sniffed protocol", criteria: `{"protocol":["bittorrent"]}`},
		{name: "source and inbound-side criteria", criteria: `{"source":["192.168.0.0/16"],"sourcePort":"1024-65535"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := &model.RoutingRule{
				Enable: true, IngressScope: model.RoutingScopeSelected, IngressIds: `[1]`,
				DestKind: model.RoutingDestDirect, Criteria: tc.criteria,
			}
			if err := normalizeRule(rule); err != nil {
				t.Fatalf("an ordinary rule must save: %v", err)
			}
		})
	}
}

/*
A geoip:/geosite: token is resolved against the .dat files next to the binary.
Missing, or mid-update while UpdateGeofile rewrites them, that is the panel
unable to judge — not the operator having typed something wrong. Refusing there
would make a broken asset download take the rule editor with it.
*/
func TestGeoTokensSaveEvenWithoutTheDataFiles(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	t.Setenv("XRAY_LOCATION_ASSET", t.TempDir())

	for _, criteria := range []string{`{"ip":["geoip:private"]}`, `{"domain":["geosite:cn"]}`} {
		rule := &model.RoutingRule{
			Enable: true, IngressScope: model.RoutingScopeSelected, IngressIds: `[1]`,
			DestKind: model.RoutingDestDirect, Criteria: criteria,
		}
		if err := normalizeRule(rule); err != nil {
			t.Fatalf("criteria %s must save with no geo data present: %v", criteria, err)
		}
	}
}
