package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"math"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type FleetNetworkService struct{ db *gorm.DB }

func NewFleetNetworkService(db *gorm.DB) (*FleetNetworkService, error) {
	if db == nil || db.Name() != "postgres" {
		return nil, ErrFleetNetwork
	}
	return &FleetNetworkService{db: db.Session(&gorm.Session{Logger: logger.Discard})}, nil
}

type FleetNetworkRevisionView struct {
	ID               string `json:"id"`
	Revision         uint64 `json:"revision"`
	SHA256           string `json:"sha256"`
	CreatedUnixMilli int64  `json:"createdUnixMilli"`
}

type FleetNodeNetworkRequest struct {
	NodeID           string                  `json:"nodeId" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	InstallationID   string                  `json:"installationId" example:"948a18c5-a5d0-4c28-8052-d46224088bd6"`
	ExpectedRevision uint64                  `json:"expectedRevision" example:"1"`
	FollowDefaults   *bool                   `json:"followDefaults,omitempty" example:"true"`
	PinnedDefaultsID string                  `json:"pinnedDefaultsId,omitempty" example:"b84cf341-f5e1-4dc6-b520-63a7d0e4b970"`
	Operations       []FleetNetworkOperation `json:"operations"`
}

type FleetNodeNetworkView struct {
	NodeID         string                   `json:"nodeId"`
	InstallationID string                   `json:"installationId"`
	Revision       FleetNetworkRevisionView `json:"revision"`
	Defaults       FleetNetworkRevisionView `json:"defaults"`
	FollowDefaults bool                     `json:"followDefaults"`
	Pending        bool                     `json:"pending"`
	Origins        map[string]string        `json:"origins"`
}

func networkRevisionView(row model.FleetNetworkRevision) FleetNetworkRevisionView {
	return FleetNetworkRevisionView{ID: row.ID, Revision: uint64(row.Revision), SHA256: hex.EncodeToString(row.Digest), CreatedUnixMilli: row.CreatedUnixMilli}
}

func lockFleetNetworkNode(tx *gorm.DB, nodeID, installationID string) error {
	if !fleetID(nodeID) || !fleetID(installationID) {
		return ErrFleetNetwork
	}
	var node model.FleetNode
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", nodeID).Error; err != nil {
		return err
	}
	if node.InstallationID != installationID {
		return ErrFleetNetworkRevision
	}
	return nil
}

func (s *FleetNetworkService) ApproveDefaults(ctx context.Context, actor *model.User, expected uint64, spec FleetNetworkSpec) (FleetNetworkRevisionView, error) {
	var result FleetNetworkRevisionView
	if expected > math.MaxInt64 {
		return result, ErrFleetNetworkRevision
	}
	if err := validateFleetNetwork(spec); err != nil {
		return result, err
	}
	encoded, digest, err := canonicalFleetNetwork(spec)
	if err != nil {
		return result, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.fleet-network-defaults',0))").Error; err != nil {
			return err
		}
		var head model.FleetNetworkDefaultsHead
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&head, "id = 1").Error; err != nil {
			return err
		}
		if head.Revision != 0 {
			var previous model.FleetNetworkRevision
			if err := tx.First(&previous, "id = ?", head.CurrentRevisionID).Error; err != nil {
				return err
			}
			if bytes.Equal(previous.Digest, digest) && (expected == uint64(head.Revision) || expected+1 == uint64(head.Revision)) {
				result = networkRevisionView(previous)
				return nil
			}
		}
		if uint64(head.Revision) != expected || head.Revision == math.MaxInt64 {
			return ErrFleetNetworkRevision
		}
		row := model.FleetNetworkRevision{ID: uuid.NewString(), Scope: "defaults", Revision: head.Revision + 1, Specification: string(encoded), Digest: digest, CreatedUnixMilli: time.Now().UnixMilli(), ActorID: actor.Id}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		head = model.FleetNetworkDefaultsHead{ID: 1, Revision: row.Revision, CurrentRevisionID: row.ID}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&head).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.FleetNodeNetworkHead{}).Where("follow_defaults").Updates(map[string]any{"desired_defaults_id": row.ID, "pending": true}).Error; err != nil {
			return err
		}
		result = networkRevisionView(row)
		return nil
	})
	return result, err
}

func loadNetworkRevision(tx *gorm.DB, id, scope string) (model.FleetNetworkRevision, error) {
	var row model.FleetNetworkRevision
	if !fleetID(id) {
		return row, ErrFleetNetworkRevision
	}
	err := tx.First(&row, "id = ? AND scope = ?", id, scope).Error
	return row, err
}

func (s *FleetNetworkService) ConfigureNode(ctx context.Context, actor *model.User, request FleetNodeNetworkRequest) (FleetNodeNetworkView, error) {
	var result FleetNodeNetworkView
	if request.ExpectedRevision > math.MaxInt64 {
		return result, ErrFleetNetworkRevision
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		if err := lockFleetNetworkNode(tx, request.NodeID, request.InstallationID); err != nil {
			return err
		}
		var global model.FleetNetworkDefaultsHead
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&global, "id = 1").Error; err != nil {
			return err
		}
		var head model.FleetNodeNetworkHead
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&head, "node_id = ?", request.NodeID).Error; err != nil {
			return err
		}
		if head.Revision != 0 && head.InstallationID != request.InstallationID {
			return ErrFleetNetworkRevision
		}
		if uint64(head.Revision) != request.ExpectedRevision || head.Revision == math.MaxInt64 {
			return ErrFleetNetworkRevision
		}
		overrides := FleetNetworkOverrides{Version: 1, FollowDefaults: true}
		if head.CurrentRevisionID != "" {
			previous, err := loadNetworkRevision(tx, head.CurrentRevisionID, "node")
			if err != nil {
				return err
			}
			if json.Unmarshal([]byte(previous.Specification), &overrides) != nil {
				return ErrFleetNetwork
			}
		}
		if request.FollowDefaults != nil {
			overrides.FollowDefaults = *request.FollowDefaults
			if overrides.FollowDefaults {
				overrides.PinnedDefaultsID = ""
			} else {
				overrides.PinnedDefaultsID = request.PinnedDefaultsID
			}
		} else if request.PinnedDefaultsID != "" {
			return ErrFleetNetwork
		}
		if overrides.FollowDefaults && request.PinnedDefaultsID != "" {
			return ErrFleetNetwork
		}
		var err error
		overrides, err = applyFleetNetworkOperations(overrides, request.Operations)
		if err != nil {
			return err
		}
		defaultID := global.CurrentRevisionID
		if !overrides.FollowDefaults {
			defaultID = overrides.PinnedDefaultsID
		}
		defaults, err := loadNetworkRevision(tx, defaultID, "defaults")
		if err != nil {
			return err
		}
		var spec FleetNetworkSpec
		if json.Unmarshal([]byte(defaults.Specification), &spec) != nil {
			return ErrFleetNetwork
		}
		resolved, origins, err := resolveFleetNetwork(spec, overrides)
		if err != nil {
			return err
		}
		if err := validateFleetAssetBindings(tx, request.NodeID, request.InstallationID, resolved.AssetSlots, overrides.AssetBindings); err != nil {
			return err
		}
		encoded, digest, err := canonicalFleetNetwork(overrides)
		if err != nil {
			return err
		}
		row := model.FleetNetworkRevision{
			ID: uuid.NewString(), Scope: "node", NodeID: &request.NodeID, InstallationID: &request.InstallationID,
			Revision: head.Revision + 1, BasedOnDefaultsID: &defaultID, Specification: string(encoded), Digest: digest, CreatedUnixMilli: time.Now().UnixMilli(), ActorID: actor.Id,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		head.NodeID, head.InstallationID, head.Revision, head.CurrentRevisionID = request.NodeID, request.InstallationID, row.Revision, row.ID
		head.FollowDefaults, head.DesiredDefaultsID, head.Pending = overrides.FollowDefaults, defaultID, true
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&head).Error; err != nil {
			return err
		}
		result = FleetNodeNetworkView{NodeID: head.NodeID, InstallationID: head.InstallationID, Revision: networkRevisionView(row), Defaults: networkRevisionView(defaults), FollowDefaults: head.FollowDefaults, Pending: true, Origins: origins}
		return nil
	})
	return result, err
}

func validateFleetAssetBindings(tx *gorm.DB, nodeID, installationID string, slots map[string]string, bindings map[string]FleetAssetReference) error {
	if len(bindings) > 8192 {
		return ErrFleetNetwork
	}
	type identity struct {
		ID       string
		Revision int64
	}
	expected := make(map[identity]string, len(bindings))
	var keys [][]any
	for slot, reference := range bindings {
		if slots[slot] == "" || !fleetID(reference.AssetID) || reference.Revision == 0 || reference.Revision > math.MaxInt64 {
			return ErrFleetNetworkDependency
		}
		key := identity{reference.AssetID, int64(reference.Revision)}
		if kind, found := expected[key]; found && kind != slots[slot] {
			return ErrFleetNetworkDependency
		} else if !found {
			keys = append(keys, []any{reference.AssetID, int64(reference.Revision)})
		}
		expected[key] = slots[slot]
	}
	for offset := 0; offset < len(keys); offset += 512 {
		chunk := keys[offset:min(offset+512, len(keys))]
		var rows []model.FleetNetworkAssetRevision
		if err := tx.Select("asset_id, revision, node_id, installation_id, kind").Where("(asset_id,revision) IN ?", chunk).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(chunk) {
			return ErrFleetNetworkDependency
		}
		for _, asset := range rows {
			if asset.NodeID != nodeID || asset.InstallationID != installationID || asset.Kind != expected[identity{asset.AssetID, asset.Revision}] {
				return ErrFleetNetworkDependency
			}
		}
	}
	return nil
}
