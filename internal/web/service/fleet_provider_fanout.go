package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func queueFleetProviderRollout(tx *gorm.DB, node model.FleetNode, base fleetProviderBase, updates []FleetNetworkProviderPin) error {
	if len(updates) == 0 {
		return nil
	}
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.fleet-provider-rollout:' || ?,0))", node.ID).Error; err != nil {
		return err
	}
	var work model.FleetProviderRollout
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&work, "node_id = ?", node.ID).Error; err != nil {
		return err
	}
	targets := map[string]FleetNetworkProviderPin{}
	if work.Revision != 0 && work.InstallationID == node.InstallationID {
		if json.Unmarshal([]byte(work.Targets), &targets) != nil {
			return ErrFleetNetwork
		}
	} else {
		digest, err := fleetProviderSourceDigest(base.Manifest)
		if err != nil {
			return err
		}
		work = model.FleetProviderRollout{
			NodeID: node.ID, Revision: work.Revision, InstallationID: node.InstallationID, BaseCompilationID: base.Compilation.ID, NodeRevisionID: base.Compilation.NodeRevisionID,
			DefaultsRevisionID: base.Compilation.DefaultsRevisionID, BaseDeploymentRevision: node.AppliedRevision, BaseDigest: bytes.Clone(base.Deployment.Digest), SourceDigest: digest,
		}
	}
	changed := false
	for _, pin := range updates {
		key := providerPinKey(pin)
		old, exists := targets[key]
		if !exists || pin.Revision > old.Revision {
			targets[key], changed = pin, true
		} else if pin.Revision == old.Revision && pin != old {
			return ErrFleetNetworkRevision
		}
	}
	if !changed {
		return nil
	}
	if work.Revision == math.MaxInt64 {
		return ErrFleetNetworkRevision
	}
	encoded, _, err := canonicalFleetArtifact(targets, 1<<20)
	if err != nil {
		return err
	}
	work.Revision++
	work.OperationID, work.Targets, work.Pending, work.ReadyUnixMilli = uuid.NewString(), string(encoded), true, 0
	work.LeaseID, work.LeaseUntilUnixMilli, work.Diagnostic = nil, 0, ""
	return tx.Save(&work).Error
}

func providerTargets(base fleetProviderBase, source model.FleetOutboundProvider, cached model.FleetOutboundProviderRevision) ([]FleetNetworkProviderPin, error) {
	var targets []FleetNetworkProviderPin
	for _, pin := range base.Manifest.Providers {
		if pin.ProviderID != source.ID || !pin.FollowLatest || pin.Revision >= uint64(cached.Revision) {
			continue
		}
		if source.Scope == "node" && (source.NodeID == nil || source.InstallationID == nil || *source.NodeID != base.Manifest.NodeID || *source.InstallationID != base.Manifest.InstallationID) {
			return nil, ErrFleetNetworkRevision
		}
		pin.Revision, pin.ConfigurationRevision, pin.SHA256 = uint64(cached.Revision), uint64(cached.ConfigurationRevision), hex.EncodeToString(cached.Digest)
		targets = append(targets, pin)
	}
	return targets, nil
}

func (s *FleetProviderService) FanoutDue(ctx context.Context, maximum int) (int, error) {
	if maximum < 1 || maximum > 8 {
		return 0, ErrFleetProvider
	}
	for count := range maximum {
		err := s.transaction(ctx, func(tx *gorm.DB) error {
			var source model.FleetOutboundProvider
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("fanout_pending AND fanout_ready_unix_milli <= ?", s.now().UnixMilli()).Order("fanout_ready_unix_milli, id").First(&source).Error; err != nil {
				return err
			}
			if source.FanoutRevision != source.CachedRevision || source.CachedRevision == 0 {
				return ErrFleetProviderRevision
			}
			cached, err := loadProviderCache(tx, source.ID, source.FanoutRevision)
			if deferFleetProviderScope(err) {
				return tx.Model(&source).Updates(map[string]any{"fanout_ready_unix_milli": s.now().Add(time.Second).UnixMilli(), "diagnostic": "FANOUT_CACHE_UNAVAILABLE"}).Error
			}
			if err != nil {
				return err
			}
			query := tx.Table("fleet_published_provider_bindings b").Select("DISTINCT b.node_id").
				Joins("JOIN fleet_nodes n ON n.id = b.node_id AND n.installation_id = b.installation_id AND n.applied_revision = b.deployment_revision").
				Where("b.provider_id = ? AND b.follow_latest AND b.scope_known", source.ID).Order("b.node_id").Limit(32)
			if source.FanoutAfterNode != nil {
				query = query.Where("b.node_id > ?", *source.FanoutAfterNode)
			}
			var ids []string
			if err := query.Pluck("b.node_id", &ids).Error; err != nil {
				return err
			}
			for _, id := range ids {
				var node model.FleetNode
				if err := tx.First(&node, "id = ?", id).Error; err != nil {
					return err
				}
				base, err := loadFleetProviderBase(tx, node)
				if deferFleetProviderScope(err) {
					if err := deferFleetProviderBindings(tx, node.ID, s.now()); err != nil {
						return err
					}
					continue
				}
				if err != nil {
					return err
				}
				targets, err := providerTargets(base, source, cached)
				if err != nil {
					return err
				}
				if err := queueFleetProviderRollout(tx, node, base, targets); err != nil {
					return err
				}
			}
			updates := map[string]any{"fanout_pending": len(ids) == 32}
			if len(ids) != 0 {
				updates["fanout_after_node"] = ids[len(ids)-1]
			}
			return tx.Model(&source).Updates(updates).Error
		})
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return count, nil
		}
		if err != nil {
			return count, err
		}
	}
	return maximum, nil
}

