package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetFreshSourceReadsAndFirstFundingIgnoreLegacyCountersPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	var templates []model.Inbound
	for index, template := range f.inbounds {
		template.Id, template.Port, template.Tag = 0, 25831+index, uuid.NewString()
		if err := f.db.Create(&template).Error; err != nil {
			t.Fatal(err)
		}
		templates = append(templates, template)
	}
	request := fleetCreateRequest(templates[0].Id)
	request.Client.Enable, request.Client.Group, request.Client.ExpiryTime = true, "fresh-owned-group", f.client.ExpiryTime
	request.InboundIds = []int{templates[0].Id, templates[1].Id}
	created, err := CreateFleetClientTx(t.Context(), f.db, &f.admin, request)
	if err != nil {
		t.Fatal(err)
	}
	f.client = model.ClientRecord{}
	if err := f.db.First(&f.client, created.PanelClientID).Error; err != nil {
		t.Fatal(err)
	}
	f.inbounds = templates
	assertFresh := func() {
		t.Helper()
		traffic, err := (&InboundService{}).GetClientTrafficByEmail(f.client.Email)
		if err != nil || traffic == nil || traffic.Up != 0 || traffic.Down != 0 || traffic.LastOnline != 0 || traffic.Accounting == nil || traffic.Accounting.ClientID != created.ClientID || traffic.Accounting.State != "awaiting_activation" {
			t.Fatalf("fresh owned source read required/adopted legacy seed: %+v %v", traffic, err)
		}
		page, err := (&ClientService{}).ListPaged(&InboundService{}, &SettingService{}, ClientPageParams{Search: f.client.Email})
		if err != nil || page.Filtered != 1 || len(page.Items) != 1 || page.Items[0].Traffic.Accounting == nil || page.Items[0].Traffic.Accounting.ClientID != created.ClientID || page.Items[0].Traffic.Up != 0 {
			t.Fatalf("fresh client list lost immutable accounting identity: %+v %v", page, err)
		}
		used, err := (&ClientService{}).ListPaged(&InboundService{}, &SettingService{}, ClientPageParams{Search: f.client.Email, UsageFrom: 1})
		if err != nil || used.Filtered != 0 {
			t.Fatalf("fresh source usage filter adopted legacy counters: %+v %v", used, err)
		}
		if group := groupByName(t, &ClientService{}, f.client.Group); group.TrafficUsed != 0 {
			t.Fatalf("fresh group display adopted legacy counters: %+v", group)
		}
	}
	assertFresh()
	if err := f.db.Create(&core.ClientTraffic{Email: f.client.Email, InboundId: templates[0].Id, Up: 100, Down: 200, LastOnline: 123456, Total: request.Client.TotalGB, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	assertFresh()
	if err := f.db.Create(&model.GroupQuota{Name: f.client.Group, QuotaBytes: 700}).Error; err != nil {
		t.Fatal(err)
	}
	placement := f.approve(t, f.materialize(t, 0))
	active, err := (FleetClientService{}).Transition(t.Context(), &f.admin, FleetTransitionRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, Kind: controlplane.Activation,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration),
	})
	if err != nil || active.Term.OpeningUploaded != 0 || active.Term.OpeningDownloaded != 0 {
		t.Fatalf("fresh first activation adopted unrelated legacy counters: %+v %v", active, err)
	}
	groupFleetCredit(t, f, placement, active.Term, 100)
	var budget model.FleetBudget
	if err := f.db.Table("fleet_budgets b").Select("b.*").Joins("JOIN fleet_budget_scopes s ON s.budget_id=b.id").Where("s.kind='group'").Take(&budget).Error; err != nil || budget.UsedBytes != 0 || budget.ReservedBytes != 100 {
		t.Fatalf("fresh group funding adopted legacy counters: %+v %v", budget, err)
	}
	if err := f.db.Model(&model.FleetClient{}).Where("id = ?", created.ClientID).Update("current_term_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&InboundService{}).GetClientTrafficByEmail(f.client.Email); !errors.Is(err, ErrFleetUsage) {
		t.Fatalf("lost current pointer was mistaken for a fresh zero ledger: %v", err)
	}
	if _, err := (FleetClientService{}).Materialize(t.Context(), &f.admin, FleetMaterializeRequest{
		PanelClientID: f.client.Id, NodeID: f.nodes[1].NodeID, InstallationID: f.nodes[1].InstallationID, RuntimeID: "xray", CoreID: "xray", InboundIDs: request.InboundIds,
	}); !errors.Is(err, ErrFleetUsage) {
		t.Fatalf("lost term history was re-adopted as fresh billing: %v", err)
	}
}
