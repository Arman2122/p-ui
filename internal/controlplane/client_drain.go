package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"math"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func (s *Store) CompleteTransition(ctx context.Context, request CompleteTransitionRequest) (TransitionResult, error) {
	if !validID(request.ClientID) || !validID(request.PendingTermID) || request.ExpectedRevision == 0 || request.ExpectedRevision > math.MaxInt64 ||
		request.ExpectedCredentialGeneration == 0 || request.ExpectedCredentialGeneration > math.MaxInt64 || len(request.SourcePolicyDigest) != sha256.Size {
		return TransitionResult{}, ErrInvalid
	}
	return s.lifecycleOperation(ctx, request.OperationID, "complete", request, func(tx *gorm.DB) (TransitionResult, error) {
		client, err := lockClient(tx, request.ClientID)
		if err != nil {
			return TransitionResult{}, err
		}
		if uint64(client.Revision) != request.ExpectedRevision || uint64(client.CredentialGeneration) != request.ExpectedCredentialGeneration || client.Revision == math.MaxInt64 ||
			client.PendingTermID == nil || *client.PendingTermID != request.PendingTermID || client.CurrentTermID == nil {
			return TransitionResult{}, ErrRevision
		}
		var term, previous model.FleetTerm
		if err := tx.First(&term, "id = ? AND client_id = ?", request.PendingTermID, client.ID).Error; err != nil {
			return TransitionResult{}, err
		}
		if err := tx.First(&previous, "id = ? AND client_id = ?", *client.CurrentTermID, client.ID).Error; err != nil {
			return TransitionResult{}, err
		}
		if term.State != "pending" || previous.State != "draining" || term.PredecessorID == nil || *term.PredecessorID != previous.ID || !bytes.Equal(term.SourcePolicyDigest, request.SourcePolicyDigest) {
			return TransitionResult{}, ErrRevision
		}
		var rates model.FleetRateBudget
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&rates, "id = ? AND client_id = ?", client.ID, client.ID).Error; err != nil {
			return TransitionResult{}, err
		}
		if err := requireStoppedGrants(tx, previous); err != nil {
			return TransitionResult{}, err
		}
		if err := startTerm(&term, time.Now().UnixMilli()); err != nil {
			return TransitionResult{}, err
		}
		term.State = "active"
		term.CredentialGeneration = client.CredentialGeneration
		if err := tx.Save(&term).Error; err != nil {
			return TransitionResult{}, err
		}
		if err := tx.Model(&previous).Update("state", "closed").Error; err != nil {
			return TransitionResult{}, err
		}
		client.CurrentTermID, client.PendingTermID = &term.ID, nil
		client.Revision++
		if err := tx.Save(&client).Error; err != nil {
			return TransitionResult{}, err
		}
		if err := activateRatePool(ctx, tx, client.ID, term); err != nil {
			return TransitionResult{}, err
		}
		return TransitionResult{Client: client, Term: term}, nil
	})
}

func requireStoppedGrants(tx *gorm.DB, term model.FleetTerm) error {
	var grants []model.FleetGrant
	return tx.Where("client_id = ? AND term_id = ?", term.ClientID, term.ID).FindInBatches(&grants, 1000, func(_ *gorm.DB, _ int) error {
		ids := make([]string, len(grants))
		for i, grant := range grants {
			ids[i] = grant.ID
		}
		var shares []model.FleetRateShare
		if err := tx.Where("id IN ?", ids).Find(&shares).Error; err != nil {
			return err
		}
		var stops []model.FleetGrantStop
		if err := tx.Where("grant_id IN ?", ids).Find(&stops).Error; err != nil {
			return err
		}
		if len(shares) != len(ids) || len(stops) != len(ids) {
			return ErrDrain
		}
		byGrant := make(map[string]model.FleetGrantStop, len(stops))
		for _, stop := range stops {
			byGrant[stop.GrantID] = stop
		}
		for _, share := range shares {
			stop := byGrant[share.ID]
			if share.BudgetID != term.ClientID || stop.DeploymentRevision == 0 || stop.RateRevision > share.AppliedRevision {
				return ErrDrain
			}
			outstanding, err := decodeRates(share.Outstanding)
			if err != nil {
				return err
			}
			if outstanding != (Rates{}) {
				return ErrDrain
			}
		}
		return nil
	}).Error
}

