package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

var ErrFleetNetworkApproval = fmt.Errorf("%w: an explicitly applied network baseline is required", ErrFleetNetworkDependency)

const fleetSourceNetworkApprovalRequired = "NETWORK_APPROVAL_REQUIRED"

func setFleetSourceApprovalHoldTx(tx *gorm.DB, work model.FleetSourceWork, held bool) error {
	updates := map[string]any{"diagnostic": ""}
	if held {
		updates["diagnostic"], updates["pending"] = fleetSourceNetworkApprovalRequired, true
		updates["available_unix_milli"] = gorm.Expr("GREATEST(available_unix_milli, ?)", time.Now().Add(time.Second).UnixMilli())
	}
	result := tx.Model(&model.FleetSourceWork{}).Where("placement_id = ? AND revision = ? AND operation_id = ? AND source_revision = ? AND pending AND enabled AND NOT requires_stop",
		work.PlacementID, work.Revision, work.OperationID, work.SourceRevision).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return controlplane.ErrRevision
	}
	return nil
}

func requireAppliedFleetNetworkTx(tx *gorm.DB, state fleetNetworkState) error {
	if state.Node.AppliedRevision <= 0 || state.Node.AppliedRevision != state.Node.DesiredRevision || state.Head.Pending || state.Head.PublishedCompilationID == nil ||
		state.Head.CurrentRevisionID != state.Revision.ID || state.Head.DesiredDefaultsID != state.Defaults.ID || state.Head.FollowDefaults != state.Overrides.FollowDefaults {
		return ErrFleetNetworkApproval
	}
	base, err := loadFleetProviderBase(tx, state.Node)
	if errors.Is(err, ErrFleetNetworkRevision) || errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrFleetNetworkApproval
	}
	if err != nil {
		return err
	}
	if base.Compilation.ID != *state.Head.PublishedCompilationID || base.Compilation.NodeRevisionID != state.Revision.ID || base.Compilation.DefaultsRevisionID != state.Defaults.ID ||
		base.Manifest.NodeRevisionID != state.Revision.ID || base.Manifest.DefaultsRevisionID != state.Defaults.ID {
		return ErrFleetNetworkApproval
	}
	for _, revision := range []model.FleetNetworkRevision{state.Revision, state.Defaults} {
		_, digest, err := canonicalFleetNetwork(json.RawMessage(revision.Specification))
		if err != nil || !bytes.Equal(digest, revision.Digest) {
			return ErrFleetNetworkApproval
		}
	}
	var overrides FleetNetworkOverrides
	var defaults FleetNetworkSpec
	if json.Unmarshal([]byte(state.Revision.Specification), &overrides) != nil || json.Unmarshal([]byte(state.Defaults.Specification), &defaults) != nil {
		return ErrFleetNetworkApproval
	}
	resolved, origins, err := resolveFleetNetwork(defaults, overrides)
	if err != nil {
		return ErrFleetNetworkApproval
	}
	type compiledState struct {
		Overrides FleetNetworkOverrides
		Resolved  FleetNetworkSpec
		Origins   map[string]string
	}
	approved, _, err := canonicalFleetArtifact(compiledState{overrides, resolved, origins}, 8<<20)
	if err != nil {
		return ErrFleetNetworkApproval
	}
	current, _, err := canonicalFleetArtifact(compiledState{state.Overrides, state.Resolved, state.Origins}, 8<<20)
	if err != nil || !bytes.Equal(approved, current) {
		return ErrFleetNetworkApproval
	}
	return nil
}
