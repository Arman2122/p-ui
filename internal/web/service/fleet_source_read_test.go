package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

func sourceReadFixture(t *testing.T) (fleetClientFixture, model.FleetClient) {
	t.Helper()
	f := fleetFixture(t, false)
	materialized := f.materialize(t, 0)
	f.approve(t, materialized)
	var client model.FleetClient
	if err := f.db.First(&client, "id = ?", materialized.Client.ID).Error; err != nil {
		t.Fatal(err)
	}
	return f, client
}

func sourceReadBinding(t *testing.T, view entity.FleetClientSourceView, id int) entity.FleetClientSourceBinding {
	t.Helper()
	index := slices.IndexFunc(view.CredentialBindings, func(binding entity.FleetClientSourceBinding) bool { return binding.InboundID == id })
	if index < 0 {
		t.Fatalf("missing source credential binding for inbound %d", id)
	}
	return view.CredentialBindings[index]
}

func TestFleetSourceReadPreservesCommonOverridesAndRetainedScopePostgres(t *testing.T) {
	f, client := sourceReadFixture(t)
	credentials := []model.ClientCredential{
		{ClientId: f.client.Id, InboundId: f.inbounds[0].Id, Key: "uuid", Value: uuid.NewString()},
		{ClientId: f.client.Id, InboundId: f.inbounds[1].Id, Key: "uuid", Value: uuid.NewString()},
	}
	if err := f.db.Create(&credentials).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.ClientInbound{}).Where("client_id = ? AND inbound_id = ?", f.client.Id, f.inbounds[0].Id).Update("flow_override", "xtls-rprx-vision").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Where("client_id = ? AND inbound_id = ?", f.client.Id, f.inbounds[1].Id).Delete(&model.ClientInbound{}).Error; err != nil {
		t.Fatal(err)
	}
	link := model.ClientExternalLink{ClientId: f.client.Id, Kind: "subscription", Value: "https://example.invalid/fixture", Remark: "fixture"}
	if err := f.db.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	view, err := ReadFleetClientSource(t.Context(), f.db, &f.admin, client.ID)
	if err != nil || view.ActorID != f.admin.Id || view.ClientID != client.ID || view.PanelClientID != f.client.Id || view.Revision != uint64(client.Revision) || view.Client.ID != f.client.UUID {
		t.Fatalf("source read lost raw identity/revision: %v", err)
	}
	if !view.EffectiveScopeKnown || !slices.Equal(view.ExplicitInboundIDs, []int{f.inbounds[0].Id}) || view.EffectiveInboundIDs == nil || !slices.Equal(*view.EffectiveInboundIDs, view.ExplicitInboundIDs) || view.DependencyDiagnostic != "" {
		t.Fatal("source read fabricated or lost current membership")
	}
	one, two := sourceReadBinding(t, view, f.inbounds[0].Id), sourceReadBinding(t, view, f.inbounds[1].Id)
	if one.Scope != "effective" || two.Scope != "retained" || one.Values["uuid"] != credentials[0].Value || two.Values["uuid"] != credentials[1].Value || one.FlowOverride != "xtls-rprx-vision" || view.Client.Flow != f.client.Flow {
		t.Fatal("source read flattened per-binding credentials/flow or relabeled retained scope")
	}
	if len(view.ExternalLinks) != 1 || view.ExternalLinks[0].Value != link.Value || view.ExternalLinks[0].Remark != link.Remark {
		t.Fatal("source read lost external links")
	}
}

