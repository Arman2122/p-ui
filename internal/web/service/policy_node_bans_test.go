package service

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
)

func seedNode(t *testing.T, name, guid string, port int) *model.Node {
	t.Helper()
	node := &model.Node{Name: name, Address: "127.0.0.1", Port: port, ApiToken: "tok", Enable: true, Status: "online", Guid: guid}
	if err := database.GetDB().Create(node).Error; err != nil {
		t.Fatalf("seed node %s: %v", name, err)
	}
	return node
}

func seedClient(t *testing.T, email string) {
	t.Helper()
	if err := database.GetDB().Create(&model.ClientRecord{Email: email, Enable: true}).Error; err != nil {
		t.Fatalf("seed client %s: %v", email, err)
	}
}

// seedNodeInbound is an inbound a node serves, attached to clients that exist.
func seedNodeInbound(t *testing.T, node *model.Node, kind core.Kind, tag string, port int, emails ...string) *model.Inbound {
	t.Helper()
	inbound := seedPolicyInbound(t, kind, tag, port)
	if err := database.GetDB().Model(&model.Inbound{}).Where("id = ?", inbound.Id).Update("node_id", node.Id).Error; err != nil {
		t.Fatalf("move inbound %s to node: %v", tag, err)
	}
	inbound.NodeID = &node.Id
	for _, email := range emails {
		attachClient(t, inbound, email)
	}
	return inbound
}

// reportFromNode is the sync's merge of one node's sightings, which is also
// what fills the registry the master's pass asks.
func reportFromNode(t *testing.T, node *model.Node, email string, entries ...model.ClientIpEntry) {
	t.Helper()
	trees := map[string]map[string][]model.ClientIpEntry{effectiveNodeKey(node): {email: entries}}
	if err := (&InboundService{}).MergeClientIpsByGuid(node, trees); err != nil {
		t.Fatalf("merge %s's report: %v", node.Name, err)
	}
	t.Cleanup(nodeSightings.Reset)
}

func sighting(ip string, first, last time.Time) model.ClientIpEntry {
	return model.ClientIpEntry{IP: ip, Timestamp: last.Unix(), FirstSeen: first.Unix()}
}

/*
A device on a node counts against the cap like one here, and the ban for it is
the node's: on the node's port for the client, never on this host, whose
firewall the node's traffic never crosses.
*/
func TestADeviceOnANodeCountsAgainstTheCapAndIsBannedThere(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	plane := installBanPlane(t)
	svc := &PolicyService{}
	seen := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(seen))
	seedPolicyInbound(t, "vless", "vless-here", 45840, "capped")
	node := seedNode(t, "n-away", "node-away", 2096)
	seedNodeInbound(t, node, "vless", "vless-away", 45841, "capped")
	setLimitIP(t, "capped", 1)
	now := time.Now()
	seen.sessions = observe("capped", now.Add(-10*time.Minute), "198.51.100.1")
	reportFromNode(t, node, "capped", sighting("203.0.113.20", now.Add(-2*time.Minute), now.Add(-5*time.Second)))

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	verdict := verdictFor(t, verdicts, "capped")
	if got := overAddresses(verdicts, "capped"); !slices.Equal(got, []string{"203.0.113.20"}) {
		t.Fatalf("over %v, want the node's device: it came second", got)
	}
	if len(verdict.Ban) != 0 {
		t.Fatalf("banned here: %v; this host's firewall never sees the node's traffic", banAddresses(verdict))
	}
	want := []IPLimitNodeBan{{Source: "203.0.113.20", NodeID: node.Id, Guid: "node-away", Ports: []uint16{45841}}}
	if !reflect.DeepEqual(verdict.NodeBans, want) {
		t.Fatalf("node bans %+v, want %+v", verdict.NodeBans, want)
	}

	created, err := svc.RecordBans(context.Background(), verdicts, now)
	if err != nil {
		t.Fatalf("RecordBans: %v", err)
	}
	if len(created) != 1 || created[0].NodeGuid != "node-away" || created[0].Ports != "45841" {
		t.Fatalf("recorded %+v, want one ban held for node-away on 45841", created)
	}
	if rows, err := svc.BansForNode(context.Background(), node, now); err != nil || len(rows) != 1 {
		t.Fatalf("the node's push set: %+v (%v), want the one ban", rows, err)
	}
	if err := svc.ConvergeBans(context.Background(), now); err != nil {
		t.Fatalf("ConvergeBans: %v", err)
	}
	if strings.Contains(plane.last(), "203.0.113.20") {
		t.Fatalf("a node's ban reached this host's firewall:\n%s", plane.last())
	}
}

