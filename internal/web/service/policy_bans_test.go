package service

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/banset"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
)

type recordingBanPlane struct{ scripts []string }

func (p *recordingBanPlane) Probe(context.Context) error { return nil }

func (p *recordingBanPlane) Apply(_ context.Context, script string) error {
	p.scripts = append(p.scripts, script)
	return nil
}

func (p *recordingBanPlane) last() string {
	if len(p.scripts) == 0 {
		return ""
	}
	return p.scripts[len(p.scripts)-1]
}

func installBanPlane(t *testing.T) *recordingBanPlane {
	t.Helper()
	plane := &recordingBanPlane{}
	t.Cleanup(UseBanManager(banset.NewManager(plane)))
	return plane
}

func setSetting(t *testing.T, key, value string) {
	t.Helper()
	if err := (&SettingService{}).setString(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}

/*
A ban lands on every enabled local inbound the client is on, not only the one
that observed the device: banned on one port, the device would connect to the
client's other inbound and the cap would enforce nothing. A disabled inbound is
not a door, so it is not banned on.
*/
func TestABanCoversEveryEnabledLocalInboundTheClientIsOn(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	seen := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(seen))
	seedPolicyInbound(t, "vless", "vless-first", 45811, "capped")
	attachClient(t, seedPolicyInbound(t, "vless", "vless-second", 45812), "capped")
	disabled := seedPolicyInbound(t, "vless", "vless-disabled", 45813)
	attachClient(t, disabled, "capped")
	if err := database.GetDB().Model(&model.Inbound{}).Where("id = ?", disabled.Id).Update("enable", false).Error; err != nil {
		t.Fatalf("disable inbound: %v", err)
	}
	setLimitIP(t, "capped", 1)
	seen.sessions = observe("capped", time.Now().Add(-10*time.Minute), "198.51.100.1", "198.51.100.2")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	verdict := verdictFor(t, verdicts, "capped")
	if want := []string{"198.51.100.2"}; !slices.Equal(banAddresses(verdict), want) {
		t.Fatalf("ban %v, want %v", banAddresses(verdict), want)
	}
	if want := []uint16{45811, 45812}; !slices.Equal(verdict.BanPorts, want) {
		t.Fatalf("the ban covers ports %v, want %v: every enabled local inbound the client is on, and no disabled one", verdict.BanPorts, want)
	}
}

/*
Behind one NAT, the abuser's address is the neighbour's too. When the operator
chose to skip a shared address, a source another client holds inside their cap
is reported and not banned; when they chose to ban, it is banned like any other.
*/
func TestASharedAddressIsBannedOrSkippedAsTheOperatorChose(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	plane := installBanPlane(t)
	svc := &PolicyService{}
	seen := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(seen))
	seedPolicyInbound(t, "vless", "vless-shared", 45821, "over", "neighbour")
	setLimitIP(t, "over", 1)
	setLimitIP(t, "neighbour", 5)
	at := time.Now().Add(-10 * time.Minute)
	seen.sessions = slices.Concat(observe("over", at, "198.51.100.1", "203.0.113.9"), observe("neighbour", at, "203.0.113.9"))

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if want := []string{"203.0.113.9"}; !slices.Equal(banAddresses(verdictFor(t, verdicts, "over")), want) {
		t.Fatalf("the verdict names %v, want %v", banAddresses(verdictFor(t, verdicts, "over")), want)
	}

	setSetting(t, "ipLimitSharedAddressAction", SharedAddressSkip)
	now := time.Now()
	created, err := svc.RecordBans(context.Background(), verdicts, now)
	if err != nil {
		t.Fatalf("RecordBans: %v", err)
	}
	if len(created) != 1 || created[0].Reason != BanReasonSharedSkipped || created[0].Ports != "" {
		t.Fatalf("skip: %+v, want one report of the pair with no port to drop on: the neighbour holds that address inside their cap", created)
	}
	if err := svc.ConvergeBans(context.Background(), now); err != nil {
		t.Fatalf("ConvergeBans: %v", err)
	}
	if last := plane.last(); strings.Contains(last, "203.0.113.9") {
		t.Fatalf("a reported address reached the firewall:\n%s", last)
	}
	if again, err := svc.RecordBans(context.Background(), verdicts, now.Add(10*time.Second)); err != nil || len(again) != 0 {
		t.Fatalf("the report must be made once, not every pass: %+v, %v", again, err)
	}

	setSetting(t, "ipLimitSharedAddressAction", SharedAddressBan)
	created, err = svc.RecordBans(context.Background(), verdicts, now.Add(20*time.Second))
	if err != nil {
		t.Fatalf("RecordBans: %v", err)
	}
	if len(created) != 1 || created[0].ClientEmail != "over" || created[0].Source != "203.0.113.9" || created[0].Reason != BanReasonOverCap {
		t.Fatalf("ban: %+v, want the report of over from 203.0.113.9 replaced by its ban", created)
	}
	rows, err := svc.ActiveBans(context.Background())
	if err != nil || len(rows) != 1 || rows[0].Ports != "45821" {
		t.Fatalf("after the ban: %+v (%v), want the one row, now with the port", rows, err)
	}
}

// A release is felt now, not at the next pass: the row goes and the firewall is
// converged in the same call.
func TestAReleasedBanLeavesTheFirewallAtOnce(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	plane := installBanPlane(t)
	svc := &PolicyService{}
	now := time.Now()
	verdicts := []IPLimitVerdict{{
		Email: "alice", Ban: []policy.Observation{{IP: "10.0.0.9"}}, BanPorts: []uint16{443, 8443},
	}}
	if _, err := svc.RecordBans(context.Background(), verdicts, now); err != nil {
		t.Fatalf("RecordBans: %v", err)
	}
	if err := svc.ConvergeBans(context.Background(), now); err != nil {
		t.Fatalf("ConvergeBans: %v", err)
	}
	if last := plane.last(); !strings.Contains(last, "10.0.0.9 . 443") || !strings.Contains(last, "10.0.0.9 . 8443") {
		t.Fatalf("both ports must be dropped:\n%s", last)
	}

	released, err := svc.ReleaseBan(context.Background(), "alice", "10.0.0.9", now.Add(time.Second))
	if err != nil {
		t.Fatalf("ReleaseBan: %v", err)
	}
	if released != 1 {
		t.Fatalf("released %d, want 1", released)
	}
	if last := plane.last(); strings.Contains(last, "10.0.0.9") {
		t.Fatalf("the released source is still in the firewall's want:\n%s", last)
	}
}

// An operator's ban is every local inbound port as it stands each pass, so an
// inbound added after the ban is inside it rather than a way around it.
func TestAnOperatorBanCoversAnInboundAddedLater(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	plane := installBanPlane(t)
	svc := &PolicyService{}
	seedPolicyInbound(t, "vless", "vless-before", 45831)
	now := time.Now()

	if _, err := svc.BanAddress(context.Background(), "203.0.113.5", 60, now); err != nil {
		t.Fatalf("BanAddress: %v", err)
	}
	if last := plane.last(); !strings.Contains(last, "203.0.113.5 . 45831") {
		t.Fatalf("the operator's ban did not land on the existing inbound:\n%s", last)
	}

	seedPolicyInbound(t, "vless", "vless-after", 45832)
	if err := svc.ConvergeBans(context.Background(), now.Add(10*time.Second)); err != nil {
		t.Fatalf("ConvergeBans: %v", err)
	}
	if last := plane.last(); !strings.Contains(last, "203.0.113.5 . 45832") {
		t.Fatalf("an inbound added after the ban is outside it:\n%s", last)
	}
}