func TestFleetSourceReadInheritanceAndUnknownScopePostgres(t *testing.T) {
	f, client := sourceReadFixture(t)
	if err := f.db.Where("client_id = ?", f.client.Id).Delete(&model.ClientInbound{}).Error; err != nil {
		t.Fatal(err)
	}
	group := model.ClientGroup{Name: "read-group"}
	plan := model.Policy{Name: "read-plan", Tiers: `[]`, Rev: 2}
	for _, row := range []any{&group, &plan} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&f.client).Update("group_name", group.Name).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.PolicyInbound{PolicyId: plan.Id, InboundId: f.inbounds[0].Id},
		&model.GroupInbound{Name: group.Name, InboundID: f.inbounds[1].Id},
		&model.ClientPolicy{Email: f.client.Email, PolicyId: &plan.Id, AppliedRev: plan.Rev},
		&model.ClientCredential{ClientId: f.client.Id, InboundId: f.inbounds[0].Id, Key: "uuid", Value: uuid.NewString()},
	} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	view, err := ReadFleetClientSource(t.Context(), f.db, &f.admin, client.ID)
	if err != nil || !view.EffectiveScopeKnown || len(view.ExplicitInboundIDs) != 0 || view.EffectiveInboundIDs == nil || !slices.Equal(*view.EffectiveInboundIDs, []int{f.inbounds[0].Id}) || view.PolicyID == nil || *view.PolicyID != plan.Id || view.PlanOverrides == nil || *view.PlanOverrides.Inbounds {
		t.Fatalf("source read changed canonical inherited membership: %v", err)
	}
	if err := f.db.Model(&plan).Update("rev", plan.Rev+1).Error; err != nil {
		t.Fatal(err)
	}
	view, err = ReadFleetClientSource(t.Context(), f.db, &f.admin, client.ID)
	if err != nil || view.EffectiveScopeKnown || view.EffectiveInboundIDs != nil || view.DependencyDiagnostic != "SOURCE_MEMBERSHIP_UNRESOLVED" || view.PolicyID == nil || *view.PolicyID != plan.Id {
		t.Fatalf("unresolved source appeared as known empty membership: %v", err)
	}
	if binding := sourceReadBinding(t, view, f.inbounds[0].Id); binding.Scope != "unknown" {
		t.Fatal("unresolved dependency claimed retained or effective binding")
	}
	encoded, err := json.Marshal(view)
	if err != nil || !bytes.Contains(encoded, []byte(`"effectiveInboundIds":null`)) {
		t.Fatal("unknown membership lost explicit null wire scope")
	}
	if err := f.db.Model(&model.ClientPolicy{}).Where("email = ?", f.client.Email).Updates(map[string]any{"applied_rev": plan.Rev, "override_inbounds": true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Where("name = ?", group.Name).Delete(&model.GroupInbound{}).Error; err != nil {
		t.Fatal(err)
	}
	view, err = ReadFleetClientSource(t.Context(), f.db, &f.admin, client.ID)
	if err != nil || !view.EffectiveScopeKnown || view.EffectiveInboundIDs == nil || len(*view.EffectiveInboundIDs) != 0 {
		t.Fatalf("known empty source became unknown: %v", err)
	}
	encoded, err = json.Marshal(view)
	if err != nil || !bytes.Contains(encoded, []byte(`"effectiveInboundIds":[]`)) {
		t.Fatal("known empty membership lost explicit array wire scope")
	}
}

func TestFleetSourceReadIncludesPendingTermWithoutNodeWorkPostgres(t *testing.T) {
	f, client := sourceReadFixture(t)
	var placement model.FleetPlacement
	if err := f.db.First(&placement, "client_id = ?", client.ID).Error; err != nil {
		t.Fatal(err)
	}
	f.activate(t, placement)
	if _, err := (FleetClientService{}).Transition(t.Context(), &f.admin, FleetTransitionRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, Kind: controlplane.TrafficReset,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration),
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []any{&model.FleetSourceWork{}, &model.FleetPlacementIntent{}, &model.FleetPlacementFanout{}} {
		var count int64
		if err := f.db.Model(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("pending-term fixture unexpectedly has node work %T: %d %v", table, count, err)
		}
	}
	view, err := ReadFleetClientSource(t.Context(), f.db, &f.admin, client.ID)
	if err != nil || !view.Pending {
		t.Fatalf("pending term without node work appeared complete: %v", err)
	}
}

func TestFleetSourceReadCancelsWhileWaitingForPoolPostgres(t *testing.T) {
	f, client := sourceReadFixture(t)
	pool, err := f.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	maximum := pool.Stats().MaxOpenConnections
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { pool.SetMaxOpenConns(maximum) })
	borrowed, err := pool.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = borrowed.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	view, err := ReadFleetClientSource(ctx, f.db, &f.admin, client.ID)
	if !errors.Is(err, context.DeadlineExceeded) || view.ClientID != "" {
		t.Fatalf("source read ignored cancellation while waiting for pool: %v", err)
	}
}