// A client no local core ever sees is still decided here, from what its nodes
// reported, and the node that saw the extra device is the one handed the ban.
func TestAClientServedOnlyByNodesIsDecidedOnTheMaster(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	installBanPlane(t)
	svc := &PolicyService{}
	installCores(t, newProxyCore(&declaredCore{id: "proxy", kind: "vless"}))
	seedClient(t, "roamer")
	a := seedNode(t, "n-a", "node-a", 2096)
	b := seedNode(t, "n-b", "node-b", 2097)
	seedNodeInbound(t, a, "vless", "vless-a", 45851, "roamer")
	seedNodeInbound(t, b, "vless", "vless-b", 45852, "roamer")
	setLimitIP(t, "roamer", 1)
	now := time.Now()
	reportFromNode(t, a, "roamer", sighting("203.0.113.1", now.Add(-10*time.Minute), now.Add(-5*time.Second)))
	reportFromNode(t, b, "roamer", sighting("203.0.113.2", now.Add(-time.Minute), now.Add(-5*time.Second)))

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	verdict := verdictFor(t, verdicts, "roamer")
	if got := overAddresses(verdicts, "roamer"); !slices.Equal(got, []string{"203.0.113.2"}) {
		t.Fatalf("over %v, want the later arrival on node-b", got)
	}
	want := []IPLimitNodeBan{{Source: "203.0.113.2", NodeID: b.Id, Guid: "node-b", Ports: []uint16{45852}}}
	if !reflect.DeepEqual(verdict.NodeBans, want) {
		t.Fatalf("node bans %+v, want %+v", verdict.NodeBans, want)
	}
	if _, err := svc.RecordBans(context.Background(), verdicts, now); err != nil {
		t.Fatalf("RecordBans: %v", err)
	}
	if rows, _ := svc.BansForNode(context.Background(), a, now); len(rows) != 0 {
		t.Fatalf("node-a was handed a ban it did not earn: %+v", rows)
	}
	if rows, _ := svc.BansForNode(context.Background(), b, now); len(rows) != 1 || rows[0].Source != "203.0.113.2" {
		t.Fatalf("node-b's push set: %+v, want the one ban of 203.0.113.2", rows)
	}
}

// What this host files as its own sightings is what its own cores saw; a
// node's device is the node's to attribute, or the panel would show it here.
func TestTheMastersAttributionIsItsOwnSightingsOnly(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	seen := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(seen))
	seedPolicyInbound(t, "vless", "vless-here", 45840, "capped")
	node := seedNode(t, "n-away", "node-away", 2096)
	seedNodeInbound(t, node, "vless", "vless-away", 45841, "capped")
	now := time.Now()
	seen.sessions = observe("capped", now.Add(-10*time.Minute), "198.51.100.1")
	reportFromNode(t, node, "capped", sighting("203.0.113.20", now.Add(-2*time.Minute), now.Add(-5*time.Second)))

	if _, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background())); err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	localGuid, _ := (&SettingService{}).GetPanelGuid()
	var mine []model.NodeClientIp
	if err := database.GetDB().Where("node_guid = ? AND email = ?", localGuid, "capped").Find(&mine).Error; err != nil {
		t.Fatalf("read attribution: %v", err)
	}
	if len(mine) != 1 || !strings.Contains(mine[0].Ips, "198.51.100.1") {
		t.Fatalf("this host's own sighting was not filed: %+v", mine)
	}
	if strings.Contains(mine[0].Ips, "203.0.113.20") {
		t.Fatalf("the node's device was filed as this host's own sighting: %s", mine[0].Ips)
	}
	attribution, err := (&InboundService{}).GetClientIpNodeAttribution("capped")
	if err != nil || attribution["203.0.113.20"] != "node-away" {
		t.Fatalf("attribution %v (%v), want the node's device on node-away", attribution, err)
	}
}

// An older node records no arrival. Its device then arrived when it was last
// seen, the start a held address always had, and not now, which would make
// every device of an older node the newest and the one that always yields.
func TestAnOlderNodesDeviceKeepsItsArrival(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	seen := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(seen))
	seedPolicyInbound(t, "vless", "vless-here", 45840, "capped")
	node := seedNode(t, "n-old", "node-old", 2096)
	seedNodeInbound(t, node, "vless", "vless-old", 45841, "capped")
	setLimitIP(t, "capped", 1)
	now := time.Now()
	seen.sessions = observe("capped", now.Add(-30*time.Second), "198.51.100.1")
	reportFromNode(t, node, "capped", model.ClientIpEntry{IP: "203.0.113.30", Timestamp: now.Add(-60 * time.Second).Unix()})

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if got := overAddresses(verdicts, "capped"); !slices.Equal(got, []string{"198.51.100.1"}) {
		t.Fatalf("over %v, want the local device: the node's was seen first, arrival or no arrival", got)
	}
	if verdict := verdictFor(t, verdicts, "capped"); len(verdict.NodeBans) != 0 {
		t.Fatalf("the node was handed a ban for the device holding the slot: %+v", verdict.NodeBans)
	}
}

