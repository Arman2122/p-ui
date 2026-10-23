package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func (s *Store) Publish(ctx context.Context, nodeID string, payload []byte) (uint64, error) {
	if !validID(nodeID) || len(payload) == 0 || len(payload) > 32<<20 {
		return 0, ErrInvalid
	}
	bundle, err := configuration.Decode(payload)
	if err != nil {
		return s.publish(ctx, nodeID, payload)
	}
	var revision uint64
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", nodeID).Error; err != nil {
			return err
		}
		revision, err = (&Store{db: tx}).PublishConfiguration(ctx, identity.Principal{NodeID: node.ID, InstallationID: node.InstallationID}, bundle)
		return err
	})
	return revision, err
}

func (s *Store) publish(ctx context.Context, nodeID string, payload []byte) (uint64, error) {
	if !validID(nodeID) || len(payload) == 0 || len(payload) > 32<<20 {
		return 0, ErrInvalid
	}
	digest := sha256.Sum256(payload)
	var revision uint64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", nodeID).Error; err != nil {
			return err
		}
		if bundle, err := configuration.Decode(payload); err == nil {
			if err := guardManagedBundle(tx, nodeID, bundle); err != nil {
				return err
			}
		}
		var current model.FleetDeployment
		result := tx.Find(&current, "node_id = ? AND revision = ?", nodeID, node.DesiredRevision)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 0 && bytes.Equal(current.Digest, digest[:]) {
			revision = uint64(current.Revision)
			return indexDeploymentAdmissions(tx, node, current)
		}
		if node.DesiredRevision == math.MaxInt64 {
			return ErrRevision
		}
		node.DesiredRevision++
		deployment := model.FleetDeployment{NodeID: nodeID, Revision: node.DesiredRevision, Digest: digest[:], Payload: payload}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&deployment).Error; err != nil {
			return err
		}
		if err := tx.Model(&node).Update("desired_revision", node.DesiredRevision).Error; err != nil {
			return err
		}
		if err := indexDeploymentAdmissions(tx, node, deployment); err != nil {
			return err
		}
		if err := pruneDeployments(tx, node); err != nil {
			return err
		}
		revision = uint64(node.DesiredRevision)
		return nil
	})
	return revision, err
}

func (s *Store) BeginSession(ctx context.Context, p identity.Principal, hello *nodev1.NodeHello) (uint64, error) {
	if hello == nil || p.Require(hello.NodeId, hello.InstallationId) != nil {
		return 0, ErrScope
	}
	capabilities, err := canonicalPlacementCapabilities(hello.Capabilities)
	if err != nil {
		return 0, err
	}
	if err := s.RequireAgentJournal(ctx, p, hello.AgentStateJournalId); err != nil {
		return 0, err
	}
	var generation uint64
	var recovery bool
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, true); err != nil {
			return err
		}
		if err := lockAuthenticatedNode(tx, p); err != nil {
			return err
		}
		if err := requireNodeJournalTx(tx, p.NodeID, p.InstallationID, hello.AgentStateJournalId); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", p.NodeID).Error; err != nil {
			return err
		}
		var cursor model.FleetUsageCursor
		if err := tx.Find(&cursor, "node_id = ? AND installation_id = ?", p.NodeID, p.InstallationID).Error; err != nil {
			return err
		}
		if hello.AuthorityGeneration > uint64(node.AuthorityGeneration) || hello.DesiredRevision > uint64(node.DesiredRevision) ||
			hello.AppliedRevision > uint64(node.DesiredRevision) || hello.DurableUsageSequence > uint64(cursor.Sequence) {
			recovery = true
			return tx.Model(&model.FleetFence{}).Where("id = 1").Updates(map[string]any{
				"recovery_required": true, "reason": "NODE_HIGH_WATER_AHEAD", "witness_node_id": p.NodeID,
			}).Error
		}
		if node.AuthorityGeneration == math.MaxInt64 {
			return ErrRevision
		}
		node.AuthorityGeneration++
		if err := recordPlacementCapabilitiesTx(tx, node, capabilities, time.Now().UnixMilli()); err != nil {
			return err
		}
		if err := tx.Model(&node).Updates(map[string]any{"authority_generation": node.AuthorityGeneration, "sent_revision": 0, "last_seen_unix_milli": time.Now().UnixMilli()}).Error; err != nil {
			return err
		}
		generation = uint64(node.AuthorityGeneration)
		return nil
	})
	if err == nil && recovery {
		return 0, ErrRecovery
	}
	return generation, err
}

