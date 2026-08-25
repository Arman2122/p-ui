package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The installer's firewall step used to guess, and the guess was wrong by design.

install_firewall allowed ssh, http, https, 2053 and 2096 and then force-enabled
ufw. install.sh assigns the panel port with `shuf -i 1024-62000`, so 2053 is
essentially never the real one, and no inbound port was in the list at all — so
an operator picking "Install Firewall" from the panel's own menu lost the panel
and every client at once.

This is the answer the script reads instead. Its format is an interface: one
record per line, "<port>/<proto> <role>", stable field order.
*/
func TestTheFirewallReportNamesEveryPortThisHostBinds(t *testing.T) {
	initTestDB(t)
	setting := &SettingService{}
	if err := setting.SetPort(2053); err != nil {
		t.Fatal(err)
	}
	if err := setting.setInt("subPort", 2096); err != nil {
		t.Fatal(err)
	}
	if err := setting.setBool("subEnable", true); err != nil {
		t.Fatal(err)
	}

	seedFirewallInbound(t, "vless-on", 443, model.VLESS, true, `{"clients":[],"decryption":"none"}`)
	seedFirewallInbound(t, "wg-off", 51820, model.WGKernel, false, `{"clients":[]}`)

	report := firewallReportLines(t)

	for _, want := range []string{
		"2053/tcp panel",
		"2096/tcp subscription",
		"443/tcp inbound",
		// Included on purpose: an allowed port with no listener answers nothing,
		// while leaving it closed means enabling the inbound next month is eaten
		// by a firewall nothing re-runs.
		"51820/udp inbound-disabled",
	} {
		if !containsLine(report, want) {
			t.Fatalf("the report is missing %q:\n%s", want, strings.Join(report, "\n"))
		}
	}
}

// A node's inbounds are served by the node's own process, so opening their
// ports here protects nothing and advertises a service that is not present.
func TestTheFirewallReportExcludesANodesInbounds(t *testing.T) {
	initTestDB(t)
	nodeID := 7
	inbound := &model.Inbound{
		UserId: 1, Tag: "remote", Enable: true, Port: 8443, NodeID: &nodeID,
		Protocol: model.VLESS, Settings: `{"clients":[],"decryption":"none"}`,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatal(err)
	}

	for _, line := range firewallReportLines(t) {
		if strings.HasPrefix(line, "8443/") {
			t.Fatalf("a node's inbound port is in the report: %q", line)
		}
	}
}

// PUI_PORT is what the panel binds when it is set, so it is what must be open.
func TestTheFirewallReportFollowsThePortOverride(t *testing.T) {
	initTestDB(t)
	if err := (&SettingService{}).SetPort(2053); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUI_PORT", "8443")

	report := firewallReportLines(t)
	if !containsLine(report, "8443/tcp panel") {
		t.Fatalf("the report does not name the port the panel really binds:\n%s", strings.Join(report, "\n"))
	}
	if containsLine(report, "2053/tcp panel") {
		t.Fatalf("the report opens the overridden setting, which nothing listens on:\n%s", strings.Join(report, "\n"))
	}
}

// The shape the shell parses, pinned: two fields, port/proto then role.
func TestTheFirewallReportLineShapeIsStable(t *testing.T) {
	line := FirewallPort{Port: 443, Proto: "tcp", Role: "inbound"}.String()
	if line != "443/tcp inbound" {
		t.Fatalf("line = %q; the installer reads this with `while read spec role`", line)
	}
	if fields := strings.Fields(line); len(fields) != 2 {
		t.Fatalf("line has %d fields, want 2 — adding one is a breaking change to the script", len(fields))
	}
}

func firewallReportLines(t *testing.T) []string {
	t.Helper()
	ports, err := (&InboundService{}).FirewallPorts()
	if err != nil {
		t.Fatalf("FirewallPorts: %v", err)
	}
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		out = append(out, p.String())
	}
	return out
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

func seedFirewallInbound(t *testing.T, tag string, port int, protocol model.Protocol, enable bool, settings string) {
	t.Helper()
	inbound := &model.Inbound{UserId: 1, Tag: tag, Port: port, Protocol: protocol, Settings: settings}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed %s: %v", tag, err)
	}
	// Enable carries a database-side default, so it is written explicitly (L9).
	if err := database.GetDB().Model(&model.Inbound{}).
		Where("id = ?", inbound.Id).Update("enable", enable).Error; err != nil {
		t.Fatal(err)
	}
}
