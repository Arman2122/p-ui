package service

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

func ReadFleetClientSource(ctx context.Context, db *gorm.DB, actor *model.User, clientID string) (entity.FleetClientSourceView, error) {
	var result entity.FleetClientSourceView
	if !fleetID(clientID) {
		return result, controlplane.ErrInvalid
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := fleetSourceReadPool(readCtx, db)
	if err != nil {
		return result, err
	}
	err = db.WithContext(readCtx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var err error
		result, err = readFleetClientSourceTx(tx, actor.Id, clientID)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err == nil {
		var current *sql.DB
		current, err = fleetSourceReadPool(readCtx, db)
		if err == nil && current != pool {
			err = ErrFleetRuntimeDatabase
		}
	}
	if err != nil {
		return entity.FleetClientSourceView{}, err
	}
	return result, nil
}

func fleetSourceReadPool(ctx context.Context, db *gorm.DB) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil || db.Statement == nil || db.Config == nil || database.GetDB() != db {
		return nil, ErrFleetRuntimeDatabase
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return nil, ErrFleetRuntimeDatabase
	}
	pool, err := db.DB()
	if err != nil || pool == nil {
		return nil, errors.Join(ErrFleetRuntimeDatabase, err)
	}
	if err := pool.PingContext(ctx); err != nil {
		return nil, errors.Join(ErrFleetRuntimeDatabase, err)
	}
	if database.GetDB() != db {
		return nil, ErrFleetRuntimeDatabase
	}
	return pool, nil
}

func readFleetClientSourceTx(tx *gorm.DB, actorID int, clientID string) (entity.FleetClientSourceView, error) {
	result := entity.FleetClientSourceView{ActorID: actorID, ExplicitInboundIDs: []int{}, CredentialBindings: []entity.FleetClientSourceBinding{}, ExternalLinks: []entity.ExternalLinkInput{}}
	var client model.FleetClient
	if err := tx.First(&client, "id = ?", clientID).Error; err != nil {
		return result, err
	}
	if !client.Owned || client.PanelClientID == nil || client.Revision <= 0 {
		return result, ErrFleetSource
	}
	var source model.ClientRecord
	if err := tx.First(&source, *client.PanelClientID).Error; err != nil {
		return result, err
	}
	result.ClientID, result.PanelClientID, result.Revision = client.ID, source.Id, uint64(client.Revision)
	result.Client = *source.ToClient()
	var assignment model.ClientPolicy
	var selected *model.ClientPolicy
	found := tx.Where("email = ?", source.Email).Find(&assignment)
	if found.Error != nil {
		return result, found.Error
	}
	if found.RowsAffected != 0 {
		selected = &assignment
		result.PolicyID = assignment.PolicyId
		result.PlanOverrides = &entity.PlanOverrides{Quota: &assignment.OverrideQuota, Expiry: &assignment.OverrideExpiry, LimitIP: &assignment.OverrideLimitIP, Inbounds: &assignment.OverrideInbounds}
	}
	var links []model.ClientInbound
	if err := tx.Where("client_id = ?", source.Id).Order("inbound_id").Limit(4097).Find(&links).Error; err != nil {
		return result, err
	}
	if len(links) > 4096 {
		return result, ErrFleetSource
	}
	for _, link := range links {
		result.ExplicitInboundIDs = append(result.ExplicitInboundIDs, link.InboundId)
	}
	effective, err := fleetEffectiveTemplateIDs(tx, source, selected, result.ExplicitInboundIDs, false)
	if err != nil && !errors.Is(err, ErrFleetSource) && !errors.Is(err, ErrResellerForbidden) && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if err != nil {
		result.DependencyDiagnostic = "SOURCE_MEMBERSHIP_UNRESOLVED"
	} else {
		effective = append([]int{}, effective...)
		result.EffectiveScopeKnown, result.EffectiveInboundIDs = true, &effective
	}
	if err := readFleetSourceBindings(tx, source.Id, links, &result); err != nil {
		return result, err
	}
	var external []model.ClientExternalLink
	if err := tx.Where("client_id = ?", source.Id).Order("sort_index,id").Limit(129).Find(&external).Error; err != nil {
		return result, err
	}
	if len(external) > 128 {
		return result, ErrFleetSource
	}
	for _, link := range external {
		result.ExternalLinks = append(result.ExternalLinks, entity.ExternalLinkInput{Kind: link.Kind, Value: link.Value, Remark: link.Remark})
	}
	err = tx.Raw(`SELECT EXISTS (SELECT 1 FROM fleet_source_works WHERE client_id=? AND pending)
OR EXISTS (SELECT 1 FROM fleet_placement_intents WHERE client_id=? AND pending)
OR EXISTS (SELECT 1 FROM fleet_placement_fanouts WHERE scope='client' AND scope_id=? AND pending)`, client.ID, client.ID, client.ID).Scan(&result.Pending).Error
	result.Pending = result.Pending || client.PendingTermID != nil
	return result, err
}

func readFleetSourceBindings(tx *gorm.DB, clientID int, links []model.ClientInbound, result *entity.FleetClientSourceView) error {
	var size struct{ Rows, Bytes int64 }
	if err := tx.Model(&model.ClientCredential{}).Where("client_id = ?", clientID).
		Select("COUNT(*) AS rows, COALESCE(SUM(octet_length(value)), 0) AS bytes").Scan(&size).Error; err != nil {
		return err
	}
	if size.Rows > 65536 || size.Bytes > maxFleetSourceCredentialBytes {
		return ErrFleetSource
	}
	var credentials []model.ClientCredential
	if err := tx.Where("client_id = ?", clientID).Order("inbound_id,key").Limit(65537).Find(&credentials).Error; err != nil {
		return err
	}
	if len(credentials) > 65536 {
		return ErrFleetSource
	}
	bindings := map[int]entity.FleetClientSourceBinding{}
	var effective []int
	if result.EffectiveInboundIDs != nil {
		effective = *result.EffectiveInboundIDs
	}
	for _, id := range effective {
		bindings[id] = entity.FleetClientSourceBinding{InboundID: id, Values: map[string]string{}}
	}
	for _, link := range links {
		bindings[link.InboundId] = entity.FleetClientSourceBinding{InboundID: link.InboundId, FlowOverride: link.FlowOverride, Values: map[string]string{}}
	}
	for _, credential := range credentials {
		binding := bindings[credential.InboundId]
		if binding.Values == nil {
			binding.InboundID, binding.Values = credential.InboundId, map[string]string{}
		}
		binding.Values[credential.Key] = credential.Value
		bindings[credential.InboundId] = binding
	}
	ids := make([]int, 0, len(bindings))
	for id := range bindings {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		binding := bindings[id]
		binding.Scope = "unknown"
		if result.EffectiveScopeKnown {
			binding.Scope = "retained"
			if slices.Contains(effective, id) {
				binding.Scope = "effective"
			}
		}
		result.CredentialBindings = append(result.CredentialBindings, binding)
	}
	return nil
}
