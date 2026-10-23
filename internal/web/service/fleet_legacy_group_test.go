package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func legacyGroupMutationState(t *testing.T, f fleetClientFixture) []byte {
	t.Helper()
	var state []json.RawMessage
	for _, modelType := range []any{&model.ClientRecord{}, &model.ClientGroup{}, &model.GroupQuota{}, &model.GroupInbound{}, &model.ClientInbound{}, &model.ClientCredential{}, &model.FleetClient{}, &model.FleetBudget{}, &model.FleetGrant{}, &model.FleetSourceWork{}} {
		statement := &gorm.Statement{DB: f.db}
		if err := statement.Parse(modelType); err != nil {
			t.Fatal(err)
		}
		var row string
		if err := f.db.Raw("SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text, '[]') FROM " + statement.Schema.Table + " t").Scan(&row).Error; err != nil {
			t.Fatal(err)
		}
		state = append(state, json.RawMessage(row))
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestFleetLegacyInboundCommitRefusesOwnedTargetAndNeighborPostgres(t *testing.T) {
	for _, name := range []string{"detach-owned", "attach-neighbor"} {
		t.Run(name, func(t *testing.T) {
			f, _ := sourceReadFixture(t)
			legacy := model.ClientRecord{Email: "legacy-inbound-neighbor", UUID: uuid.NewString(), SubID: uuid.NewString()}
			if err := f.db.Create(&legacy).Error; err != nil {
				t.Fatal(err)
			}
			before := legacyGroupMutationState(t, f)
			var err error
			if name == "detach-owned" {
				_, err = (&ClientService{}).delInboundClients(&InboundService{}, f.inbounds[0].Id, []*model.ClientRecord{&f.client}, true)
			} else {
				encoded, marshalErr := json.Marshal(map[string]any{"clients": []model.Client{*legacy.ToClient()}})
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				_, err = (&ClientService{}).addInboundClient(&InboundService{}, &model.Inbound{Id: f.inbounds[0].Id, Settings: string(encoded)}, nil)
			}
			if !errors.Is(err, ErrFleetLifecycle) {
				t.Fatalf("legacy inbound commit rewrote owned source or neighbor: %v", err)
			}
			if !bytes.Equal(before, legacyGroupMutationState(t, f)) {
				t.Fatal("rejected legacy inbound commit changed normalized source or allowance")
			}
		})
	}
}

func TestFleetLegacyGroupRechecksOwnershipAfterReadingSourcesPostgres(t *testing.T) {
	f, group := groupedFleetFixture(t, 0, 0)
	materialized := f.materialize(t, 0)
	callback := "legacy-group-ownership-" + uuid.NewString()
	claimed := false
	var claimErr error
	if err := f.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if claimed || tx.Statement.Table != "clients" || !strings.Contains(tx.Statement.SQL.String(), "email IN") {
			return
		}
		claimed = true
		_, claimErr = (FleetClientService{}).Approve(t.Context(), &f.admin, FleetApproveRequest{
			PlacementID: materialized.Placement.ID, ExpectedRevision: uint64(materialized.Placement.Revision),
			DefinitionDigest: materialized.Placement.DefinitionDigest, SourcePolicyDigest: materialized.Placement.SourcePolicyDigest, GraphDigest: make([]byte, 32),
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
	_, err := (&ClientService{}).AddToGroup([]string{f.client.Email}, "forbidden-after-adoption")
	if !claimed || claimErr != nil || !errors.Is(err, ErrFleetLifecycle) {
		t.Fatalf("newly owned source passed legacy group commit: claimed=%v claim=%v edit=%v", claimed, claimErr, err)
	}
	var current model.ClientRecord
	if err := f.db.First(&current, f.client.Id).Error; err != nil || current.Group != group.Name {
		t.Fatalf("raced legacy group edit escaped source lock: %v", err)
	}
	var count int64
	if err := f.db.Model(&model.ClientGroup{}).Where("name = ?", "forbidden-after-adoption").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("raced refusal leaked target group: %d %v", count, err)
	}
}

func TestFleetLegacyGroupRejectsMemberAppearingBeforeParentLockPostgres(t *testing.T) {
	f, group := groupedFleetFixture(t, 0, 0)
	callback := "legacy-group-member-" + uuid.NewString()
	appeared := false
	var insertErr error
	if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		lock, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if appeared || tx.Statement.Table != "client_groups" || !ok || lock.Strength != "UPDATE" {
			return
		}
		appeared = true
		insertErr = f.db.Create(&model.ClientRecord{Email: "new-group-member", UUID: uuid.NewString(), Group: group.Name}).Error
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
	_, err := (&ClientService{}).RenameGroup(group.Name, "must-retry-new-member")
	if !appeared || insertErr != nil || !errors.Is(err, ErrFleetSource) {
		t.Fatalf("group edit acquired an unplanned source after parent lock: appeared=%v insert=%v edit=%v", appeared, insertErr, err)
	}
	var current model.ClientGroup
	if err := f.db.First(&current, group.Id).Error; err != nil || current.Name != group.Name {
		t.Fatalf("membership drift did not roll back group rename: %v", err)
	}
}

func TestFleetLegacyGroupQuotaAndTemplateMetadataRollbackTogetherPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	group := model.ClientGroup{Name: "atomic-empty-group"}
	if err := f.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.GroupQuota{Name: group.Name, QuotaBytes: 100}).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE FUNCTION reject_group_quota_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'group quota rejected'; END $$`,
		`CREATE TRIGGER reject_group_quota_write BEFORE INSERT OR UPDATE ON group_quota FOR EACH ROW EXECUTE FUNCTION reject_group_quota_write()`,
	} {
		if err := f.db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	ids := []int{f.inbounds[0].Id}
	err := (&ClientService{}).SetGroupQuota(GroupQuotaRequest{Name: group.Name, QuotaBytes: 200, InboundIDs: &ids})
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "P0001" {
		t.Fatalf("quota fixture failed outside commit: %v", err)
	}
	var count int64
	if err := f.db.Model(&model.GroupInbound{}).Where("name = ?", group.Name).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("group template metadata committed before rejected quota: %d %v", count, err)
	}
}

func TestFleetLegacyGroupMutationsRefuseBeforeAnyWritePostgres(t *testing.T) {
	for _, name := range []string{"move", "remove", "rename", "delete", "quota", "inbounds", "join-owned-pool", "retained-rename", "retained-delete", "retained-quota"} {
		t.Run(name, func(t *testing.T) {
			f, group := groupedFleetFixture(t, 0, 0)
			placement := f.approve(t, f.materialize(t, 0))
			if name == "retained-rename" || name == "retained-delete" || name == "retained-quota" {
				f.activate(t, placement)
				if err := f.db.Model(&f.client).Update("group_name", "later-group").Error; err != nil {
					t.Fatal(err)
				}
			}
			legacy := model.ClientRecord{Email: "legacy-group-neighbor", UUID: "legacy-group-neighbor"}
			if err := f.db.Create(&legacy).Error; err != nil {
				t.Fatal(err)
			}
			before := legacyGroupMutationState(t, f)
			service := &ClientService{}
			var err error
			switch name {
			case "move":
				_, err = service.AddToGroup([]string{f.client.Email}, "must-not-create")
			case "remove":
				_, err = service.RemoveFromGroup([]string{f.client.Email})
			case "rename", "retained-rename":
				_, err = service.RenameGroup(group.Name, "must-not-rename")
			case "delete", "retained-delete":
				_, err = service.DeleteGroup(group.Name)
			case "quota", "retained-quota":
				err = service.SetGroupQuota(GroupQuotaRequest{Name: group.Name, QuotaBytes: 900})
			case "inbounds":
				ids := []int{}
				err = service.SetGroupQuota(GroupQuotaRequest{Name: group.Name, QuotaBytes: 900, InboundIDs: &ids})
			case "join-owned-pool":
				_, err = service.AddToGroup([]string{legacy.Email}, group.Name)
			}
			if !errors.Is(err, ErrFleetLifecycle) {
				t.Fatalf("legacy group mutation bypassed owned lifecycle: %v", err)
			}
			if !bytes.Equal(before, legacyGroupMutationState(t, f)) {
				t.Fatal("rejected legacy group mutation changed source, pool, bindings or authority")
			}
		})
	}
}