func managedAuthority(tx *gorm.DB, clientID, termID string, active bool) (*model.FleetTerm, error) {
	var client model.FleetClient
	result := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", clientID).Find(&client)
	if result.Error != nil || result.RowsAffected == 0 {
		return nil, result.Error
	}
	var term model.FleetTerm
	if err := tx.First(&term, "id = ? AND client_id = ?", termID, clientID).Error; err != nil {
		return nil, err
	}
	if active && (client.PanelClientID == nil || client.CurrentTermID == nil || *client.CurrentTermID != term.ID || term.State != "active") {
		return nil, ErrScope
	}
	return &term, nil
}

func guardManagedReservation(tx *gorm.DB, request ReserveRequest, placementID string) error {
	term, err := managedAuthority(tx, request.ClientID, request.TermID, true)
	if err != nil {
		return err
	}
	if term != nil && (!slices.Contains(request.BudgetIDs, term.BudgetID) || term.ExpiresUnixMilli != 0 && term.ExpiresUnixMilli <= time.Now().UnixMilli()) {
		return ErrScope
	}
	if term != nil {
		var owned bool
		if err := tx.Model(&model.FleetClient{}).Select("owned").Where("id = ?", request.ClientID).Scan(&owned).Error; err != nil {
			return err
		}
		if owned && placementID == "" {
			return ErrScope
		}
	}
	return nil
}

