package controlplane

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/diagnostics"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func (s *Store) Heartbeat(ctx context.Context, p identity.Principal, generation uint64, heartbeat *nodev1.Heartbeat) error {
	if heartbeat == nil || generation == 0 || generation > math.MaxInt64 || heartbeat.ObservedUnixMilli <= 0 || heartbeat.AppliedRevision > math.MaxInt64 {
		return ErrInvalid
	}
	conditions, err := diagnostics.Conditions(heartbeat.Conditions)
	if err != nil {
		return ErrInvalid
	}
	encoded, err := json.Marshal(conditions)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		if err := lockAuthenticatedNode(tx, p); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", p.NodeID).Error; err != nil {
			return err
		}
		if uint64(node.AuthorityGeneration) != generation {
			return ErrScope
		}
		if heartbeat.AppliedRevision > uint64(node.DesiredRevision) {
			return ErrRevision
		}
		now := time.Now().UnixMilli()
		observation := model.FleetNodeObservation{
			NodeID: p.NodeID, RuntimeGeneration: int64(generation), ReceivedUnixMilli: now,
			ObservedUnixMilli: heartbeat.ObservedUnixMilli, ReportedAppliedRevision: int64(heartbeat.AppliedRevision), ConditionsJSON: string(encoded),
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "node_id"}}, DoUpdates: clause.AssignmentColumns([]string{
			"runtime_generation", "received_unix_milli", "observed_unix_milli", "reported_applied_revision", "conditions_json",
		})}).Create(&observation).Error; err != nil {
			return err
		}
		return tx.Model(&node).Update("last_seen_unix_milli", now).Error
	})
}

func recordApplyObservation(tx *gorm.DB, nodeID string, generation uint64, result *nodev1.ApplyResult) error {
	var current model.FleetNodeObservation
	if err := tx.Find(&current, "node_id = ?", nodeID).Error; err != nil {
		return err
	}
	if current.ResultRevision > int64(result.Revision) || current.ResultRevision == int64(result.Revision) && current.ResultGeneration == int64(generation) &&
		(current.ResultStage == int32(nodev1.ApplyStage_APPLY_STAGE_APPLIED) || result.Stage == nodev1.ApplyStage_APPLY_STAGE_RECEIVED && current.ResultStage == int32(nodev1.ApplyStage_APPLY_STAGE_REJECTED)) {
		return nil
	}
	code := ""
	if result.Stage == nodev1.ApplyStage_APPLY_STAGE_REJECTED {
		code = "APPLY_FAILED"
	}
	observation := model.FleetNodeObservation{
		NodeID: nodeID, ResultGeneration: int64(generation), ResultRevision: int64(result.Revision),
		ResultStage: int32(result.Stage), ResultCode: code, ResultReceivedUnixMilli: time.Now().UnixMilli(), ConditionsJSON: "[]",
	}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "node_id"}}, DoUpdates: clause.AssignmentColumns([]string{
		"result_generation", "result_revision", "result_stage", "result_code", "result_received_unix_milli",
	})}).Create(&observation).Error
}

func (s *Store) Observations(ctx context.Context, nodes []string) (map[string]model.FleetNodeObservation, error) {
	if len(nodes) > 200 {
		return nil, ErrInvalid
	}
	result := make(map[string]model.FleetNodeObservation, len(nodes))
	if len(nodes) == 0 {
		return result, nil
	}
	var rows []model.FleetNodeObservation
	if err := s.db.WithContext(ctx).Where("node_id IN ?", nodes).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.NodeID] = row
	}
	return result, nil
}
