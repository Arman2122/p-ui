package service

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"

	"gorm.io/gorm"
)

/*
Two saves cannot both claim the same port.

checkPortConflict reads committed rows and its caller commits later, with
nothing holding the gap. Two concurrent creates on one port therefore both read
"free" and both commit. The database does not stop it -- there is no unique
index on (port, listen) and could not be one, because the conflict rule is
transport-aware -- so the panel keeps both rows and disambiguates the TAG, which
hides the collision behind a plausible-looking name.

Xray then refuses to start the second listener and the whole core is down, for a
config the panel reported saving successfully. Two admins on one panel, or one
admin double-clicking Save, is all it takes.

The barrier below makes the race deterministic rather than hoping to hit a
window: the first port-conflict query blocks until a second arrives. Without the
lock both arrive and proceed together. With it, the second cannot even issue its
query until the first has committed, so the barrier times out, the winner
commits, and the loser is refused by the ordinary message. F032.
*/
func TestInboundPortClaimIsExclusiveAcrossConcurrentCreates(t *testing.T) {
	setupConflictDB(t)

	const port = 31000

	// Release the first arrival once a second shows up, or after a short wait if
	// the fix is in place and no second arrival is possible.
	var mu sync.Mutex
	arrived := 0
	second := make(chan struct{})
	barrier := func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Table != "inbounds" {
			return
		}
		if !strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "port") {
			return
		}
		mu.Lock()
		arrived++
		n := arrived
		mu.Unlock()
		if n == 1 {
			select {
			case <-second:
			case <-time.After(1500 * time.Millisecond):
			}
			return
		}
		if n == 2 {
			close(second)
		}
	}

	db := database.GetDB()
	if err := db.Callback().Query().After("gorm:query").Register("f032_barrier", barrier); err != nil {
		t.Fatalf("register barrier: %v", err)
	}
	t.Cleanup(func() {
		_ = database.GetDB().Callback().Query().After("gorm:query").Remove("f032_barrier")
	})

	svc := &InboundService{}
	mk := func(tag string) *model.Inbound {
		return &model.Inbound{
			Tag: tag, Enable: true, Listen: "0.0.0.0", Port: port,
			Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`,
			Settings: `{"clients":[],"decryption":"none"}`,
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, tag := range []string{"race-a", "race-b"} {
		wg.Add(1)
		go func(i int, tag string) {
			defer wg.Done()
			_, _, _, errs[i] = svc.AddInbound(mk(tag))
		}(i, tag)
	}
	wg.Wait()

	var live []model.Inbound
	if err := database.GetDB().Where("port = ?", port).Find(&live).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(live) != 1 {
		tags := make([]string, 0, len(live))
		for _, in := range live {
			tags = append(tags, in.Tag)
		}
		t.Fatalf("%d inbounds committed on port %d (%v); xray refuses to start the second listener and the whole core goes down, on a save the panel reported as successful",
			len(live), port, tags)
	}

	// The loser must be told why, not silently renamed.
	refused := 0
	for _, err := range errs {
		if err == nil {
			continue
		}
		refused++
		if !strings.Contains(strings.ToLower(err.Error()), "already used") {
			t.Errorf("the losing save failed for the wrong reason: %v", err)
		}
	}
	if refused != 1 {
		t.Errorf("want exactly one save refused, got %d (errors: %v)", refused, errs)
	}
}