func TestFleetSourceReadRejectsCredentialExpansionBeforeValuesPostgres(t *testing.T) {
	f, client := sourceReadFixture(t)
	ids := []int{f.inbounds[0].Id, f.inbounds[1].Id}
	inbounds := make([]model.Inbound, 511)
	for index := range inbounds {
		inbounds[index] = model.Inbound{UserId: f.admin.Id, Protocol: model.OpenVPN, Tag: uuid.NewString(), Port: 30000 + index, Settings: `{}`, StreamSettings: `{}`, Sniffing: `{}`}
	}
	if err := f.db.CreateInBatches(&inbounds, 128).Error; err != nil {
		t.Fatal(err)
	}
	for _, inbound := range inbounds {
		ids = append(ids, inbound.Id)
	}
	if err := f.db.Exec("INSERT INTO client_credentials (client_id,inbound_id,key,value) SELECT ?,id,'tlsCryptV2',repeat('x',65536) FROM inbounds WHERE id IN ?", f.client.Id, ids).Error; err != nil {
		t.Fatal(err)
	}
	callback := "source-read-value-bound-" + uuid.NewString()
	readValues := false
	if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if _, matched := tx.Statement.Dest.(*[]model.ClientCredential); matched {
			readValues = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
	view, err := ReadFleetClientSource(t.Context(), f.db, &f.admin, client.ID)
	if !errors.Is(err, ErrFleetSource) || readValues || view.ClientID != "" {
		t.Fatalf("oversize credentials were materialized or partially returned: valuesRead=%v err=%v", readValues, err)
	}
}

func TestFleetSourceReadRevisionAndFieldsShareSnapshotPostgres(t *testing.T) {
	f, client := sourceReadFixture(t)
	originalRevision := uint64(client.Revision)
	callback := "source-read-snapshot-" + uuid.NewString()
	changed := false
	var writeErr error
	if err := f.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if changed || tx.Statement.Table != "fleet_clients" {
			return
		}
		changed = true
		writeErr = f.db.Transaction(func(writer *gorm.DB) error {
			if err := writer.Model(&f.client).Update("comment", "newer source").Error; err != nil {
				return err
			}
			return writer.Model(&client).Update("revision", client.Revision+1).Error
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
	view, err := ReadFleetClientSource(t.Context(), f.db, &f.admin, client.ID)
	if err != nil || !changed || writeErr != nil || view.Revision != originalRevision || view.Client.Comment != "" {
		t.Fatalf("source read mixed old revision and newer source: changed=%v write=%v read=%v revision=%d", changed, writeErr, err, view.Revision)
	}
	if err := f.db.Callback().Query().Remove(callback); err != nil {
		t.Fatal(err)
	}
	view, err = ReadFleetClientSource(t.Context(), f.db, &f.admin, client.ID)
	if err != nil || view.Revision != originalRevision+1 || view.Client.Comment != "newer source" {
		t.Fatalf("subsequent snapshot failed to see committed source: %v", err)
	}
}

func TestFleetSourceReadRejectsActorAndDatabaseDriftPostgres(t *testing.T) {
	f, client := sourceReadFixture(t)
	stale := f.admin
	stale.LoginEpoch++
	if _, err := ReadFleetClientSource(t.Context(), f.db, &stale, client.ID); !errors.Is(err, ErrResellerForbidden) {
		t.Fatalf("stale actor read source credentials: %v", err)
	}
	if err := f.db.Transaction(func(tx *gorm.DB) error {
		_, err := ReadFleetClientSource(t.Context(), tx, &f.admin, client.ID)
		return err
	}); !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("nested read degraded snapshot isolation: %v", err)
	}
	foreign, err := gorm.Open(postgres.Open(os.Getenv("PUI_DB_DSN")), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := foreign.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if _, err := ReadFleetClientSource(t.Context(), foreign, &f.admin, client.ID); !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("foreign handle read source credentials: %v", err)
	}
	callback := "source-read-replace-" + uuid.NewString()
	replaced := false
	var replacementErr error
	if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if replaced || tx.Statement.Table != "users" {
			return
		}
		replaced = true
		replacementErr = database.InitDB()
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
	view, err := ReadFleetClientSource(t.Context(), f.db, &f.admin, client.ID)
	if !replaced || replacementErr != nil || !errors.Is(err, ErrFleetRuntimeDatabase) || view.ClientID != "" {
		t.Fatalf("read released source after DB replacement: replaced=%v replacement=%v read=%v", replaced, replacementErr, err)
	}
	current := database.GetDB()
	currentPool, err := current.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := currentPool.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFleetClientSource(t.Context(), current, &f.admin, client.ID); !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("closed pool read source: %v", err)
	}
}
