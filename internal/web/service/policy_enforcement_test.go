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
TestABounceNeverLandsOnACoreThatSawNothing.

A client on two cores has ONE merged address list, and cutting them is per-core:
the runtime resolves the core from the inbound it is handed, and RemoveUser drops
every session that core holds. Choosing the inbound by id therefore cuts whichever
inbound sorts first, which is independent of the core that observed the breach.
*/
func TestABounceNeverLandsOnACoreThatSawNothing(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	quiet := &declaredCore{id: "proxy", kind: "vless"}
	loud := &declaredCore{
		id: "tunnel", kind: "wgkernel", selector: core.SelectorInnerIP, device: "pwg7",
	}
	installCores(t, newProxyCore(quiet), newPeerCore(loud))

	// The vless inbound is created first, so it holds the LOWER id and is what an
	// id tie-break picks — while every banned address comes from the other core.
	proxyInbound := seedPolicyInbound(t, "vless", "vless-quiet", 45401, "dual")
	tunnelInbound := seedPolicyInbound(t, "wgkernel", "wg-loud", 45402)
	attachClient(t, tunnelInbound, "dual")
	setLimitIP(t, "dual", 2)

	at := time.Now().Add(-10 * time.Minute)
	loud.sessions = observe("dual", at, "198.51.100.1", "198.51.100.2", "198.51.100.3")
	quiet.sessions = observe("dual", at.Add(time.Hour), "203.0.113.9")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if proxyInbound.Id >= tunnelInbound.Id {
		t.Fatalf("this only bites while the compliant inbound sorts first: %d vs %d", proxyInbound.Id, tunnelInbound.Id)
	}
	if got, want := bannedAddresses(verdicts, "dual"), []string{"198.51.100.1", "198.51.100.2"}; !slices.Equal(got, want) {
		t.Fatalf("banned %v, want the two oldest %v, every one of them the tunnel's", got, want)
	}
	verdict := verdictFor(t, verdicts, "dual")
	if verdict.Bounce {
		t.Fatalf("the bounce landed on inbound %d (%s): the only core that saw a banned address loses its counters when cut",
			verdict.Inbound.Id, verdict.Inbound.Protocol)
	}
}

/*
TestABounceLandsOnTheCoreThatSawTheBreach is the mirror failure.

With the core that cannot safely be cut sorting first, an id tie-break refuses to
cut anybody, and a genuine key-sharer on the OTHER core is never enforced at all.
*/
func TestABounceLandsOnTheCoreThatSawTheBreach(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	quiet := &declaredCore{
		id: "tunnel", kind: "wgkernel", selector: core.SelectorInnerIP, device: "pwg7",
	}
	loud := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newPeerCore(quiet), newProxyCore(loud))

	tunnelInbound := seedPolicyInbound(t, "wgkernel", "wg-quiet", 45403, "dual")
	proxyInbound := seedPolicyInbound(t, "vless", "vless-loud", 45404)
	attachClient(t, proxyInbound, "dual")
	setLimitIP(t, "dual", 2)

	at := time.Now().Add(-10 * time.Minute)
	loud.sessions = observe("dual", at, "203.0.113.1", "203.0.113.2", "203.0.113.3")
	quiet.sessions = observe("dual", at.Add(time.Hour), "198.51.100.9")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if tunnelInbound.Id >= proxyInbound.Id {
		t.Fatalf("this only bites while the unbounceable inbound sorts first: %d vs %d", tunnelInbound.Id, proxyInbound.Id)
	}
	verdict := verdictFor(t, verdicts, "dual")
	if !verdict.Bounce {
		t.Fatal("the core that observed the banned addresses can safely be cut, so the cap must be enforced")
	}
	if verdict.Inbound.Id != proxyInbound.Id {
		t.Fatalf("bounce target = inbound %d, want the observing core's %d", verdict.Inbound.Id, proxyInbound.Id)
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

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	verdict := verdictFor(t, verdicts, "behaving")
	if len(verdict.Ban) != 0 {
		t.Fatalf("a client inside its cap must carry an empty ban list, got %v", verdict.Ban)
	}
	if verdict.Bounce {
		t.Fatal("an empty ban list can never justify cutting a client off")
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
