package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type FleetNetworkAssetRequest struct {
	AssetID          string `json:"assetId" example:"17f763c4-6530-41f7-8a88-534560d4397e"`
	NodeID           string `json:"nodeId" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	InstallationID   string `json:"installationId" example:"948a18c5-a5d0-4c28-8052-d46224088bd6"`
	Kind             string `json:"kind" example:"tls-certificate"`
	ExpectedRevision uint64 `json:"expectedRevision" example:"0"`
	Data             []byte `json:"data" example:"AQ=="`
}

type FleetNetworkAssetView struct {
	AssetID          string `json:"assetId"`
	Revision         uint64 `json:"revision"`
	NodeID           string `json:"nodeId"`
	InstallationID   string `json:"installationId"`
	Kind             string `json:"kind"`
	SHA256           string `json:"sha256"`
	Size             int    `json:"size"`
	CreatedUnixMilli int64  `json:"createdUnixMilli"`
}

func networkAssetView(row model.FleetNetworkAssetRevision) FleetNetworkAssetView {
	return FleetNetworkAssetView{
		AssetID: row.AssetID, Revision: uint64(row.Revision), NodeID: row.NodeID, InstallationID: row.InstallationID,
		Kind: row.Kind, SHA256: hex.EncodeToString(row.Digest), Size: len(row.Data), CreatedUnixMilli: row.CreatedUnixMilli,
	}
}

func (s *FleetNetworkService) PutAsset(ctx context.Context, actor *model.User, request FleetNetworkAssetRequest) (FleetNetworkAssetView, error) {
	var result FleetNetworkAssetView
	if !fleetID(request.AssetID) || !validFleetAssetKind(request.Kind) || request.ExpectedRevision > math.MaxInt64 || len(request.Data) == 0 || len(request.Data) > 8<<20 {
		return result, ErrFleetNetwork
	}
	digest := sha256.Sum256(request.Data)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		if err := lockFleetNetworkNode(tx, request.NodeID, request.InstallationID); err != nil {
			return err
		}
		var head model.FleetNetworkAssetHead
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&head, "asset_id = ?", request.AssetID).Error; err != nil {
			return err
		}
		if head.Revision != 0 {
			if head.NodeID != request.NodeID || head.InstallationID != request.InstallationID {
				return ErrFleetNetworkDependency
			}
			var previous model.FleetNetworkAssetRevision
			if err := tx.First(&previous, "asset_id = ? AND revision = ?", head.AssetID, head.Revision).Error; err != nil {
				return err
			}
			if previous.Kind == request.Kind && bytes.Equal(previous.Digest, digest[:]) && (request.ExpectedRevision == uint64(head.Revision) || request.ExpectedRevision+1 == uint64(head.Revision)) {
				result = networkAssetView(previous)
				return nil
			}
		}
		if uint64(head.Revision) != request.ExpectedRevision || head.Revision == math.MaxInt64 {
			return ErrFleetNetworkRevision
		}
		head = model.FleetNetworkAssetHead{AssetID: request.AssetID, NodeID: request.NodeID, InstallationID: request.InstallationID, Revision: head.Revision + 1}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&head).Error; err != nil {
			return err
		}
		row := model.FleetNetworkAssetRevision{
			AssetID: head.AssetID, Revision: head.Revision, NodeID: head.NodeID, InstallationID: head.InstallationID,
			Kind: request.Kind, Data: bytes.Clone(request.Data), Digest: digest[:], CreatedUnixMilli: time.Now().UnixMilli(), ActorID: actor.Id,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result = networkAssetView(row)
		return nil
	})
	return result, err
}
