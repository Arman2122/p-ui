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
Deciding is not acting, and the actuator's absence used to suppress both.

`enforce` is AnyClientHasAnIPLimit() && fail2BanIsInstalled(). When it was false
the loop skipped past policy.Decide entirely, so a panel with no fail2ban kept a
record of which addresses had been seen and none at all of who had exceeded their
cap — the one thing the feature exists to tell an operator.

The pair below is the whole statement: the same breach produces the same verdict
either way, and `enforce` decides only whether anything acts on it.
*/
func TestTheVerdictIsMadeWhetherOrNotAnythingCanActOnIt(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enforce    bool
		wantBounce bool
	}{
		{name: "no actuator on this host", enforce: false},
		{name: "fail2ban present and limits configured", enforce: true, wantBounce: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initTestDB(t)
			svc := &PolicyService{}

			seen := &declaredCore{id: "proxy", kind: "vless"}
			installCores(t, newProxyCore(seen))
			seedPolicyInbound(t, "vless", "vless-capped", 45501, "capped")
			setLimitIP(t, "capped", 2)

			at := time.Now().Add(-10 * time.Minute)
			seen.sessions = observe("capped", at, "198.51.100.1", "198.51.100.2", "198.51.100.3")

			verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), tc.enforce)
			if err != nil {
				t.Fatalf("EvaluateIPLimits: %v", err)
			}

			// The record: same breach, same address over the cap, either way.
			want := []string{"198.51.100.1"}
			if got := bannedAddresses(verdicts, "capped"); !slices.Equal(got, want) {
				t.Fatalf("banned %v, want %v — the verdict is the record, and it is made whether or not anything can act on it", got, want)
			}

			// The act: gated, and now the only thing that is.
			verdict := verdictFor(t, verdicts, "capped")
			if verdict.Bounce != tc.wantBounce {
				t.Fatalf("Bounce = %v, want %v: enforce decides whether the ban is carried out, nothing else",
					verdict.Bounce, tc.wantBounce)
			}
			if tc.wantBounce && verdict.Inbound == nil {
				t.Fatal("a bounce was decided with no inbound to land it on")
			}
			if !tc.wantBounce && verdict.Inbound != nil {
				t.Fatalf("nothing can act, but an inbound was still chosen to act on: %d", verdict.Inbound.Id)
			}

			/*
				And the address the cap named: excluded from the stored list when it is
				about to be cut, kept when nothing will cut it. Hiding a connection that
				is still up would empty the list for exactly the clients it is about.
			*/
			stored := storedAddresses(t, "capped")
			if got := slices.Contains(stored, "198.51.100.1"); got == tc.enforce {
				t.Fatalf("the over-cap address is %s the stored list %v; with enforce=%v it should be the other way",
					map[bool]string{true: "in", false: "missing from"}[got], stored, tc.enforce)
			}
			for _, kept := range []string{"198.51.100.2", "198.51.100.3"} {
				if !slices.Contains(stored, kept) {
					t.Fatalf("an address inside the cap is missing from the stored list %v", stored)
				}
			}
		})
	}
}
