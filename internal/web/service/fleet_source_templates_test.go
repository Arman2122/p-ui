package service

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetPlacementInheritsPlanTemplatesWithoutPermanentExplicitLinksPostgres(t *testing.T) {
	f := newPlacementApplicationFixture(t)
	if err := f.f.db.Where("client_id = ?", f.f.client.Id).Delete(&model.ClientInbound{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Create(&model.PolicyInbound{PolicyId: *f.rule.PlanID, InboundId: f.f.inbounds[0].Id}).Error; err != nil {
		t.Fatal(err)
	}
	projection, err := f.service.PreviewClient(t.Context(), &f.f.admin, f.intent.ClientID)
	if err != nil || !slices.ContainsFunc(projection.Targets, func(target FleetPlacementTarget) bool {
		return target.NodeID == f.intent.NodeID && slices.Equal(target.InboundIDs, []int{f.f.inbounds[0].Id})
	}) {
		t.Fatalf("plan-only inherited template disappeared from placement: %+v %v", projection, err)
	}
	if err := f.f.db.Transaction(func(tx *gorm.DB) error {
		_, err := fleetSnapshot(tx, f.f.client.Id, []int{f.f.inbounds[0].Id}, "xray")
		return err
	}); err != nil {
		t.Fatalf("canonical snapshot rejected inherited plan template: %v", err)
	}
	ids := []int{f.f.inbounds[0].Id}
	if _, err := SetFleetClientPlacementTx(t.Context(), f.f.db, &f.f.admin, FleetPlacementPreferenceRequest{
		OperationID: uuid.NewString(), ClientID: f.intent.ClientID, ExpectedRevision: 1, Action: "attach", NodeID: f.intent.NodeID, InboundIDs: &ids,
	}); err != nil {
		t.Fatalf("manual target rejected effective inherited template: %v", err)
	}
	var explicit int64
	if err := f.f.db.Model(&model.ClientInbound{}).Where("client_id = ?", f.f.client.Id).Count(&explicit).Error; err != nil || explicit != 0 {
		t.Fatalf("inherited membership became permanent explicit attachment: %d %v", explicit, err)
	}
	if err := f.f.db.Model(&model.ClientPolicy{}).Where("email = ?", f.f.client.Email).Update("override_inbounds", true).Error; err != nil {
		t.Fatal(err)
	}
	projection, err = f.service.PreviewClient(t.Context(), &f.f.admin, f.intent.ClientID)
	if err != nil || len(projection.Targets) != 0 {
		t.Fatalf("explicit empty override retained inherited targets: %+v %v", projection, err)
	}
	if err := f.f.db.Transaction(func(tx *gorm.DB) error {
		_, err := fleetSnapshot(tx, f.f.client.Id, []int{f.f.inbounds[0].Id}, "xray")
		return err
	}); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("materialization bypassed removed inherited membership: %v", err)
	}
}

func TestFleetTemplateGroupMembershipCannotEscapeManagedPlanPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	group := model.ClientGroup{Name: "inherited-group"}
	plan := model.Policy{Name: "managed-membership", Tiers: `[]`}
	if err := f.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&f.client).Update("group_name", group.Name).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.GroupInbound{Name: group.Name, InboundID: f.inbounds[1].Id}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.ClientPolicy{Email: f.client.Email, PolicyId: &plan.Id}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.PolicyInbound{PolicyId: plan.Id, InboundId: f.inbounds[0].Id}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		_, err := fleetSnapshot(tx, f.client.Id, []int{f.inbounds[1].Id}, "xray")
		return err
	}); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("group membership escaped managed plan restriction: %v", err)
	}
	if err := f.db.Where("client_id = ?", f.client.Id).Delete(&model.ClientInbound{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ClientPolicy{}).Where("email = ?", f.client.Email).Update("override_inbounds", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		_, err := fleetSnapshot(tx, f.client.Id, []int{f.inbounds[1].Id}, "xray")
		return err
	}); err != nil {
		t.Fatalf("explicit plan override lost current group inheritance: %v", err)
	}
	if err := f.db.Where("name = ?", group.Name).Delete(&model.GroupInbound{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		_, err := fleetSnapshot(tx, f.client.Id, []int{f.inbounds[1].Id}, "xray")
		return err
	}); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("removed group inheritance remained materializable: %v", err)
	}
}
