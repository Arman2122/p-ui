package controlplane

import (
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func clearAppliedEnabledStops(tx *gorm.DB, principal identity.Principal, revision int64, payload []byte) error {
	bundle, err := configuration.Decode(payload)
	if err != nil {
		return nil
	}
	type enabledClaim struct {
		runtime string
		rate    uint64
	}
	claims := map[string]enabledClaim{}
	var ids []string
	for _, runtime := range bundle.Runtimes {
		for _, grant := range runtime.Grants {
			if grant.Policy.Enabled && grant.RateRevision != 0 {
				claims[grant.ID] = enabledClaim{runtime.ID, grant.RateRevision}
				ids = append(ids, grant.ID)
			}
		}
	}
	for offset := 0; offset < len(ids); offset += 1024 {
		var stops []model.FleetGrantStop
		if err := tx.Where("grant_id IN ?", ids[offset:min(offset+1024, len(ids))]).Find(&stops).Error; err != nil {
			return err
		}
		if len(stops) == 0 {
			continue
		}
		stopped := make(map[string]model.FleetGrantStop, len(stops))
		known := make([]string, 0, len(stops))
		for _, stop := range stops {
			stopped[stop.GrantID] = stop
			known = append(known, stop.GrantID)
		}
		var shares []model.FleetRateShare
		if err := tx.Where("id IN ? AND node_id = ? AND installation_id = ?", known, principal.NodeID, principal.InstallationID).Find(&shares).Error; err != nil {
			return err
		}
		for _, share := range shares {
			claim, stop := claims[share.ID], stopped[share.ID]
			if share.RuntimeID != claim.runtime || uint64(share.Revision) != claim.rate || share.AppliedRevision != share.Revision || stop.DeploymentRevision > revision || stop.RateRevision > share.Revision {
				continue
			}
			if err := tx.Where("grant_id = ? AND deployment_revision = ? AND rate_revision = ?", stop.GrantID, stop.DeploymentRevision, stop.RateRevision).Delete(&model.FleetGrantStop{}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
