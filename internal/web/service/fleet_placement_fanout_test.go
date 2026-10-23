package service

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func placementFanoutFixture(t *testing.T, members int) (fleetClientFixture, string, []string) {
	t.Helper()
	f := fleetFixture(t, false)
	s, err := NewFleetPlacementService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	configurePlacementNode(t, f, s, 0, "target")
	plan := model.Policy{Name: "fanout-plan", Tiers: `[]`}
	if err := f.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	var clients []string
	for i := range members {
		source := model.ClientRecord{Email: fmt.Sprintf("fanout-%03d", i), UUID: uuid.NewString(), Enable: true}
		if err := f.db.Create(&source).Error; err != nil {
			t.Fatal(err)
		}
		client := model.FleetClient{ID: uuid.NewString(), PanelClientID: &source.Id, Revision: 1, CredentialGeneration: 1, CredentialDigest: bytes.Repeat([]byte{byte(i + 1)}, 32), Owned: true}
		if err := f.db.Create(&client).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Create(&model.ClientInbound{ClientId: source.Id, InboundId: f.inbounds[0].Id}).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Create(&model.ClientPolicy{Email: source.Email, PolicyId: &plan.Id}).Error; err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client.ID)
	}
	ruleID := uuid.NewString()
	if _, err := s.PutRule(t.Context(), &f.admin, FleetPlacementRuleRequest{ID: ruleID, Scope: "plan", PlanID: &plan.Id, Enabled: true, NodeIDs: []string{f.nodes[0].NodeID}, InboundIDs: []int{f.inbounds[0].Id}}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.FleetPlacementFanout{}).Where("scope != 'rule'").Update("available_unix_milli", time.Now().Add(time.Hour).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	return f, ruleID, clients
}

func TestFleetPlacementFanoutUsesBoundedKeysetAndNeverFundsPostgres(t *testing.T) {
	f, ruleID, clients := placementFanoutFixture(t, 3)
	for expected := range 3 {
		worked, err := ProcessFleetPlacementFanout(t.Context(), f.db, 1)
		if err != nil || !worked {
			t.Fatalf("fanout page %d=%v %v", expected, worked, err)
		}
		var count int64
		if err := f.db.Model(&model.FleetPlacementIntent{}).Where("client_id IN ? AND assigned AND pending", clients).Count(&count).Error; err != nil || count != int64(expected+1) {
			t.Fatalf("fanout ignored page bound or repeated cursor: page=%d intents=%d err=%v", expected, count, err)
		}
	}
	var work model.FleetPlacementFanout
	if err := f.db.First(&work, "scope = 'rule' AND scope_id = ?", ruleID).Error; err != nil || work.Pending || work.LeaseID != nil {
		t.Fatalf("complete keyset retained work: %+v %v", work, err)
	}
	for _, modelType := range []any{&model.FleetPlacement{}, &model.FleetGrant{}, &model.FleetTerm{}, &model.FleetAuthorityUpdate{}} {
		var count int64
		if err := f.db.Model(modelType).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("fanout created runtime or billing authority: %T count=%d err=%v", modelType, count, err)
		}
	}
}

func TestFleetPlacementFanoutSupersessionRollsBackDerivedIntentPostgres(t *testing.T) {
	f, ruleID, _ := placementFanoutFixture(t, 1)
	var fired atomic.Bool
	callback := "placement_fanout_supersession"
	if err := f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "fleet_placement_intents" || !fired.CompareAndSwap(false, true) {
			return
		}
		if err := f.db.Transaction(func(other *gorm.DB) error { return controlplane.QueuePlacementReevaluationTx(other, "rule", ruleID) }); err != nil {
			tx.AddError(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Create().Remove(callback) })
	if worked, err := ProcessFleetPlacementFanout(t.Context(), f.db, 1); !worked || !fired.Load() || !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("superseded fanout committed derived intent: worked=%v injected=%v err=%v", worked, fired.Load(), err)
	}
	var intents int64
	if err := f.db.Model(&model.FleetPlacementIntent{}).Count(&intents).Error; err != nil || intents != 0 {
		t.Fatalf("fanout CAS failure leaked intent: %d %v", intents, err)
	}
	var work model.FleetPlacementFanout
	if err := f.db.First(&work, "scope = 'rule' AND scope_id = ?", ruleID).Error; err != nil || !work.Pending || work.Revision != 2 || work.LeaseID != nil {
		t.Fatalf("old worker overwrote newer fanout: %+v %v", work, err)
	}
	if worked, err := ProcessFleetPlacementFanout(t.Context(), f.db, 1); !worked || err != nil {
		t.Fatalf("replacement fanout failed: %v %v", worked, err)
	}
}

func TestFleetPlacementFanoutDefersBrokenSourceWithoutStarvingLaterClientsPostgres(t *testing.T) {
	for _, afterWrite := range []bool{false, true} {
		name := "stale-policy"
		if afterWrite {
			name = "after-intent-write"
		}
		t.Run(name, func(t *testing.T) {
			f, ruleID, clients := placementFanoutFixture(t, 3)
			slices.Sort(clients)
			if afterWrite {
				callback := "placement_partial_failure"
				if err := f.db.Callback().Create().After("gorm:create").Register(callback, func(tx *gorm.DB) {
					intent, ok := tx.Statement.Dest.(*model.FleetPlacementIntent)
					if ok && intent.ClientID == clients[0] {
						tx.AddError(ErrFleetSource)
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = f.db.Callback().Create().Remove(callback) })
			} else {
				if err := f.db.Exec("UPDATE client_policies SET applied_rev = applied_rev + 1 WHERE email = (SELECT c.email FROM clients c JOIN fleet_clients f ON f.panel_client_id = c.id WHERE f.id = ?)", clients[0]).Error; err != nil {
					t.Fatal(err)
				}
			}
			if worked, err := ProcessFleetPlacementFanout(t.Context(), f.db, 3); !worked || err != nil {
				t.Fatalf("broken source stopped the parent fanout: worked=%v err=%v", worked, err)
			}
			var intents []model.FleetPlacementIntent
			if err := f.db.Order("client_id").Find(&intents).Error; err != nil || len(intents) != 2 || intents[0].ClientID != clients[1] || intents[1].ClientID != clients[2] {
				t.Fatalf("failed client leaked partial intent or starved later clients: %+v %v", intents, err)
			}
			var parent, retry model.FleetPlacementFanout
			if err := f.db.First(&parent, "scope = 'rule' AND scope_id = ?", ruleID).Error; err != nil || parent.Pending || parent.LeaseID != nil {
				t.Fatalf("parent fanout did not advance past failed client: %+v %v", parent, err)
			}
			if err := f.db.First(&retry, "scope = 'client' AND scope_id = ?", clients[0]).Error; err != nil || !retry.Pending || retry.AvailableUnixMilli <= time.Now().UnixMilli() || retry.LeaseID != nil {
				t.Fatalf("failed source lost bounded independent retry: %+v %v", retry, err)
			}
		})
	}
}
