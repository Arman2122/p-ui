package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// deferredCore applies an edit on its next pass, the way a core with no hot
// user add must: sing-box rewrites and reloads, coalesced every ten seconds.
type deferredCore struct{ stubCore }

func (c deferredCore) ApplyDelay(kind core.Kind) time.Duration {
	if kind == c.kinds[0] {
		return 10 * time.Second
	}
	return 0
}

/*
A core that cannot apply an edit at once had no way to say so: the panel
answered "saved" and the client could not connect for up to ten seconds, which
reads as a broken save. The contract now carries the delay, the core view
shows it per kind, and a save on such an inbound tells the operator. Roster
contract gap eight, queued dispatch, ahead of the sing-box core.
*/
func TestApplyDelayReachesTheViewAndTheSave(t *testing.T) {
	initTestDB(t)
	reg, err := cores.Default(cores.Deps{})
	if err != nil {
		t.Fatalf("default registry: %v", err)
	}
	stub := deferredCore{stubCore{descriptor: core.Descriptor{ID: "slowcore", TitleKey: "cores.slow.title"}, kinds: []core.Kind{"slowkind"}}}
	if err := reg.Register(stub); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	cores.Use(reg)

	if got := cores.ApplyDelayFor("slowkind"); got != 10*time.Second {
		t.Fatalf("ApplyDelayFor(slowkind) = %s, want 10s", got)
	}
	if got := cores.ApplyDelayFor(core.Kind(model.VLESS)); got != 0 {
		t.Fatalf("ApplyDelayFor(vless) = %s, want 0: Xray applies at once", got)
	}

	encoded, err := json.Marshal(coreViews(context.Background(), reg))
	if err != nil {
		t.Fatalf("marshal views: %v", err)
	}
	if !strings.Contains(string(encoded), `"applyDelaySeconds":{"slowkind":10}`) {
		t.Fatalf("the core view does not carry the delay: %s", encoded)
	}

	slow := &model.Inbound{UserId: 1, Protocol: model.Protocol("slowkind"), Tag: "slow-1", Port: 24401, Settings: `{"clients":[]}`, StreamSettings: `{}`}
	fast := &model.Inbound{UserId: 1, Protocol: model.VLESS, Tag: "fast-1", Port: 24402, Settings: `{"clients":[]}`, StreamSettings: `{"network":"tcp"}`}
	for _, ib := range []*model.Inbound{slow, fast} {
		if err := database.GetDB().Create(ib).Error; err != nil {
			t.Fatalf("seed %s: %v", ib.Tag, err)
		}
	}
	svc := &InboundService{}
	if got := svc.ApplyDelayFor([]int{fast.Id, slow.Id}); got != 10 {
		t.Fatalf("ApplyDelayFor(both) = %d, want the slow inbound's 10", got)
	}
	if got := svc.ApplyDelayFor([]int{fast.Id}); got != 0 {
		t.Fatalf("ApplyDelayFor(fast) = %d, want 0", got)
	}
}
