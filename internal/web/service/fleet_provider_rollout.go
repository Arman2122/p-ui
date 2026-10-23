package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type fleetProviderHold struct{ Code string }

func (hold fleetProviderHold) Error() string { return "provider rollout: " + hold.Code }

func (s *FleetProviderService) ProcessRollouts(ctx context.Context, maximum int) (int, error) {
	if maximum < 1 || maximum > 8 {
		return 0, ErrFleetProvider
	}
	for count := range maximum {
		var work model.FleetProviderRollout
		err := s.transaction(ctx, func(tx *gorm.DB) error {
			now := s.now().UnixMilli()
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("pending AND ready_unix_milli <= ? AND lease_until_unix_milli <= ?", now, now).
				Order("ready_unix_milli, node_id").First(&work).Error; err != nil {
				return err
			}
			lease := uuid.NewString()
			work.LeaseID, work.LeaseUntilUnixMilli = &lease, now+90_000
			return tx.Model(&work).Updates(map[string]any{"lease_id": lease, "lease_until_unix_milli": work.LeaseUntilUnixMilli, "ready_unix_milli": work.LeaseUntilUnixMilli}).Error
		})
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return count, nil
		}
		if err != nil {
			return count, err
		}
		err = s.processRollout(ctx, work)
		var hold fleetProviderHold
		if errors.As(err, &hold) {
			err = s.transaction(ctx, func(tx *gorm.DB) error {
				return tx.Model(&model.FleetProviderRollout{}).Where("node_id = ? AND revision = ? AND lease_id = ?", work.NodeID, work.Revision, *work.LeaseID).
					Updates(map[string]any{"lease_id": nil, "lease_until_unix_milli": 0, "ready_unix_milli": s.now().Add(time.Second).UnixMilli(), "diagnostic": hold.Code}).Error
			})
		}
		if err != nil {
			return count + 1, err
		}
	}
	return maximum, nil
}