// A login core on a node refuses the extra device itself, counting the cluster
// through the union rows; a firewall ban pushed for it would be the NAT
// collateral the login shape exists to avoid.
func TestALoginCoreOnANodeIsLeftToItsOwnAdmission(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	installCores(t, newProxyCore(&declaredCore{id: "proxy", kind: "vless"}), newDaemonCore(&declaredCore{id: "sshd", kind: "ssh"}))
	seedClient(t, "shell")
	node := seedNode(t, "n-shell", "node-shell", 2096)
	seedNodeInbound(t, node, "ssh", "ssh-away", 45861, "shell")
	setLimitIP(t, "shell", 1)
	now := time.Now()
	reportFromNode(t, node, "shell",
		sighting("203.0.113.40", now.Add(-10*time.Minute), now.Add(-5*time.Second)),
		sighting("203.0.113.41", now.Add(-time.Minute), now.Add(-5*time.Second)))

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	verdict := verdictFor(t, verdicts, "shell")
	if got := overAddresses(verdicts, "shell"); !slices.Equal(got, []string{"203.0.113.41"}) {
		t.Fatalf("over %v, want the later arrival", got)
	}
	if len(verdict.NodeBans) != 0 || len(verdict.Ban) != 0 {
		t.Fatalf("a login core's device was named for the firewall: node %+v, here %v", verdict.NodeBans, banAddresses(verdict))
	}
}

/*
The node's half. It holds exactly the set its master pushed, drops what the
master no longer sends, and leaves what it decided itself alone: the two
deadlines were set seconds apart, and the node's decision is not the master's
to end. With banning off here, a push holds nothing.
*/
func TestPushedBansAreHeldUntilTheMasterDropsThem(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	plane := installBanPlane(t)
	svc := &PolicyService{}
	seedPolicyInbound(t, "vless", "vless-node", 45871, "alice")
	now := time.Now()
	own := []IPLimitVerdict{{Email: "alice", Ban: []policy.Observation{{IP: "10.0.0.9"}}, BanPorts: []uint16{45871}}}
	if _, err := svc.RecordBans(context.Background(), own, now); err != nil {
		t.Fatalf("RecordBans: %v", err)
	}
	pushed := []model.ClientBan{
		{ClientEmail: "bob", Source: "10.0.0.8", Ports: "45871", Reason: BanReasonOverCap, CreatedAt: now.Unix(), Until: now.Add(30 * time.Minute).Unix()},
		{ClientEmail: "alice", Source: "10.0.0.9", Ports: "45871", Reason: BanReasonOverCap, CreatedAt: now.Unix(), Until: now.Add(2 * time.Hour).Unix()},
	}

	if err := svc.AcceptBans(context.Background(), "master-1", pushed, now); err != nil {
		t.Fatalf("AcceptBans: %v", err)
	}
	rows, err := svc.ActiveBans(context.Background())
	if err != nil || len(rows) != 2 {
		t.Fatalf("after the push: %+v (%v), want the node's own ban and the pushed one", rows, err)
	}
	for _, row := range rows {
		switch row.ClientEmail {
		case "bob":
			if row.Origin != "master-1" {
				t.Fatalf("the pushed ban is not filed under its master: %+v", row)
			}
		case "alice":
			if row.Origin != "" || row.Until != now.Add(30*time.Minute).Unix() {
				t.Fatalf("the node's own decision was overwritten by the push: %+v", row)
			}
		}
	}
	if last := plane.last(); !strings.Contains(last, "10.0.0.8 . 45871") || !strings.Contains(last, "10.0.0.9 . 45871") {
		t.Fatalf("both bans must be dropped:\n%s", last)
	}

	if err := svc.AcceptBans(context.Background(), "master-1", nil, now.Add(5*time.Second)); err != nil {
		t.Fatalf("AcceptBans (empty): %v", err)
	}
	rows, _ = svc.ActiveBans(context.Background())
	if len(rows) != 1 || rows[0].ClientEmail != "alice" {
		t.Fatalf("after the master dropped its set: %+v, want only the node's own ban", rows)
	}
	if last := plane.last(); strings.Contains(last, "10.0.0.8") || !strings.Contains(last, "10.0.0.9 . 45871") {
		t.Fatalf("the firewall was not converged on the drop:\n%s", last)
	}

	t.Setenv("PUI_IP_LIMIT_BANS", "false")
	if err := svc.AcceptBans(context.Background(), "master-1", pushed, now.Add(10*time.Second)); err != nil {
		t.Fatalf("AcceptBans (banning off): %v", err)
	}
	if rows, _ = svc.ActiveBans(context.Background()); len(rows) != 1 {
		t.Fatalf("a push held bans on a node set not to ban: %+v", rows)
	}
}
