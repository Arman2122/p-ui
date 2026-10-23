package service

import (
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"

	"gorm.io/gorm"
)

// A shared client edit makes every hosting node stale before its first apply.
func markClientNodesDirty(inboundIDs []int) error {
	var nodeIDs []int
	for _, batch := range chunkInts(inboundIDs, sqlInChunk) {
		var ids []int
		if err := database.GetDB().Model(&model.Inbound{}).
			Where("id IN ? AND node_id IS NOT NULL", batch).
			Distinct().Pluck("node_id", &ids).Error; err != nil {
			return err
		}
		nodeIDs = append(nodeIDs, ids...)
	}
	if len(nodeIDs) == 0 {
		return nil
	}
	return runSerializedTx(func(tx *gorm.DB) error {
		for _, id := range nodeIDs {
			if err := (&NodeService{}).MarkNodeDirtyTx(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
}