func (s *Store) Delivery(ctx context.Context, p identity.Principal, generation uint64) (*nodev1.Deployment, error) {
	if generation == 0 || generation > math.MaxInt64 {
		return nil, ErrScope
	}
	var result *nodev1.Deployment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		if err := lockAuthenticatedNode(tx, p); err != nil {
			return err
		}
		if err := requireNodeJournalTx(tx, p.NodeID, p.InstallationID, ""); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", p.NodeID).Error; err != nil {
			return err
		}
		if uint64(node.AuthorityGeneration) != generation {
			return ErrScope
		}
		if node.DesiredRevision == 0 {
			return nil
		}
		var d model.FleetDeployment
		if err := tx.First(&d, "node_id = ? AND revision = ?", p.NodeID, node.DesiredRevision).Error; err != nil {
			return err
		}
		node.SentRevision = d.Revision
		if err := tx.Model(&node).Update("sent_revision", node.SentRevision).Error; err != nil {
			return err
		}
		if err := pruneDeployments(tx, node); err != nil {
			return err
		}
		result = &nodev1.Deployment{
			NodeId: p.NodeID, ControlPlaneId: p.ControlPlaneID, AuthorityGeneration: generation,
			Revision: uint64(d.Revision), Payload: d.Payload, PayloadSha256: d.Digest,
		}
		return nil
	})
	return result, err
}

func (s *Store) Observe(ctx context.Context, p identity.Principal, generation uint64, result *nodev1.ApplyResult) error {
	if result == nil || generation == 0 || generation > math.MaxInt64 || result.AuthorityGeneration != generation ||
		result.Revision == 0 || result.Revision > math.MaxInt64 {
		return ErrInvalid
	}
	if result.Stage != nodev1.ApplyStage_APPLY_STAGE_RECEIVED && result.Stage != nodev1.ApplyStage_APPLY_STAGE_APPLIED && result.Stage != nodev1.ApplyStage_APPLY_STAGE_REJECTED {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		if err := lockAuthenticatedNode(tx, p); err != nil {
			return err
		}
		if err := requireNodeJournalTx(tx, p.NodeID, p.InstallationID, ""); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", p.NodeID).Error; err != nil {
			return err
		}
		if uint64(node.AuthorityGeneration) != generation {
			return ErrScope
		}
		var deployment model.FleetDeployment
		if err := tx.First(&deployment, "node_id = ? AND revision = ?", p.NodeID, result.Revision).Error; err != nil {
			return err
		}
		if uint64(deployment.Revision) != result.Revision || !bytes.Equal(deployment.Digest, result.PayloadSha256) {
			return ErrRevision
		}
		values := map[string]any{"last_seen_unix_milli": time.Now().UnixMilli()}
		if result.Stage == nodev1.ApplyStage_APPLY_STAGE_APPLIED {
			if !bytes.Equal(result.AssetManifestSha256, configuration.PayloadAssetManifest(deployment.Payload)) {
				return ErrRevision
			}
			if err := acknowledgeConfiguration(tx, p, deployment.Payload); err != nil {
				return err
			}
			if deployment.Revision == node.DesiredRevision && deployment.Revision >= node.AppliedRevision {
				if err := clearAppliedEnabledStops(tx, p, deployment.Revision, deployment.Payload); err != nil {
					return err
				}
				if err := recordStoppedGrants(tx, p.NodeID, deployment.Revision, deployment.Payload); err != nil {
					return err
				}
			}
			if err := supersedeAppliedOutboxes(tx, p, deployment.Revision, deployment.Payload); err != nil {
				return err
			}
		}
		if result.Stage == nodev1.ApplyStage_APPLY_STAGE_APPLIED && int64(result.Revision) > node.AppliedRevision {
			values["applied_revision"] = int64(result.Revision)
			node.AppliedRevision = int64(result.Revision)
		}
		if err := tx.Model(&node).Updates(values).Error; err != nil {
			return err
		}
		if err := recordApplyObservation(tx, p.NodeID, generation, result); err != nil {
			return err
		}
		return pruneDeployments(tx, node)
	})
}

func pruneDeployments(tx *gorm.DB, node model.FleetNode) error {
	if err := pruneAdmissionProjections(tx, node); err != nil {
		return err
	}
	return tx.Where("node_id = ? AND revision NOT IN ?", node.ID, []int64{node.DesiredRevision, node.AppliedRevision, node.SentRevision}).Delete(&model.FleetDeployment{}).Error
}

func (s *Store) CheckSession(ctx context.Context, p identity.Principal, generation uint64, touch bool) error {
	if generation == 0 || generation > math.MaxInt64 {
		return ErrScope
	}
	var fence model.FleetFence
	if err := s.db.WithContext(ctx).First(&fence, "id = 1").Error; err != nil {
		return err
	}
	if fence.RecoveryRequired {
		return ErrRecovery
	}
	if err := authorizeRegisteredPeer(s.db.WithContext(ctx), p); err != nil {
		return err
	}
	if err := requireNodeJournalTx(s.db.WithContext(ctx), p.NodeID, p.InstallationID, ""); err != nil {
		return err
	}
	query := s.db.WithContext(ctx).Model(&model.FleetNode{}).Where("id = ? AND installation_id = ? AND enabled = ? AND authority_generation = ?", p.NodeID, p.InstallationID, true, int64(generation))
	if touch {
		result := query.Update("last_seen_unix_milli", time.Now().UnixMilli())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrScope
		}
		return nil
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrScope
	}
	return nil
}
