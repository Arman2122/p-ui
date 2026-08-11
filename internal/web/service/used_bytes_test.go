package service

import (
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"

	"gorm.io/gorm"
)

/*
The historical depletion predicate, kept verbatim as the thing the rewrite must
still agree with on every row.

It answers the quota half with EXISTS rather than with a usage value, which is
why a tier pass could not bind it and why "used" was about to be defined twice.
Placeholders: now, freshSince.
*/
const historicalDepletedCond = `((total > 0 AND up + down >= total)
	OR (expiry_time > 0 AND expiry_time <= ?)
	OR (total > 0 AND EXISTS (
		SELECT 1 FROM client_global_traffics g
		WHERE g.email = client_traffics.email
			AND g.updated_at >= ?
			AND g.up + g.down >= client_traffics.total
	)))`

// TestUsedBytesHasOneDefinition pins the repair that matters most: the number a
// tier is evaluated against is the same number a quota is enforced against.
func TestUsedBytesHasOneDefinition(t *testing.T) {
	branches := []struct {
		name      string
		used      string
		usedArgs  int
		depletion string
	}{
		{"local only", usedBytesLocal, 0, depletedClientsCondLocal},
		{"cross panel", usedBytesCrossPanel, 1, depletedClientsCond},
	}

	for _, branch := range branches {
		t.Run(branch.name, func(t *testing.T) {
			if !strings.Contains(branch.depletion, branch.used) {
				t.Fatalf("the depletion predicate does not embed the usage expression verbatim.\npredicate: %s\nusage:     %s\n"+
					"a second copy of 'used' is how a client ends up depleted at quota but never throttled at a tier", branch.depletion, branch.used)
			}
			if got, want := strings.Count(branch.used, "?"), branch.usedArgs; got != want {
				t.Errorf("the usage expression binds %d placeholders, the branch binds %d arguments for it", got, want)
			}
			// One beyond the usage expression's own: the expiry comparison.
			if got, want := strings.Count(branch.depletion, "?"), branch.usedArgs+1; got != want {
				t.Errorf("the depletion predicate binds %d placeholders, want %d", got, want)
			}
		})
	}
}

// TestUsedBytesExprAndDepletionPickTheSameBranch drives both callers against a
// real database: agreeing on the text is worthless if the probe disagrees about
// which text to use on the same panel in the same pass.
func TestUsedBytesExprAndDepletionPickTheSameBranch(t *testing.T) {
	db := initTrafficTestDB(t)
	svc := &InboundService{}

	assertAgrees := func(t *testing.T, wantUsed string) {
		t.Helper()
		used, usedArgs := UsedBytesExpr(db)
		if used != wantUsed {
			t.Fatalf("UsedBytesExpr chose\n%s\nwant\n%s", used, wantUsed)
		}
		cond, condArgs := depletedCond(db)
		if !strings.Contains(cond, used) {
			t.Fatalf("depletedCond bound a different usage expression than UsedBytesExpr:\n%s\nvs\n%s", cond, used)
		}
		if len(condArgs) != len(usedArgs)+1 {
			t.Fatalf("depletedCond bound %d args around a usage expression binding %d", len(condArgs), len(usedArgs))
		}
		// Each caller samples the clock for itself, so the cutoffs differ by the
		// milliseconds between calls; what must match is the window they name.
		for i := range usedArgs {
			depletion, ok := condArgs[i].(int64)
			if !ok {
				t.Fatalf("argument %d of the depletion predicate is %T, want the freshness cutoff", i, condArgs[i])
			}
			tier, ok := usedArgs[i].(int64)
			if !ok {
				t.Fatalf("argument %d of the usage expression is %T, want the freshness cutoff", i, usedArgs[i])
			}
			if drift := depletion - tier; drift < 0 || drift > time.Second.Milliseconds() {
				t.Fatalf("argument %d: depletion binds %d and the tier pass binds %d, %dms apart — they are naming different freshness windows",
					i, depletion, tier, drift)
			}
			if want := globalTrafficFreshWindow.Milliseconds(); time.Now().UnixMilli()-depletion < want {
				t.Fatalf("argument %d is %d, which is inside the last %v — the cutoff must be the far edge of the freshness window",
					i, depletion, globalTrafficFreshWindow)
			}
		}
	}

	t.Run("no master pushes here", func(t *testing.T) {
		assertAgrees(t, usedBytesLocal)
	})

	t.Run("a master still pushes here", func(t *testing.T) {
		seedClientRow(t, "shared", 1, 10, 10, 1000)
		if err := svc.AcceptGlobalTraffic("master-a", []*core.ClientTraffic{{Email: "shared", Up: 1, Down: 1}}); err != nil {
			t.Fatalf("AcceptGlobalTraffic: %v", err)
		}
		assertAgrees(t, usedBytesCrossPanel)
	})
}