func (s *FleetProviderService) WakeApplied(ctx context.Context, maximum int) (int, error) {
	if maximum < 1 || maximum > 32 {
		return 0, ErrFleetProvider
	}
	var ids []string
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		return tx.Table("fleet_published_provider_bindings b").Select("DISTINCT b.node_id").
			Joins("JOIN fleet_nodes n ON n.id = b.node_id AND n.installation_id = b.installation_id AND n.applied_revision >= b.deployment_revision").
			Where("b.pending AND b.ready_unix_milli <= ?", s.now().UnixMilli()).Order("b.node_id").Limit(maximum).Pluck("b.node_id", &ids).Error
	})
	if err != nil {
		return 0, err
	}
	for index, id := range ids {
		err := s.wakeAppliedNode(ctx, id)
		if deferFleetProviderScope(err) {
			err = s.transaction(ctx, func(tx *gorm.DB) error { return deferFleetProviderBindings(tx, id, s.now()) })
		}
		if err != nil {
			return index, err
		}
	}
	return len(ids), nil
}

func (s *FleetProviderService) wakeAppliedNode(ctx context.Context, id string) error {
	return s.transaction(ctx, func(tx *gorm.DB) error {
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", id).Error; err != nil {
			return err
		}
		base, err := loadFleetProviderBase(tx, node)
		if err != nil {
			return err
		}
		providers := []string{}
		for _, pin := range base.Manifest.Providers {
			if pin.FollowLatest {
				providers = append(providers, pin.ProviderID)
			}
		}
		slices.Sort(providers)
		var updates []FleetNetworkProviderPin
		for _, id := range slices.Compact(providers) {
			var source model.FleetOutboundProvider
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&source, "id = ?", id).Error; err != nil {
				return err
			}
			cached, err := loadProviderCache(tx, source.ID, source.CachedRevision)
			if err != nil {
				return err
			}
			targets, err := providerTargets(base, source, cached)
			if err != nil {
				return err
			}
			updates = append(updates, targets...)
		}
		if err := queueFleetProviderRollout(tx, node, base, updates); err != nil {
			return err
		}
		return tx.Model(&model.FleetPublishedProviderBinding{}).Where("node_id = ? AND installation_id = ? AND deployment_revision <= ?", node.ID, node.InstallationID, node.AppliedRevision).
			Updates(map[string]any{"pending": false, "diagnostic": ""}).Error
	})
}

func deferFleetProviderScope(err error) bool {
	return errors.Is(err, ErrFleetNetworkRevision) || errors.Is(err, ErrFleetProviderFetch) || errors.Is(err, gorm.ErrRecordNotFound)
}

func deferFleetProviderBindings(tx *gorm.DB, nodeID string, now time.Time) error {
	return tx.Model(&model.FleetPublishedProviderBinding{}).Where(`node_id = ? AND EXISTS (SELECT 1 FROM fleet_nodes n WHERE n.id = fleet_published_provider_bindings.node_id
AND n.installation_id = fleet_published_provider_bindings.installation_id AND n.applied_revision = fleet_published_provider_bindings.deployment_revision)`, nodeID).
		Updates(map[string]any{"pending": true, "ready_unix_milli": now.Add(time.Second).UnixMilli(), "diagnostic": "PUBLICATION_SCOPE_UNAVAILABLE"}).Error
}