func guardManagedBundle(tx *gorm.DB, nodeID string, bundle configuration.Bundle) error {
	var grants []governor.Grant
	var clients, termIDs, enabled []string
	for _, runtime := range bundle.Runtimes {
		for _, grant := range runtime.Grants {
			grants = append(grants, grant)
			clients = append(clients, grant.ClientID)
			termIDs = append(termIDs, grant.Policy.Term)
			if grant.Policy.Enabled {
				enabled = append(enabled, grant.ID)
			}
		}
	}
	slices.Sort(clients)
	clients = slices.Compact(clients)
	slices.Sort(termIDs)
	termIDs = slices.Compact(termIDs)
	byClient := make(map[string]model.FleetClient)
	byTerm := make(map[string]model.FleetTerm)
	for offset := 0; offset < len(clients); offset += 1024 {
		var rows []model.FleetClient
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id IN ?", clients[offset:min(offset+1024, len(clients))]).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			byClient[row.ID] = row
		}
	}
	for offset := 0; offset < len(termIDs); offset += 1024 {
		var rows []model.FleetTerm
		if err := tx.Where("id IN ?", termIDs[offset:min(offset+1024, len(termIDs))]).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			byTerm[row.ID] = row
		}
	}
	now := time.Now().UnixMilli()
	for _, grant := range grants {
		client, managed := byClient[grant.ClientID]
		if !managed {
			continue
		}
		term, exists := byTerm[grant.Policy.Term]
		if !exists || term.ClientID != client.ID || grant.Policy.ExpiresUnixMilli != term.ExpiresUnixMilli {
			return ErrScope
		}
		if grant.Policy.Enabled && (client.PanelClientID == nil || client.CurrentTermID == nil || *client.CurrentTermID != term.ID || term.State != "active" ||
			term.ExpiresUnixMilli != 0 && term.ExpiresUnixMilli <= now) {
			return ErrScope
		}
	}
	if err := guardOwnedPlacements(tx, nodeID, bundle, byClient); err != nil {
		return err
	}
	for offset := 0; offset < len(enabled); offset += 1024 {
		if err := tx.Where("grant_id IN ?", enabled[offset:min(offset+1024, len(enabled))]).Delete(&model.FleetGrantStop{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func guardOwnedPlacements(tx *gorm.DB, nodeID string, bundle configuration.Bundle, clients map[string]model.FleetClient) error {
	type claim struct {
		grant         governor.Grant
		runtime, core string
	}
	claims := make(map[string]claim)
	var ids []string
	for _, runtime := range bundle.Runtimes {
		for _, grant := range runtime.Grants {
			if grant.Policy.Enabled && clients[grant.ClientID].Owned {
				claims[grant.ID] = claim{grant, runtime.ID, runtime.Core}
				ids = append(ids, grant.ID)
			}
		}
	}
	for offset := 0; offset < len(ids); offset += 1024 {
		chunk := ids[offset:min(offset+1024, len(ids))]
		var rows []model.FleetGrant
		if err := tx.Where("id IN ?", chunk).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(chunk) {
			return ErrScope
		}
		var placementIDs []string
		for _, row := range rows {
			if row.PlacementID == nil {
				return ErrScope
			}
			placementIDs = append(placementIDs, *row.PlacementID)
		}
		slices.Sort(placementIDs)
		placementIDs = slices.Compact(placementIDs)
		var placements []model.FleetPlacement
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id IN ?", placementIDs).Order("id").Find(&placements).Error; err != nil {
			return err
		}
		byID := make(map[string]model.FleetPlacement)
		for _, placement := range placements {
			byID[placement.ID] = placement
		}
		for _, row := range rows {
			claim, placement := claims[row.ID], byID[*row.PlacementID]
			if row.NodeID != nodeID || row.ClientID != claim.grant.ClientID || row.TermID != claim.grant.Policy.Term || row.RuntimeID != claim.runtime ||
				!placement.Enabled || len(placement.GraphDigest) != sha256.Size || placement.ClientID != row.ClientID || placement.NodeID != nodeID ||
				placement.InstallationID != row.InstallationID || placement.RuntimeID != claim.runtime || placement.CoreID != claim.core || placement.CredentialGeneration != clients[row.ClientID].CredentialGeneration {
				return ErrScope
			}
		}
	}
	return nil
}

func recordStoppedGrants(tx *gorm.DB, nodeID string, revision int64, payload []byte) error {
	bundle, err := configuration.Decode(payload)
	if err != nil {
		return nil
	}
	type stopped struct {
		grant   governor.Grant
		runtime string
	}
	claims := make(map[string]stopped)
	var ids []string
	for _, runtime := range bundle.Runtimes {
		for _, grant := range runtime.Grants {
			if grant.Policy.Enabled || grant.Policy.Upload.Mode != limits.Blocked || grant.Policy.Download.Mode != limits.Blocked || grant.RateRevision == 0 {
				continue
			}
			ids = append(ids, grant.ID)
			claims[grant.ID] = stopped{grant: grant, runtime: runtime.ID}
		}
	}
	for offset := 0; offset < len(ids); offset += 1024 {
		var shares []model.FleetRateShare
		if err := tx.Where("id IN ? AND node_id = ?", ids[offset:min(offset+1024, len(ids))], nodeID).Find(&shares).Error; err != nil {
			return err
		}
		var stops []model.FleetGrantStop
		for _, share := range shares {
			claim := claims[share.ID]
			if share.RuntimeID != claim.runtime || share.Revision != int64(claim.grant.RateRevision) || share.AppliedRevision != share.Revision {
				continue
			}
			outstanding, err := decodeRates(share.Outstanding)
			if err != nil {
				return err
			}
			if outstanding == (Rates{}) {
				stops = append(stops, model.FleetGrantStop{GrantID: share.ID, DeploymentRevision: revision, RateRevision: share.Revision})
			}
		}
		if len(stops) != 0 {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "grant_id"}}, DoUpdates: clause.AssignmentColumns([]string{"deployment_revision", "rate_revision"})}).Create(&stops).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