/*
TestDepletionRewriteMatchesTheHistoricalPredicate is the behaviour-preservation
proof for the EXISTS -> GREATEST rewrite.

Both predicates run over the same seeded rows and must select the same emails.
The rows straddle every boundary the two forms could disagree on: local-only
over quota, cross-panel-only over quota, an unmetered client, and a client whose
combined usage lands exactly on its quota.
*/
func TestDepletionRewriteMatchesTheHistoricalPredicate(t *testing.T) {
	db := initTrafficTestDB(t)
	svc := &InboundService{}

	rows := []struct {
		email      string
		up, down   int64
		total      int64
		globalUp   int64
		globalDown int64
	}{
		{email: "under-both", up: 10, down: 10, total: 1000, globalUp: 20, globalDown: 20},
		{email: "over-locally", up: 600, down: 600, total: 1000, globalUp: 1, globalDown: 1},
		{email: "over-only-across-panels", up: 10, down: 10, total: 1000, globalUp: 600, globalDown: 600},
		{email: "exactly-at-quota-across-panels", up: 10, down: 10, total: 1000, globalUp: 500, globalDown: 500},
		{email: "one-byte-short-across-panels", up: 10, down: 10, total: 1000, globalUp: 500, globalDown: 499},
		{email: "unmetered", up: 9000, down: 9000, total: 0, globalUp: 9000, globalDown: 9000},
	}
	for _, row := range rows {
		seedClientRow(t, row.email, 1, row.up, row.down, row.total)
		if err := svc.AcceptGlobalTraffic("master-a", []*core.ClientTraffic{
			{Email: row.email, Up: row.globalUp, Down: row.globalDown},
		}); err != nil {
			t.Fatalf("AcceptGlobalTraffic(%s): %v", row.email, err)
		}
	}

	selected := func(t *testing.T, cond string, args []any) []string {
		t.Helper()
		var emails []string
		if err := db.Model(core.ClientTraffic{}).Where(cond, args...).Order("email").Pluck("email", &emails).Error; err != nil {
			t.Fatalf("select with %s: %v", cond, err)
		}
		return emails
	}

	cond, condArgs := depletedCond(db)
	if cond != depletedClientsCond {
		t.Fatalf("the seeded globals should have selected the cross-panel predicate, got %s", cond)
	}
	// The historical form binds now first; the rewrite folds freshSince into the
	// usage expression, so it binds them the other way round.
	historicalArgs := []any{condArgs[1], condArgs[0]}

	got := selected(t, cond, condArgs)
	want := selected(t, historicalDepletedCond, historicalArgs)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the rewritten predicate selects %v, the historical one selects %v", got, want)
	}
	if len(want) == 0 {
		t.Fatal("neither predicate selected a row; this comparison is certifying nothing")
	}
}

// TestUsedBytesExprReadsAsAValue proves the tier half can actually bind the
// expression: an EXISTS predicate cannot be selected, which is why it was
// rewritten rather than shared as it stood.
func TestUsedBytesExprReadsAsAValue(t *testing.T) {
	db := initTrafficTestDB(t)
	svc := &InboundService{}
	seedClientRow(t, "climber", 1, 100, 200, 1000)
	if err := svc.AcceptGlobalTraffic("master-a", []*core.ClientTraffic{{Email: "climber", Up: 4000, Down: 1000}}); err != nil {
		t.Fatalf("AcceptGlobalTraffic: %v", err)
	}

	read := func(t *testing.T, tx *gorm.DB) int64 {
		t.Helper()
		used, args := UsedBytesExpr(tx)
		var out int64
		if err := tx.Model(core.ClientTraffic{}).
			Select(used+" AS used", args...).
			Where("email = ?", "climber").
			Scan(&out).Error; err != nil {
			t.Fatalf("read used bytes: %v", err)
		}
		return out
	}

	if got := read(t, db); got != 5000 {
		t.Errorf("used = %d, want the cross-panel figure 5000; the local counters alone say 300", got)
	}
	if err := database.GetDB().Exec("DELETE FROM client_global_traffics").Error; err != nil {
		t.Fatalf("clear globals: %v", err)
	}
	if got := read(t, db); got != 300 {
		t.Errorf("used = %d, want the local figure 300 once no master pushes here", got)
	}
}