func (s *FleetProviderService) processRollout(parent context.Context, claim model.FleetProviderRollout) error {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	return s.transaction(ctx, func(tx *gorm.DB) error {
		if err := lockFleetNode(tx, claim.NodeID, claim.InstallationID); err != nil {
			if errors.Is(err, ErrFleetSource) {
				return fleetProviderHold{"NODE_SCOPE_UNAVAILABLE"}
			}
			return err
		}
		state, err := loadFleetNetworkState(tx, claim.NodeID, claim.InstallationID)
		if err != nil {
			return err
		}
		if !state.Node.Enabled {
			return fleetProviderHold{"NODE_DISABLED"}
		}
		if state.Node.AppliedRevision != state.Node.DesiredRevision {
			return fleetProviderHold{"AWAITING_APPLIED"}
		}
		base, err := loadFleetProviderBase(tx, state.Node)
		if err != nil {
			return err
		}
		if state.Head.Pending || state.Head.PublishedCompilationID == nil || *state.Head.PublishedCompilationID != base.Compilation.ID ||
			state.Head.CurrentRevisionID != base.Compilation.NodeRevisionID || state.Head.DesiredDefaultsID != base.Compilation.DefaultsRevisionID {
			return fleetProviderHold{"MANUAL_NETWORK_PENDING"}
		}
		preview, err := previewFleetNetworkTx(tx, FleetNetworkPreviewRequest{
			NodeID: state.Node.ID, InstallationID: state.Node.InstallationID,
			ExpectedRevision: uint64(state.Head.Revision), ExpectedDefaultsID: state.Head.DesiredDefaultsID,
		}, 0)
		if err != nil {
			return err
		}
		if !preview.Ready {
			code := "NETWORK_DEPENDENCY_PENDING"
			if len(preview.Diagnostics) != 0 {
				code = preview.Diagnostics[0].Code
			}
			return fleetProviderHold{code}
		}
		var compiled model.FleetNetworkCompilation
		if err := tx.First(&compiled, "id = ?", preview.ID).Error; err != nil {
			return err
		}
		var current FleetNetworkManifest
		if json.Unmarshal([]byte(compiled.Manifest), &current) != nil {
			return ErrFleetNetwork
		}
		before, err := fleetProviderSourceDigest(base.Manifest)
		if err != nil {
			return err
		}
		after, err := fleetProviderSourceDigest(current)
		if err != nil || !bytes.Equal(before, after) {
			return fleetProviderHold{"SOURCE_SCOPE_CHANGED"}
		}
		var work model.FleetProviderRollout
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&work, "node_id = ?", claim.NodeID).Error; err != nil {
			return err
		}
		if !work.Pending || work.Revision != claim.Revision || work.LeaseID == nil || claim.LeaseID == nil || *work.LeaseID != *claim.LeaseID || work.LeaseUntilUnixMilli <= s.now().UnixMilli() {
			return fleetProviderHold{"WORK_SUPERSEDED"}
		}
		if work.BaseCompilationID == base.Compilation.ID && (!bytes.Equal(work.BaseDigest, base.Deployment.Digest) || !bytes.Equal(work.SourceDigest, before) ||
			work.NodeRevisionID != base.Compilation.NodeRevisionID || work.DefaultsRevisionID != base.Compilation.DefaultsRevisionID) {
			return fleetProviderHold{"BASE_SCOPE_CHANGED"}
		}
		expected, satisfied, err := providerRolloutPins(base.Manifest.Providers, work.Targets)
		if err != nil {
			return err
		}
		if !equalProviderPins(expected, current.Providers) {
			return fleetProviderHold{"PROVIDER_TARGET_CHANGED"}
		}
		if work.BaseCompilationID != base.Compilation.ID {
			if work.Revision == math.MaxInt64 {
				return ErrFleetNetworkRevision
			}
			receiptID := uuid.NewString()
			_, digest, err := canonicalFleetArtifact(struct {
				Node, Old, New string
				Revision       int64
			}{work.NodeID, work.BaseCompilationID, base.Compilation.ID, work.Revision}, 2048)
			if err != nil {
				return err
			}
			work.Revision++
			work.OperationID, work.BaseCompilationID, work.BaseDeploymentRevision = uuid.NewString(), base.Compilation.ID, state.Node.AppliedRevision
			work.NodeRevisionID, work.DefaultsRevisionID, work.BaseDigest, work.SourceDigest = base.Compilation.NodeRevisionID, base.Compilation.DefaultsRevisionID, base.Deployment.Digest, before
			if err := saveProviderOperation(tx, receiptID, digest, struct {
				Node     string
				Revision int64
				Base     string
			}{work.NodeID, work.Revision, work.BaseCompilationID}); err != nil {
				return err
			}
		}
		work.LeaseID, work.LeaseUntilUnixMilli, work.Diagnostic = nil, 0, ""
		work.ReadyUnixMilli = s.now().Add(time.Second).UnixMilli()
		if satisfied {
			work.Pending, work.PublishedRevision, work.PublishedCompilationID = false, state.Node.AppliedRevision, &base.Compilation.ID
			return tx.Save(&work).Error
		}
		publication, err := publishFleetNetworkTx(ctx, tx, FleetNetworkPublishRequest{OperationID: work.OperationID, CompilationID: preview.ID}, nil)
		if err != nil {
			return err
		}
		work.PublishedRevision, work.PublishedCompilationID = int64(publication.DeploymentRevision), &publication.CompilationID
		return tx.Save(&work).Error
	})
}

func providerRolloutPins(base []FleetNetworkProviderPin, raw string) ([]FleetNetworkProviderPin, bool, error) {
	var targets map[string]FleetNetworkProviderPin
	if json.Unmarshal([]byte(raw), &targets) != nil || len(targets) == 0 || len(targets) > 1024 {
		return nil, false, ErrFleetNetwork
	}
	expected := make([]FleetNetworkProviderPin, len(base))
	copy(expected, base)
	satisfied := true
	for index, pin := range base {
		key := providerPinKey(pin)
		target, exists := targets[key]
		if !exists {
			continue
		}
		if !pin.FollowLatest || !target.FollowLatest || providerPinKey(target) != key {
			return nil, false, fleetProviderHold{"FOLLOW_BINDING_CHANGED"}
		}
		if target.Revision > pin.Revision {
			expected[index], satisfied = target, false
		} else if target.Revision == pin.Revision && target != pin {
			return nil, false, fleetProviderHold{"PROVIDER_TARGET_CHANGED"}
		}
		delete(targets, key)
	}
	if len(targets) != 0 {
		return nil, false, fleetProviderHold{"FOLLOW_BINDING_CHANGED"}
	}
	return expected, satisfied, nil
}
