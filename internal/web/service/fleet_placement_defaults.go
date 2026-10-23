package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func (s *FleetPlacementService) ConfigureDefault(ctx context.Context, actor *model.User, request FleetPlacementDefaultRequest) (FleetPlacementDefaultView, error) {
	var view FleetPlacementDefaultView
	if request.ExpectedRevision >= math.MaxInt64 || (request.NodeID == nil) != (request.InstallationID == nil) || request.NodeID != nil && (!fleetID(*request.NodeID) || !fleetID(*request.InstallationID)) {
		return view, controlplane.ErrInvalid
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		if request.NodeID != nil {
			if err := lockFleetNode(tx, *request.NodeID, *request.InstallationID); err != nil {
				return err
			}
			var policy model.FleetNodePlacementPolicy
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&policy, "node_id = ? AND installation_id = ? AND enabled", *request.NodeID, *request.InstallationID).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.fleet-placement-default',0))").Error; err != nil {
			return err
		}
		var current model.FleetPlacementDefaults
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = 1").Find(&current).Error; err != nil {
			return err
		}
		if current.Revision > 0 && equalPlacementNodeID(current.NodeID, request.NodeID) && equalPlacementNodeID(current.InstallationID, request.InstallationID) &&
			(uint64(current.Revision) == request.ExpectedRevision || uint64(current.Revision) == request.ExpectedRevision+1) {
			view = FleetPlacementDefaultView{Revision: uint64(current.Revision), NodeID: current.NodeID, InstallationID: current.InstallationID, Pending: true}
			return nil
		}
		if uint64(current.Revision) != request.ExpectedRevision {
			return controlplane.ErrRevision
		}
		current = model.FleetPlacementDefaults{ID: 1, Revision: current.Revision + 1, NodeID: request.NodeID, InstallationID: request.InstallationID, ActorID: actor.Id, UpdatedUnixMilli: time.Now().UnixMilli()}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&current).Error; err != nil {
			return err
		}
		view = FleetPlacementDefaultView{Revision: uint64(current.Revision), NodeID: current.NodeID, InstallationID: current.InstallationID, Pending: true}
		return controlplane.QueuePlacementReevaluationTx(tx, "default", "1")
	})
	return view, err
}

func equalPlacementNodeID(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (s *FleetPlacementService) ReadDefault(ctx context.Context, actor *model.User) (FleetPlacementDefaultView, error) {
	var view FleetPlacementDefaultView
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var row model.FleetPlacementDefaults
		if err := tx.Where("id = 1").Find(&row).Error; err != nil {
			return err
		}
		view = FleetPlacementDefaultView{Revision: uint64(row.Revision), NodeID: row.NodeID, InstallationID: row.InstallationID}
		var count int64
		err := tx.Model(&model.FleetPlacementFanout{}).Where("scope = 'default' AND scope_id = '1' AND pending").Count(&count).Error
		view.Pending = count > 0
		return err
	})
	return view, err
}

func (s *FleetPlacementService) ReadNode(ctx context.Context, actor *model.User, nodeID string) (FleetNodePlacementView, error) {
	var view FleetNodePlacementView
	if !fleetID(nodeID) {
		return view, controlplane.ErrInvalid
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", nodeID).Error; err != nil {
			return err
		}
		var row model.FleetNodePlacementPolicy
		if err := tx.Where("node_id = ? AND installation_id = ?", nodeID, node.InstallationID).Find(&row).Error; err != nil {
			return err
		}
		view = FleetNodePlacementView{NodeID: node.ID, InstallationID: node.InstallationID, Revision: uint64(row.Revision), Enabled: row.Enabled, Labels: map[string]string{}, InboundIDs: []int{}, SHA256: hex.EncodeToString(row.Digest)}
		if row.Revision > 0 && (json.Unmarshal([]byte(row.Labels), &view.Labels) != nil || json.Unmarshal([]byte(row.InboundIDs), &view.InboundIDs) != nil) {
			return controlplane.ErrRecovery
		}
		var count int64
		err := tx.Model(&model.FleetPlacementFanout{}).Where("scope = 'node' AND scope_id = ? AND pending", node.ID+"/"+node.InstallationID).Count(&count).Error
		view.Pending = count > 0
		return err
	})
	return view, err
}

func (s *FleetPlacementService) ListRules(ctx context.Context, actor *model.User, after string, limit int) ([]FleetPlacementRuleView, error) {
	if after != "" && !fleetID(after) || limit < 1 || limit > 200 {
		return nil, controlplane.ErrInvalid
	}
	views := []FleetPlacementRuleView{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		query := tx.Order("id").Limit(limit)
		if after != "" {
			query = query.Where("id > ?", after)
		}
		var rows []model.FleetPlacementRule
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			view := FleetPlacementRuleView{ID: row.ID, Revision: uint64(row.Revision), Scope: row.Scope, PlanID: row.PlanID, GroupID: row.GroupID, Enabled: row.Enabled, SHA256: hex.EncodeToString(row.Digest)}
			if json.Unmarshal([]byte(row.NodeIDs), &view.NodeIDs) != nil || json.Unmarshal([]byte(row.Labels), &view.Labels) != nil || json.Unmarshal([]byte(row.InboundIDs), &view.InboundIDs) != nil {
				return controlplane.ErrRecovery
			}
			var count int64
			if err := tx.Model(&model.FleetPlacementFanout{}).Where("scope = 'rule' AND scope_id = ? AND pending", row.ID).Count(&count).Error; err != nil {
				return err
			}
			view.Pending = count > 0
			views = append(views, view)
		}
		return nil
	})
	return views, err
}
