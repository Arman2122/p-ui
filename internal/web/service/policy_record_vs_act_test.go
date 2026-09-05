package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// storedAddresses is what /clientIps and both info modals read back.
func storedAddresses(t *testing.T, email string) []string {
	t.Helper()
	var row model.InboundClientIps
	if err := database.GetDB().Where("client_email = ?", email).First(&row).Error; err != nil {
		t.Fatalf("no stored addresses for %s: %v", email, err)
	}
	out := make([]string, 0, 4)
	for _, entry := range decodeClientIPs(row.Ips) {
		out = append(out, entry.IP)
	}
	return out
}

/*
Deciding is not acting. The verdict names who is over the cap and which actuator
reaches them; whether that actuator is present on this host is the job's to
weigh when it carries the verdict out. So the same breach yields the same
verdict on a box with fail2ban and one without — the record an operator reads is
never a function of what the host happens to have installed.
*/
func TestTheVerdictIsMadeWhetherOrNotAnythingCanActOnIt(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	seen := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(seen))
	seedPolicyInbound(t, "vless", "vless-capped", 45501, "capped")
	setLimitIP(t, "capped", 2)

	at := time.Now().Add(-10 * time.Minute)
	seen.sessions = observe("capped", at, "198.51.100.1", "198.51.100.2", "198.51.100.3")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}

	// The record: the latest arrival is over, and the firewall is named for it —
	// the one actuator that reaches a core which can neither admit nor cut.
	verdict := verdictFor(t, verdicts, "capped")
	want := []string{"198.51.100.3"}
	if got := overAddresses(verdicts, "capped"); !slices.Equal(got, want) {
		t.Fatalf("over %v, want %v — the verdict is the record, and it is made whether or not anything can act on it", got, want)
	}
	if got := banAddresses(verdict); !slices.Equal(got, want) {
		t.Fatalf("ban %v, want %v: the verdict names the firewall; the job decides whether one is there", got, want)
	}
	if len(verdict.Cuts) != 0 {
		t.Fatalf("no core here can cut an Xray session, yet a cut was scheduled: %+v", verdict.Cuts)
	}

	// And every live address stays on the stored list, the over-cap one included:
	// it is a connection that is still up, and this list is the one place the
	// panel reports it.
	stored := storedAddresses(t, "capped")
	for _, live := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		if !slices.Contains(stored, live) {
			t.Fatalf("a live address is missing from the stored list %v", stored)
		}
	}
}
