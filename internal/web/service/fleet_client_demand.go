package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
)

type FleetAdmissionAuthorization struct {
	Demand     model.FleetAdmissionDemand
	Client     model.FleetClient
	Placement  model.FleetPlacement
	Term       model.FleetTerm
	Descriptor nodeadmission.Descriptor
	snapshot   fleetClientSnapshot
}

type FleetAdmissionOutcome string

const (
	FleetAdmissionSatisfied    FleetAdmissionOutcome = "satisfied"
	FleetAdmissionAwaitingRate FleetAdmissionOutcome = "awaiting-rate"
	FleetAdmissionDeferred     FleetAdmissionOutcome = "deferred"
	FleetAdmissionRejected     FleetAdmissionOutcome = "rejected"
)

type FleetAdmissionResult struct {
	Outcome    FleetAdmissionOutcome
	Diagnostic string
}

// The action commits authority delivery with fresh scope and intent settlement, never term activation.
func WithFleetAdmissionDemand(ctx context.Context, db *gorm.DB, lease model.FleetAdmissionNodeQueue, expected model.FleetAdmissionDemand, action func(context.Context, *gorm.DB, FleetAdmissionAuthorization) error) error {
	if action == nil {
		return controlplane.ErrInvalid
	}
	_, err := WithFleetAdmissionDemandResult(ctx, db, lease, expected, func(ctx context.Context, tx *gorm.DB, authorization FleetAdmissionAuthorization) (FleetAdmissionResult, error) {
		return FleetAdmissionResult{Outcome: FleetAdmissionSatisfied}, action(ctx, tx, authorization)
	})
	return err
}

func WithFleetAdmissionDemandResult(ctx context.Context, db *gorm.DB, lease model.FleetAdmissionNodeQueue, expected model.FleetAdmissionDemand, action func(context.Context, *gorm.DB, FleetAdmissionAuthorization) (FleetAdmissionResult, error)) (FleetAdmissionResult, error) {
	var result FleetAdmissionResult
	if db == nil || action == nil || !fleetID(lease.NodeID) || lease.ClaimID == nil || !fleetID(*lease.ClaimID) || expected.NodeID != lease.NodeID || !fleetID(expected.PlacementID) {
		return result, controlplane.ErrInvalid
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current model.FleetAdmissionDemand
		if err := tx.Where("placement_id = ? AND node_id = ?", expected.PlacementID, lease.NodeID).First(&current).Error; err != nil {
			return err
		}
		snapshot, placement, _, err := fleetPlacementSnapshot(tx, current.PlacementID)
		if err != nil {
			return err
		}
		if err := requireFleetPlacementSourceReadyTx(tx, placement); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "placement_id = ?", current.PlacementID).Error; err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		if !current.Pending || current.RequestID != expected.RequestID || current.Revision != expected.Revision || current.NodeID != lease.NodeID ||
			current.DeadlineUnixMilli <= now || !snapshot.Fleet.Owned || snapshot.Term == nil || snapshot.Term.ID != current.TermID || snapshot.Term.State != "active" ||
			current.ClientID != snapshot.Fleet.ID || current.CredentialGeneration != snapshot.Fleet.CredentialGeneration || current.PlacementRevision != placement.Revision ||
			current.InstallationID != placement.InstallationID || current.RuntimeID != placement.RuntimeID || !placement.Enabled || len(placement.GraphDigest) != sha256.Size || !nodeadmission.SupportsRuntime(placement.CoreID) ||
			!bytes.Equal(current.SourcePolicyDigest, snapshot.PolicyDigest) || fleetEntitlementMatches(snapshot) != nil {
			return ErrFleetSource
		}
		var claimed model.FleetAdmissionNodeQueue
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("node_id = ? AND claim_id = ? AND lease_until_unix_milli > ?", lease.NodeID, *lease.ClaimID, now).First(&claimed).Error; err != nil {
			return err
		}
		if err := controlplane.RequireRuntimeJournalTx(tx, current.NodeID, current.InstallationID, current.RuntimeID); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", current.NodeID).Error; err != nil {
			return err
		}
		if node.AuthorityGeneration != current.SessionGeneration || node.AppliedRevision != current.DeploymentRevision || node.DesiredRevision != node.AppliedRevision {
			return ErrFleetSource
		}
		var agent model.FleetNodeJournal
		if err := tx.First(&agent, "node_id = ?", current.NodeID).Error; err != nil {
			return err
		}
		var runtime model.FleetRuntimeJournal
		if err := tx.First(&runtime, "node_id = ? AND runtime_id = ?", current.NodeID, current.RuntimeID).Error; err != nil {
			return err
		}
		if agent.StateJournalID == nil || *agent.StateJournalID != current.AgentStateJournalID || runtime.StateJournalID != current.StateJournalID || runtime.CreditJournalID != current.CreditJournalID {
			return controlplane.ErrRecovery
		}
		request := &nodev1.ActivationRequest{
			ClientId: current.ClientID, RuntimeId: current.RuntimeID, PlacementId: current.PlacementID, PlacementRevision: uint64(current.PlacementRevision),
			CredentialGeneration: uint64(current.CredentialGeneration), InboundInstanceId: current.InboundInstanceID, DeploymentRevision: uint64(current.DeploymentRevision),
		}
		descriptor, deployment, err := controlplane.DeployedAdmissionTx(tx, placement, request)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(descriptor)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(encoded)
		if !bytes.Equal(deployment, current.DeploymentDigest) || !bytes.Equal(digest[:], current.DescriptorDigest) || descriptor.InboundTag != current.InboundTag {
			return ErrFleetSource
		}
		deadline := min(current.DeadlineUnixMilli, claimed.LeaseUntilUnixMilli)
		live, cancel := context.WithDeadline(ctx, time.UnixMilli(deadline))
		defer cancel()
		tx = tx.WithContext(live)
		result, err = action(live, tx, FleetAdmissionAuthorization{Demand: current, Client: snapshot.Fleet, Placement: placement, Term: *snapshot.Term, Descriptor: descriptor, snapshot: snapshot})
		if err != nil {
			return err
		}
		if result.Outcome != FleetAdmissionSatisfied && result.Outcome != FleetAdmissionAwaitingRate || len(result.Diagnostic) > 1024 {
			return controlplane.ErrInvalid
		}
		if time.Now().UnixMilli() >= deadline {
			return ErrFleetSource
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		if err := tx.Model(&current).Updates(map[string]any{"outcome": string(result.Outcome), "diagnostic": result.Diagnostic}).Error; err != nil {
			return err
		}
		return store.SettleAdmissionDemand(live, lease, current, result.Outcome == FleetAdmissionAwaitingRate)
	})
	return result, err
}
