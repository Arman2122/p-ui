package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/shaping"
)

// attachClient links an existing client to a SECOND inbound, which is how one
// client comes to be served by two different cores at once.
func attachClient(t *testing.T, inbound *model.Inbound, email string) {
	t.Helper()
	var record model.ClientRecord
	if err := database.GetDB().Where("email = ?", email).First(&record).Error; err != nil {
		t.Fatalf("find client %s: %v", email, err)
	}
	link := &model.ClientInbound{ClientId: record.Id, InboundId: inbound.Id}
	if err := database.GetDB().Create(link).Error; err != nil {
		t.Fatalf("attach %s to inbound %d: %v", email, inbound.Id, err)
	}
}

// verdictFor returns one client's whole verdict, so a test can assert the bounce
// decision and not merely the ban list.
func verdictFor(t *testing.T, verdicts []IPLimitVerdict, email string) IPLimitVerdict {
	t.Helper()
	for _, verdict := range verdicts {
		if verdict.Email == email {
			return verdict
		}
	}
	t.Fatalf("no verdict for %s in %+v", email, verdicts)
	return IPLimitVerdict{}
}

/*
TestACutNeverLandsOnACoreThatSawNothing.

A client on two cores has ONE merged address list, and a cut is per-core: the
runtime resolves the core from the inbound it is handed. Aiming by inbound id
would act on whichever inbound sorts first, independent of the core that
observed the address — so here the cuttable core sorts first, saw only the
device inside the cap, and must be left alone while the addresses over it, all
the tunnel's, go to the only actuator that reaches a tunnel: the firewall.
*/
func TestACutNeverLandsOnACoreThatSawNothing(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	quiet := &declaredCore{id: "daemon", kind: "ssh"}
	loud := &declaredCore{
		id: "tunnel", kind: "wgkernel", selector: core.SelectorInnerIP, device: "pwg7",
	}
	cuttable := newDaemonCore(quiet)
	installCores(t, cuttable, newPeerCore(loud))

	// The ssh inbound is created first, so it holds the LOWER id and is what an
	// id tie-break picks — while every over-cap address comes from the other core.
	sshInbound := seedPolicyInbound(t, "ssh", "ssh-quiet", 45401, "dual")
	tunnelInbound := seedPolicyInbound(t, "wgkernel", "wg-loud", 45402)
	attachClient(t, tunnelInbound, "dual")
	setLimitIP(t, "dual", 2)

	// The ssh device arrived first and holds a slot; the tunnel's three came later.
	at := time.Now().Add(-10 * time.Minute)
	loud.sessions = observe("dual", at, "198.51.100.1", "198.51.100.2", "198.51.100.3")
	quiet.sessions = observe("dual", at.Add(-time.Hour), "203.0.113.9")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if sshInbound.Id >= tunnelInbound.Id {
		t.Fatalf("this only bites while the cuttable inbound sorts first: %d vs %d", sshInbound.Id, tunnelInbound.Id)
	}
	if got, want := overAddresses(verdicts, "dual"), []string{"198.51.100.2", "198.51.100.3"}; !slices.Equal(got, want) {
		t.Fatalf("over %v, want the two latest arrivals %v, every one of them the tunnel's", got, want)
	}
	verdict := verdictFor(t, verdicts, "dual")
	if len(verdict.Cuts) != 0 {
		t.Fatalf("a cut was aimed at inbound %d: the only cuttable core saw none of the over-cap addresses", verdict.Cuts[0].Inbound.Id)
	}
	if got := banAddresses(verdict); !slices.Equal(got, []string{"198.51.100.2", "198.51.100.3"}) {
		t.Fatalf("the tunnel's addresses must go to the firewall, got %v", got)
	}
}

