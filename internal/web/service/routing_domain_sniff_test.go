package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	json_util "github.com/Arman2122/p-ui/internal/util/json_util"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
A domain rule makes the inbound it names read the payload.

Xray matches a domain from the sniffer, and the sniffer runs on an Xray inbound.
The panel's own default for a new inbound is sniffing OFF -- so a rule saying
"google.com goes through this exit" saved, displayed as active, and never
matched a single connection. F060 on the kinds people actually use.

routeOnly is the load-bearing part and is asserted here rather than described:
without it the dispatcher REPLACES the dial target with the sniffed name, and
the front's containment guard matches `ip: geoip:private`. A target rewritten to
a domain has no IP for that guard to read, so a rule aimed at a private address
would slip past the one protection a front is not exempt from. Verified against
the pinned core: GetTargetDomain reads RouteTarget, GetTargetIPs reads Target.
*/
func seedDomainRule(t *testing.T, inboundID int) {
	t.Helper()
	ids, err := json.Marshal([]int{inboundID})
	if err != nil {
		t.Fatalf("marshal ids: %v", err)
	}
	rule := &model.RoutingRule{
		Enable: true, Remark: "google direct",
		IngressScope: model.RoutingScopeSelected, IngressIds: string(ids),
		DestKind: model.RoutingDestDirect,
		Criteria: `{"domain":["google.com"]}`,
	}
	if err := database.GetDB().Create(rule).Error; err != nil {
		t.Fatalf("seed routing rule: %v", err)
	}
}

func seedRoutedInbound(t *testing.T, tag string, port int, sniffing string) *model.Inbound {
	t.Helper()
	inbound := &model.Inbound{
		UserId: 1, Tag: tag, Enable: true, Port: port,
		Protocol: model.VLESS, Settings: `{"clients":[],"decryption":"none"}`,
		Sniffing: sniffing,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	return inbound
}

func planInto(t *testing.T, inbounds ...xray.InboundConfig) *xray.Config {
	t.Helper()
	cfg := &xray.Config{
		RouterConfig:    json_util.RawMessage(`{"rules":[]}`),
		OutboundConfigs: json_util.RawMessage(`[{"protocol":"freedom","tag":"direct"},{"protocol":"blackhole","tag":"blocked"}]`),
		InboundConfigs:  inbounds,
	}
	if err := applyRoutingPlan(context.Background(), cfg, false); err != nil {
		t.Fatalf("applyRoutingPlan: %v", err)
	}
	return cfg
}

func sniffingOf(t *testing.T, cfg *xray.Config, tag string) string {
	t.Helper()
	for _, inbound := range cfg.InboundConfigs {
		if inbound.Tag == tag {
			return string(inbound.Sniffing)
		}
	}
	t.Fatalf("inbound %q is not in the generated config", tag)
	return ""
}

func TestADomainRuleTurnsSniffingOnForItsInbound(t *testing.T) {
	initTestDB(t)
	inbound := seedRoutedInbound(t, "vless-in", 46410, "")
	seedDomainRule(t, inbound.Id)

	cfg := planInto(t, xray.InboundConfig{Tag: "vless-in", Port: 46410, Protocol: "vless"})

	got := sniffingOf(t, cfg, "vless-in")
	if got == "" {
		t.Fatal("a domain rule left its inbound without a sniffing block; Xray reads no name and the rule never matches")
	}
	if !strings.Contains(got, `"routeOnly":true`) {
		t.Fatalf("the sniffing block does not set routeOnly, so the dial target is rewritten to the sniffed name and the front's private-address guard has no IP to match: %s", got)
	}
	for _, protocol := range []string{"tls", "http", "quic"} {
		if !strings.Contains(got, `"`+protocol+`"`) {
			t.Errorf("the sniffing block does not read %s; a browser speaks it to the very sites these rules name: %s", protocol, got)
		}
	}
}

// An address rule needs no name, and sniffing every connection for an answer no
// rule reads costs a cached reader on each one.
func TestAnAddressRuleLeavesSniffingAlone(t *testing.T) {
	initTestDB(t)
	inbound := seedRoutedInbound(t, "vless-addr", 46411, "")
	ids, _ := json.Marshal([]int{inbound.Id})
	rule := &model.RoutingRule{
		Enable: true, Remark: "by address",
		IngressScope: model.RoutingScopeSelected, IngressIds: string(ids),
		DestKind: model.RoutingDestDirect, Criteria: `{"ip":["8.8.8.8"]}`,
	}
	if err := database.GetDB().Create(rule).Error; err != nil {
		t.Fatalf("seed rule: %v", err)
	}

	cfg := planInto(t, xray.InboundConfig{Tag: "vless-addr", Port: 46411, Protocol: "vless"})
	if got := sniffingOf(t, cfg, "vless-addr"); got != "" {
		t.Fatalf("an address rule turned sniffing on: %s", got)
	}
}

/*
An inbound the operator already configured keeps their block.

Theirs may exclude domains, carry fakedns or deliberately rewrite the target.
Replacing it would answer a question they had already answered -- and the panel
never touches the stored row either, only what it generates.
*/
func TestAnInboundThatAlreadySniffsKeepsItsOwnBlock(t *testing.T) {
	initTestDB(t)
	inbound := seedRoutedInbound(t, "vless-own", 46412, "")
	seedDomainRule(t, inbound.Id)

	const own = `{"enabled":true,"destOverride":["tls"],"domainsExcluded":["internal.example.com"]}`
	cfg := planInto(t, xray.InboundConfig{
		Tag: "vless-own", Port: 46412, Protocol: "vless",
		Sniffing: json_util.RawMessage(own),
	})

	if got := sniffingOf(t, cfg, "vless-own"); got != own {
		t.Fatalf("the operator's own sniffing block was replaced:\n got: %s\nwant: %s", got, own)
	}
}

// And an inbound with sniffing explicitly turned OFF is still made to read the
// name, because the rule is the intent -- but only in the generated config.
func TestSniffingDisabledOnTheRowIsStillHonouredForTheRule(t *testing.T) {
	initTestDB(t)
	inbound := seedRoutedInbound(t, "vless-off", 46413, "")
	seedDomainRule(t, inbound.Id)

	cfg := planInto(t, xray.InboundConfig{
		Tag: "vless-off", Port: 46413, Protocol: "vless",
		Sniffing: json_util.RawMessage(`{"enabled":false,"destOverride":["tls"]}`),
	})

	if got := sniffingOf(t, cfg, "vless-off"); !strings.Contains(got, `"enabled":true`) {
		t.Fatalf("a domain rule on an inbound with sniffing off never matches: %s", got)
	}

	var stored model.Inbound
	if err := database.GetDB().First(&stored, inbound.Id).Error; err != nil {
		t.Fatalf("reload the inbound: %v", err)
	}
	if stored.Sniffing != "" {
		t.Fatalf("the stored row was rewritten: %s", stored.Sniffing)
	}
}
