package service

import (
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func fleetEnableAPIFixture(t *testing.T) (authorityFixture, model.ClientRecord) {
	t.Helper()
	f := newAuthorityFixture(t)
	legacy := model.ClientRecord{Email: "legacy-enable-neighbor", UUID: uuid.NewString(), Enable: true, TotalGB: 1000, ExpiryTime: f.f.client.ExpiryTime}
	if err := f.f.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Create(&model.ClientInbound{ClientId: legacy.Id, InboundId: f.f.inbounds[0].Id}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Create(&core.ClientTraffic{Email: legacy.Email, InboundId: f.f.inbounds[0].Id, Enable: true, Total: 1000, ExpiryTime: legacy.ExpiryTime}).Error; err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]any{"decryption": "none", "clients": []*model.Client{f.f.client.ToClient(), legacy.ToClient()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Model(&model.Inbound{}).Where("id = ?", f.f.inbounds[0].Id).Update("settings", string(settings)).Error; err != nil {
		t.Fatal(err)
	}
	previous := webruntime.GetManager()
	t.Cleanup(func() { webruntime.SetManager(previous) })
	return f, legacy
}

func useFleetEnableAPIDispatcher(t *testing.T, f fleetClientFixture) {
	t.Helper()
	dispatch, err := NewFleetRuntime(f.db)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := webruntime.NewFleetManager(dispatch)
	if err != nil {
		t.Fatal(err)
	}
	webruntime.SetManager(manager)
}

func TestFleetSourceEnableAPIRequiresDispatcherBeforeMixedMutationPostgres(t *testing.T) {
	f, legacy := fleetEnableAPIFixture(t)
	for _, manager := range []*webruntime.Manager{nil, webruntime.NewManager(webruntime.LocalDeps{})} {
		webruntime.SetManager(manager)
		result, _, err := (&ClientService{}).BulkSetEnableAuthorized(t.Context(), &f.f.admin, &InboundService{}, []string{legacy.Email, f.f.client.Email}, false)
		if !errors.Is(err, webruntime.ErrFleetDispatcher) || result.Changed != 0 || result.Pending != 0 {
			t.Fatalf("missing dispatcher did not fail before mutation=%+v err=%v", result, err)
		}
	}
	if _, _, err := (&ClientService{}).BulkSetEnable(&InboundService{}, []string{legacy.Email, f.f.client.Email}, false); !errors.Is(err, ErrFleetLifecycle) {
		t.Fatalf("actorless legacy enable accepted owned source=%v", err)
	}
	var count int64
	if err := f.f.db.Model(&model.ClientRecord{}).Where("id IN ? AND enable", []int{legacy.Id, f.f.client.Id}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("dispatcher failure changed normalized source=%d err=%v", count, err)
	}
	if err := f.f.db.Model(&core.ClientTraffic{}).Where("email IN ? AND enable", []string{legacy.Email, f.f.client.Email}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("dispatcher failure changed traffic=%d err=%v", count, err)
	}
	if err := f.f.db.Model(&model.FleetSourceWork{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("dispatcher failure queued work=%d err=%v", count, err)
	}
}

func TestFleetSourceEnableAPIMixedBatchPreservesOwnedTrafficAndAttachmentsPostgres(t *testing.T) {
	f, legacy := fleetEnableAPIFixture(t)
	useFleetEnableAPIDispatcher(t, f.f)
	result, _, err := (&ClientService{}).BulkSetEnableAuthorized(t.Context(), &f.f.admin, &InboundService{}, []string{legacy.Email, f.f.client.Email}, false)
	if err != nil || result.Changed != 2 || result.Pending != 1 || len(result.Skipped) != 0 || len(result.Outcome.OK) != 2 {
		t.Fatalf("mixed source enable API=%+v err=%v", result, err)
	}
	var owned, nonfleet model.ClientRecord
	if err := f.f.db.First(&owned, f.f.client.Id).Error; err != nil || owned.Enable {
		t.Fatalf("legacy neighbor rewrote owned normalized flag=%+v err=%v", owned, err)
	}
	if err := f.f.db.First(&nonfleet, legacy.Id).Error; err != nil || nonfleet.Enable {
		t.Fatalf("nonfleet enable behavior changed=%+v err=%v", nonfleet, err)
	}
	var traffic core.ClientTraffic
	if err := f.f.db.First(&traffic, "email = ?", owned.Email).Error; err != nil || !traffic.Enable {
		t.Fatalf("fleet API mutated legacy traffic authority=%+v err=%v", traffic, err)
	}
	var legacyTraffic core.ClientTraffic
	if err := f.f.db.First(&legacyTraffic, "email = ?", legacy.Email).Error; err != nil || legacyTraffic.Enable {
		t.Fatalf("legacy traffic did not follow flag=%+v err=%v", legacyTraffic, err)
	}
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", f.materialized.Placement.ID).Error; err != nil || !work.Pending || !work.RequiresStop || work.Enabled {
		t.Fatalf("fleet source intent was not atomic=%+v err=%v", work, err)
	}
	var grant model.FleetGrant
	if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil || grant.Revision != f.credit.Grant.Revision || grant.IssuedBytes != 300 || grant.ConsumedBytes != 0 {
		t.Fatalf("API enable altered owned quota=%+v err=%v", grant, err)
	}
	var attached int64
	if err := f.f.db.Model(&model.ClientInbound{}).Where("client_id = ?", owned.Id).Count(&attached).Error; err != nil || attached != 2 {
		t.Fatalf("legacy neighbor rebuilt owned attachments=%d err=%v", attached, err)
	}
	var inbound model.Inbound
	if err := f.f.db.First(&inbound, f.f.inbounds[0].Id).Error; err != nil {
		t.Fatal(err)
	}
	clients, err := (&InboundService{}).GetClients(&inbound)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if client.Email == owned.Email && !client.Enable || client.Email == legacy.Email && client.Enable {
			t.Fatalf("fleet API wrote legacy JSON or skipped neighbor=%+v", client)
		}
	}
	queued, _, err := (&ClientService{}).BulkSetEnableAuthorized(t.Context(), &f.f.admin, &InboundService{}, []string{owned.Email}, true)
	if err != nil || queued.Changed != 1 || queued.Pending != 1 {
		t.Fatalf("enable API implied immediate convergence=%+v err=%v", queued, err)
	}
}

func TestFleetSourceEnableAPIRollsBackRejectedClientAndReportsPartialResultPostgres(t *testing.T) {
	f, legacy := fleetEnableAPIFixture(t)
	useFleetEnableAPIDispatcher(t, f.f)
	for _, statement := range []string{
		`CREATE FUNCTION reject_source_enable_api() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'source enable API fault'; END $$`,
		`CREATE TRIGGER reject_source_enable_api BEFORE INSERT ON fleet_source_works FOR EACH ROW EXECUTE FUNCTION reject_source_enable_api()`,
	} {
		if err := f.f.db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, _, err := (&ClientService{}).BulkSetEnableAuthorized(t.Context(), &f.f.admin, &InboundService{}, []string{f.f.client.Email, legacy.Email}, false)
	if err != nil || result.Changed != 1 || result.Pending != 0 || len(result.Skipped) != 1 || result.Skipped[0].Email != f.f.client.Email || !strings.Contains(result.Skipped[0].Reason, "source enable API fault") {
		t.Fatalf("source mutation partial result=%+v err=%v", result, err)
	}
	var source model.ClientRecord
	var count int64
	if err := f.f.db.First(&source, f.f.client.Id).Error; err != nil || !source.Enable {
		t.Fatalf("rejected API source edit escaped transaction=%+v err=%v", source, err)
	}
	if err := f.f.db.Model(&model.FleetSourceWork{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected API source edit queued work=%d err=%v", count, err)
	}
	source = model.ClientRecord{}
	if err := f.f.db.First(&source, legacy.Id).Error; err != nil || source.Enable {
		t.Fatalf("independent nonfleet target was lost=%+v err=%v", source, err)
	}
}

func TestFleetSourceEnableAPIFencesOwnershipClaimAfterClassificationPostgres(t *testing.T) {
	f, legacy := fleetEnableAPIFixture(t)
	useFleetEnableAPIDispatcher(t, f.f)
	var claimed atomic.Bool
	var claimErr error
	callback := "test:source_enable_ownership_claim"
	if err := f.f.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "fleet_clients" || !strings.Contains(tx.Statement.SQL.String(), "owned AND panel_client_id IN") || !claimed.CompareAndSwap(false, true) {
			return
		}
		materialized, err := (FleetClientService{}).Materialize(t.Context(), &f.f.admin, FleetMaterializeRequest{
			PanelClientID: legacy.Id, NodeID: f.f.nodes[0].NodeID, InstallationID: f.f.nodes[0].InstallationID,
			RuntimeID: "xray", CoreID: "xray", InboundIDs: []int{f.f.inbounds[0].Id},
		})
		if err != nil {
			claimErr = err
			return
		}
		_, claimErr = (FleetClientService{}).Approve(t.Context(), &f.f.admin, FleetApproveRequest{
			PlacementID: materialized.Placement.ID, ExpectedRevision: uint64(materialized.Placement.Revision), DefinitionDigest: materialized.Placement.DefinitionDigest,
			SourcePolicyDigest: materialized.Placement.SourcePolicyDigest, GraphDigest: make([]byte, 32),
		})
	}); err != nil {
		t.Fatal(err)
	}
	result, _, err := (&ClientService{}).BulkSetEnableAuthorized(t.Context(), &f.f.admin, &InboundService{}, []string{legacy.Email, f.f.client.Email}, false)
	_ = f.f.db.Callback().Query().Remove(callback)
	if err != nil || !claimed.Load() || claimErr != nil || result.Changed != 1 || result.Pending != 1 || len(result.Skipped) != 1 || result.Skipped[0].Email != legacy.Email || result.Skipped[0].Reason != ErrFleetLifecycle.Error() {
		t.Fatalf("newly claimed source fell through legacy enable=%+v err=%v claimed=%v claim=%v", result, err, claimed.Load(), claimErr)
	}
	var source model.ClientRecord
	if err := f.f.db.First(&source, legacy.Id).Error; err != nil || !source.Enable {
		t.Fatalf("legacy enable overwrote newly claimed source=%+v err=%v", source, err)
	}
}

func TestFleetSourceEnableAPIRejectsIdentityReplacementBeforeSourceLockPostgres(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "rename", true: "delete"}[remove], func(t *testing.T) {
			f, legacy := fleetEnableAPIFixture(t)
			useFleetEnableAPIDispatcher(t, f.f)
			var replaced atomic.Bool
			var replacement model.ClientRecord
			var replaceErr error
			callback := "test:source_enable_identity_replacement"
			if err := f.f.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table != "clients" || len(tx.Statement.Selects) != 1 || tx.Statement.Selects[0] != "id,email" || !strings.Contains(tx.Statement.SQL.String(), "email IN") || !replaced.CompareAndSwap(false, true) {
					return
				}
				replaceErr = f.f.db.Transaction(func(write *gorm.DB) error {
					if remove {
						if err := write.Delete(&model.ClientRecord{}, legacy.Id).Error; err != nil {
							return err
						}
					} else if err := write.Model(&model.ClientRecord{}).Where("id = ?", legacy.Id).Update("email", "renamed-original-enable-client").Error; err != nil {
						return err
					}
					replacement = model.ClientRecord{Email: legacy.Email, UUID: uuid.NewString(), Enable: true}
					return write.Create(&replacement).Error
				})
			}); err != nil {
				t.Fatal(err)
			}
			result, _, err := (&ClientService{}).BulkSetEnableAuthorized(t.Context(), &f.f.admin, &InboundService{}, []string{legacy.Email}, false)
			_ = f.f.db.Callback().Query().Remove(callback)
			if err != nil || !replaced.Load() || replaceErr != nil || result.Changed != 0 || len(result.Skipped) != 1 || result.Skipped[0].Reason != ErrFleetSource.Error() {
				t.Fatalf("replacement identity was targeted by stale enable=%+v err=%v replaced=%v replacement=%v", result, err, replaced.Load(), replaceErr)
			}
			var saved model.ClientRecord
			if err := f.f.db.First(&saved, replacement.Id).Error; err != nil || !saved.Enable || saved.UUID != replacement.UUID {
				t.Fatalf("replacement source was mutated=%+v err=%v", saved, err)
			}
		})
	}
}