/*
TestACutLandsOnTheCoreThatSawTheBreach is the mirror failure.

With the core that cannot cut sorting first, an id tie-break would find no
cuttable inbound and hand a genuine sharer on the OTHER core to the firewall —
when that core can end the sessions itself, and admits at login, so no ban is due.
*/
func TestACutLandsOnTheCoreThatSawTheBreach(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	quiet := &declaredCore{
		id: "tunnel", kind: "wgkernel", selector: core.SelectorInnerIP, device: "pwg7",
	}
	loud := &declaredCore{id: "daemon", kind: "ssh"}
	installCores(t, newPeerCore(quiet), newDaemonCore(loud))

	tunnelInbound := seedPolicyInbound(t, "wgkernel", "wg-quiet", 45403, "dual")
	sshInbound := seedPolicyInbound(t, "ssh", "ssh-loud", 45404)
	attachClient(t, sshInbound, "dual")
	setLimitIP(t, "dual", 2)

	at := time.Now().Add(-10 * time.Minute)
	loud.sessions = observe("dual", at, "203.0.113.1", "203.0.113.2", "203.0.113.3")
	quiet.sessions = observe("dual", at.Add(-time.Hour), "198.51.100.9")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if tunnelInbound.Id >= sshInbound.Id {
		t.Fatalf("this only bites while the uncuttable inbound sorts first: %d vs %d", tunnelInbound.Id, sshInbound.Id)
	}
	verdict := verdictFor(t, verdicts, "dual")
	if len(verdict.Cuts) != 2 {
		t.Fatalf("the core that observed the over-cap addresses can end them, so both must be cut: %+v", verdict.Cuts)
	}
	for _, cut := range verdict.Cuts {
		if cut.Inbound.Id != sshInbound.Id {
			t.Fatalf("cut aimed at inbound %d, want the observing core's %d", cut.Inbound.Id, sshInbound.Id)
		}
	}
	if len(verdict.Ban) != 0 {
		t.Fatalf("the cutting core admits at login, so nothing is the firewall's, yet %v would be banned", banAddresses(verdict))
	}
}

/*
TestAClientBackUnderItsCapIsStillReported.

The caller's re-ban memory is pruned by being handed the clients that went back
under their cap. Dropping an empty verdict makes that prune unreachable, and a
pair that is never forgotten can never be reported again once fail2ban's own ban
expires — the cap then stops being enforced for the panel's lifetime.
*/
func TestAClientBackUnderItsCapIsStillReported(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	base := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(base))
	seedPolicyInbound(t, "vless", "vless-under", 45405, "behaving")
	setLimitIP(t, "behaving", 2)
	base.sessions = observe("behaving", time.Now().Add(-time.Minute), "203.0.113.50")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	verdict := verdictFor(t, verdicts, "behaving")
	if len(verdict.Over) != 0 {
		t.Fatalf("a client inside its cap must carry an empty over list, got %v", verdict.Over)
	}
	if len(verdict.Cuts) != 0 || len(verdict.Ban) != 0 {
		t.Fatalf("nothing over the cap can justify a cut or a ban, got cuts %+v ban %v", verdict.Cuts, verdict.Ban)
	}
}

/*
TestDeletingAPlanIsReportedAsUnresolved.

The foreign key is ON DELETE SET NULL and not CASCADE for exactly one reason: the
assignment stays visible so the panel can report which clients lost their plan.
Reading an FK-nulled row as "never assigned" throws that away, and the operator
learns from a customer that the throttle stopped.
*/
func TestDeletingAPlanIsReportedAsUnresolved(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	installKernel(t, "pwg7")
	tunnelWithClients(t, "pwg7", "dropped")
	id := seedPlan(t, "doomed", theLadder, "dropped")

	if err := database.GetDB().Model(&core.ClientTraffic{}).Where("email = ?", "dropped").
		Update("up", int64(200<<30)).Error; err != nil {
		t.Fatalf("set usage: %v", err)
	}
	before, err := svc.EnforcedFor(context.Background(), "dropped")
	if err != nil {
		t.Fatalf("EnforcedFor: %v", err)
	}
	if before.WantDownBps != 2_000_000 || before.Unresolved {
		t.Fatalf("before the delete = %+v, want the 2 Mbps tier and a resolved plan", before)
	}

	if err := svc.Del(id); err != nil {
		t.Fatalf("Del: %v", err)
	}
	after, err := svc.EnforcedFor(context.Background(), "dropped")
	if err != nil {
		t.Fatalf("EnforcedFor: %v", err)
	}
	if !after.Unresolved {
		t.Fatalf("after the delete = %+v, want Unresolved: the row survived and now names no plan", after)
	}
	if after.WantDownBps != 0 || after.WantUpBps != 0 {
		t.Fatalf("an unresolvable plan must never throttle, got %+v", after)
	}
}

/*
TestACoreOfferingAForeignDeviceIsNamed.

The mechanism only ever sees a device string, so its refusal names the device and
not the core that offered it. The namespace requirement is a fact about a core's
device naming and belongs in the answer an operator reads.
*/
func TestACoreOfferingAForeignDeviceIsNamed(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	tunnelWithClients(t, "ocserv7", "stranger")

	_, err := svc.ShapingWants(context.Background())
	if !errors.Is(err, shaping.ErrNotOwned) {
		t.Fatalf("ShapingWants = %v, want ErrNotOwned", err)
	}
	if !strings.Contains(err.Error(), "tunnel") || !strings.Contains(err.Error(), "ocserv7") {
		t.Fatalf("the refusal must name the core and the device it offered, got %q", err)
	}
}
