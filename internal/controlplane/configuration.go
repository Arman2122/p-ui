package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func (s *Store) PublishConfiguration(ctx context.Context, p identity.Principal, desired configuration.Bundle) (uint64, error) {
	payload, err := json.Marshal(desired)
	if err != nil {
		return 0, err
	}
	bundle, err := configuration.Decode(payload)
	if err != nil {
		return 0, err
	}
	var revision uint64
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		if err := lockNode(tx, p.NodeID, p.InstallationID); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", p.NodeID).Error; err != nil {
			return err
		}
		if node.DesiredRevision == math.MaxInt64 {
			return ErrRevision
		}
		if err := validateRuntimeGrants(tx, p, bundle); err != nil {
			return err
		}
		if err := validateRuntimeAdmissions(tx, p, bundle); err != nil {
			return err
		}
		var old model.FleetDeployment
		if err := tx.Find(&old, "node_id = ? AND revision = ?", p.NodeID, node.DesiredRevision).Error; err != nil {
			return err
		}
		if previous, err := configuration.Decode(old.Payload); err == nil {
			if bytes.Equal(normalizedBundle(previous), normalizedBundle(bundle)) {
				revision = uint64(node.DesiredRevision)
				return indexDeploymentAdmissions(tx, node, old)
			}
		}
		for i := range bundle.Runtimes {
			for j := range bundle.Runtimes[i].Grants {
				bundle.Runtimes[i].Grants[j].Policy.Revision = uint64(node.DesiredRevision + 1)
			}
		}
		payload, err = json.Marshal(bundle)
		if err != nil {
			return err
		}
		revision, err = (&Store{db: tx}).publish(ctx, p.NodeID, payload)
		return err
	})
	return revision, err
}

func normalizedBundle(bundle configuration.Bundle) []byte {
	for i := range bundle.Runtimes {
		for j := range bundle.Runtimes[i].Grants {
			bundle.Runtimes[i].Grants[j].Policy.Revision = 1
		}
		slices.SortFunc(bundle.Runtimes[i].Grants, func(a, b governor.Grant) int { return strings.Compare(a.ID, b.ID) })
	}
	slices.SortFunc(bundle.Runtimes, func(a, b configuration.Runtime) int { return strings.Compare(a.ID, b.ID) })
	encoded, _ := json.Marshal(bundle)
	return encoded
}

func validateRuntimeGrants(tx *gorm.DB, p identity.Principal, bundle configuration.Bundle) error {
	for _, runtime := range bundle.Runtimes {
		for offset := 0; offset < len(runtime.Grants); offset += 1024 {
			chunk := runtime.Grants[offset:min(offset+1024, len(runtime.Grants))]
			ids := make([]string, len(chunk))
			for i, g := range chunk {
				ids[i] = g.ID
			}
			var grants []model.FleetGrant
			if err := tx.Where("id IN ? AND node_id = ? AND installation_id = ?", ids, p.NodeID, p.InstallationID).Find(&grants).Error; err != nil {
				return err
			}
			var shares []model.FleetRateShare
			if err := tx.Where("id IN ? AND node_id = ? AND installation_id = ?", ids, p.NodeID, p.InstallationID).Find(&shares).Error; err != nil {
				return err
			}
			if len(grants) != len(chunk) || len(shares) != len(chunk) {
				return ErrScope
			}
			byGrant := make(map[string]model.FleetGrant, len(grants))
			byShare := make(map[string]model.FleetRateShare, len(shares))
			for _, g := range grants {
				byGrant[g.ID] = g
			}
			for _, s := range shares {
				byShare[s.ID] = s
			}
			for i, g := range chunk {
				a, s := byGrant[g.ID], byShare[g.ID]
				if g.Policy.CreditRevision != 0 && g.Policy.CreditRevision != uint64(a.Revision) {
					return ErrRevision
				}
				chunk[i].Policy.CreditRevision = uint64(a.Revision)
				if g.Policy.AuthorityRevision != 0 && g.Policy.AuthorityRevision != uint64(s.Revision) {
					return ErrRevision
				}
				chunk[i].Policy.AuthorityRevision = uint64(s.Revision)
				if a.RuntimeID != runtime.ID || a.ClientID != g.ClientID || a.TermID != g.Policy.Term || a.Unlimited != g.Policy.QuotaUnlimited ||
					uint64(a.IssuedBytes) != g.Policy.CreditCeiling || s.RuntimeID != runtime.ID || uint64(s.Revision) != g.RateRevision {
					return ErrScope
				}
				rates, err := decodeRates(s.Desired)
				if err != nil {
					return err
				}
				if rates.Upload != g.Policy.Upload || rates.Download != g.Policy.Download {
					return ErrScope
				}
			}
		}
	}
	return nil
}

func acknowledgeConfiguration(tx *gorm.DB, p identity.Principal, payload []byte) error {
	bundle, err := configuration.Decode(payload)
	if err != nil {
		return nil
	}
	type claim struct {
		grant   governor.Grant
		runtime string
	}
	claims := make(map[string]claim)
	var ids []string
	for _, runtime := range bundle.Runtimes {
		for _, grant := range runtime.Grants {
			if grant.RateRevision == 0 {
				continue
			}
			claims[grant.ID] = claim{grant: grant, runtime: runtime.ID}
			ids = append(ids, grant.ID)
		}
	}
	var changed []model.FleetRateShare
	var budgets []string
	for offset := 0; offset < len(ids); offset += 1024 {
		chunk := ids[offset:min(offset+1024, len(ids))]
		var shares []model.FleetRateShare
		if err := tx.Where("id IN ?", chunk).Find(&shares).Error; err != nil {
			return err
		}
		if len(shares) != len(chunk) {
			return ErrScope
		}
		for _, share := range shares {
			claim := claims[share.ID]
			if share.NodeID != p.NodeID || share.InstallationID != p.InstallationID || share.RuntimeID != claim.runtime {
				return ErrScope
			}
			if uint64(share.Revision) != claim.grant.RateRevision {
				continue
			}
			desired, err := decodeRates(share.Desired)
			if err != nil {
				return err
			}
			if desired.Upload != claim.grant.Policy.Upload || desired.Download != claim.grant.Policy.Download {
				return ErrScope
			}
			share.AppliedRevision, share.Outstanding = share.Revision, share.Desired
			changed = append(changed, share)
			budgets = append(budgets, share.BudgetID)
		}
	}
	slices.Sort(budgets)
	budgets = slices.Compact(budgets)
	for offset := 0; offset < len(budgets); offset += 1024 {
		var held []model.FleetRateBudget
		chunk := budgets[offset:min(offset+1024, len(budgets))]
		if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", chunk).Order("id").Find(&held).Error; err != nil {
			return err
		}
		if len(held) != len(chunk) {
			return ErrScope
		}
	}
	if len(changed) != 0 {
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"applied_revision", "outstanding"})}).CreateInBatches(changed, 1024).Error
	}
	return nil
}
