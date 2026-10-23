package controlplane

import (
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func carryStopProvenance(tx *gorm.DB, principal identity.Principal, operationID string, base, revision int64) error {
	var ids []string
	if err := tx.Model(&model.FleetNetworkPublication{}).Where("node_id = ? AND deployment_revision = ?", principal.NodeID, base).Distinct("compilation_id").Limit(2).Pluck("compilation_id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) != 1 {
		return ErrStopComposition
	}
	var compilation model.FleetNetworkCompilation
	if err := tx.First(&compilation, "id = ?", ids[0]).Error; err != nil {
		return err
	}
	if compilation.NodeID != principal.NodeID || compilation.InstallationID != principal.InstallationID || !compilation.Ready {
		return ErrStopComposition
	}
	if err := carryStopProviderBindings(tx, principal, compilation, base, revision); err != nil {
		return err
	}
	return tx.Create(&model.FleetNetworkPublication{
		OperationID: operationID, CompilationID: compilation.ID, NodeID: principal.NodeID,
		DeploymentRevision: revision, CreatedUnixMilli: time.Now().UnixMilli(),
	}).Error
}
