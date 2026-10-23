package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type FleetNetworkDefaultsView struct {
	Revision      FleetNetworkRevisionView `json:"revision"`
	Specification FleetNetworkSpec         `json:"specification"`
}

type FleetNodeNetworkStateView struct {
	FleetNodeNetworkView
	Configured  bool                     `json:"configured"`
	Overrides   FleetNetworkOverrides    `json:"overrides"`
	Effective   FleetNetworkSpec         `json:"effective"`
	Diagnostics []FleetNetworkDiagnostic `json:"diagnostics,omitempty"`
}

type FleetNetworkAssetPage struct {
	Items      []FleetNetworkAssetView `json:"items"`
	NextCursor string                  `json:"nextCursor"`
}

type FleetNetworkPreviewRequest struct {
	NodeID             string `json:"nodeId" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	InstallationID     string `json:"installationId" example:"948a18c5-a5d0-4c28-8052-d46224088bd6"`
	ExpectedRevision   uint64 `json:"expectedRevision" example:"1"`
	ExpectedDefaultsID string `json:"expectedDefaultsId" example:"87cb0f1e-6f1f-466a-91d3-8a8dd983b458"`
}

type FleetNetworkPublishRequest struct {
	OperationID   string `json:"operationId" example:"e8c5a425-11b1-4f3a-9718-a60251d6810e"`
	CompilationID string `json:"compilationId" example:"70966504-cde8-478b-9f3a-1ca59d52131a"`
}

type FleetNetworkPublicationView struct {
	CompilationID      string `json:"compilationId"`
	NodeID             string `json:"nodeId"`
	DeploymentRevision uint64 `json:"deploymentRevision"`
	Replayed           bool   `json:"replayed"`
}

func (s *FleetNetworkService) ReadDefaults(ctx context.Context, actor *model.User) (FleetNetworkDefaultsView, error) {
	result := FleetNetworkDefaultsView{Specification: FleetNetworkSpec{Version: 1}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var head model.FleetNetworkDefaultsHead
		found := tx.Find(&head, "id = 1")
		if found.Error != nil || found.RowsAffected == 0 {
			return found.Error
		}
		row, err := loadNetworkRevision(tx, head.CurrentRevisionID, "defaults")
		if err != nil {
			return err
		}
		if json.Unmarshal([]byte(row.Specification), &result.Specification) != nil {
			return ErrFleetNetwork
		}
		result.Revision = networkRevisionView(row)
		return nil
	})
	return result, err
}

func (s *FleetNetworkService) ReadNode(ctx context.Context, actor *model.User, nodeID string) (FleetNodeNetworkStateView, error) {
	var result FleetNodeNetworkStateView
	if !fleetID(nodeID) {
		return result, ErrFleetNetwork
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", nodeID).Error; err != nil {
			return err
		}
		var head model.FleetNodeNetworkHead
		found := tx.Find(&head, "node_id = ?", nodeID)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected == 0 {
			result.FleetNodeNetworkView = FleetNodeNetworkView{NodeID: node.ID, InstallationID: node.InstallationID, FollowDefaults: true, Pending: true}
			result.Overrides = FleetNetworkOverrides{Version: 1, FollowDefaults: true}
			var global model.FleetNetworkDefaultsHead
			found := tx.Find(&global, "id = 1")
			if found.Error != nil {
				return found.Error
			}
			defaults := FleetNetworkSpec{Version: 1}
			if found.RowsAffected != 0 {
				row, err := loadNetworkRevision(tx, global.CurrentRevisionID, "defaults")
				if err != nil {
					return err
				}
				if json.Unmarshal([]byte(row.Specification), &defaults) != nil {
					return ErrFleetNetwork
				}
				result.Defaults = networkRevisionView(row)
			}
			var err error
			result.Effective, result.Origins, err = resolveFleetNetwork(defaults, result.Overrides)
			return err
		}
		state, err := loadFleetNetworkState(tx, node.ID, node.InstallationID)
		if err != nil && !errors.Is(err, ErrFleetNetworkDependency) {
			return err
		}
		result.Configured = true
		result.FleetNodeNetworkView = FleetNodeNetworkView{NodeID: node.ID, InstallationID: node.InstallationID, Revision: networkRevisionView(state.Revision), Defaults: networkRevisionView(state.Defaults), FollowDefaults: state.Head.FollowDefaults, Pending: state.Head.Pending, Origins: state.Origins}
		result.Overrides, result.Effective = state.Overrides, state.Resolved
		if err != nil {
			result.Diagnostics = []FleetNetworkDiagnostic{{Code: "NETWORK_OVERRIDE_CONFLICT"}}
		}
		return nil
	})
	return result, err
}

func (s *FleetNetworkService) ListAssets(ctx context.Context, actor *model.User, nodeID, after string, limit int) (FleetNetworkAssetPage, error) {
	result := FleetNetworkAssetPage{Items: []FleetNetworkAssetView{}}
	if !fleetID(nodeID) || after != "" && !fleetID(after) || limit < 1 || limit > 200 {
		return result, ErrFleetNetwork
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", nodeID).Error; err != nil {
			return err
		}
		var rows []struct {
			model.FleetNetworkAssetRevision
			Size int
		}
		query := tx.Table("fleet_network_asset_heads h").Select("a.asset_id,a.revision,a.node_id,a.installation_id,a.kind,a.digest,a.created_unix_milli,octet_length(a.data) AS size").
			Joins("JOIN fleet_network_asset_revisions a ON a.asset_id = h.asset_id AND a.revision = h.revision").Where("h.node_id = ? AND h.installation_id = ?", node.ID, node.InstallationID)
		if after != "" {
			query = query.Where("h.asset_id > ?", after)
		}
		if err := query.Order("h.asset_id").Limit(limit + 1).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) > limit {
			rows = rows[:limit]
			result.NextCursor = rows[len(rows)-1].AssetID
		}
		for _, row := range rows {
			result.Items = append(result.Items, FleetNetworkAssetView{AssetID: row.AssetID, Revision: uint64(row.Revision), NodeID: row.NodeID, InstallationID: row.InstallationID, Kind: row.Kind, SHA256: hex.EncodeToString(row.Digest), Size: row.Size, CreatedUnixMilli: row.CreatedUnixMilli})
		}
		return nil
	})
	return result, err
}
