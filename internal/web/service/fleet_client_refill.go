package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

const MaxFleetRefillTargetBytes int64 = 1 << 30

var ErrFleetRefill = errors.New("fleet refill requires fresh settled forwarding authority")

type FleetRefillRequest struct {
	PlacementID, TermID, GrantID                            string
	ExpectedPlacementRevision, ExpectedCredentialGeneration uint64
	AuthoritySequence                                       uint64
	LowWaterBytes, TargetBytes                              int64
}

// Low-water and target are trusted master scheduler policy; the node supplies observations only.
func RefillCreditTx(ctx context.Context, db *gorm.DB, request FleetRefillRequest) (FleetCredit, error) {
	var result FleetCredit
	if db == nil || !fleetID(request.PlacementID) || !fleetID(request.TermID) || !fleetID(request.GrantID) ||
		request.AuthoritySequence == 0 || request.AuthoritySequence > math.MaxInt64 || request.LowWaterBytes < 0 ||
		request.TargetBytes <= request.LowWaterBytes || request.TargetBytes > MaxFleetRefillTargetBytes {
		return result, controlplane.ErrInvalid
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(append([]byte("panel-fleet-refill/v1:"), encoded...))
	operationID := uuid.NewSHA1(uuid.MustParse(request.GrantID), []byte("refill/"+strconv.FormatUint(request.AuthoritySequence, 10))).String()
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		snapshot, placement, _, err := fleetPlacementSnapshot(tx, request.PlacementID)
		if err != nil {
			return err
		}
		pendingStop, err := fleetPlacementStopPending(tx, placement.ID)
		if err != nil {
			return err
		}
		if pendingStop {
			return controlplane.ErrDrain
		}
		if err := requireFleetPlacementSourceReadyTx(tx, placement); err != nil {
			return err
		}
		if !snapshot.Fleet.Owned || !placement.Enabled || len(placement.GraphDigest) != sha256.Size || uint64(placement.Revision) != request.ExpectedPlacementRevision ||
			uint64(snapshot.Fleet.CredentialGeneration) != request.ExpectedCredentialGeneration || snapshot.Term == nil || snapshot.Term.ID != request.TermID ||
			snapshot.Term.State != "active" ||
			placement.CurrentGrantID == nil || *placement.CurrentGrantID != request.GrantID || placement.CurrentGrantTermID == nil || *placement.CurrentGrantTermID != request.TermID {
			return ErrFleetSource
		}
		if err := fleetEntitlementMatches(snapshot); err != nil {
			return err
		}
		if err := controlplane.RequireRuntimeJournalTx(tx, placement.NodeID, placement.InstallationID, placement.RuntimeID); err != nil {
			return err
		}
		op := model.FleetOperation{ID: operationID, Digest: digest[:], Result: []byte{}}
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&op)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			if err := tx.First(&op, "id = ?", operationID).Error; err != nil {
				return err
			}
			if !bytes.Equal(op.Digest, digest[:]) {
				return controlplane.ErrConflict
			}
			if err := json.Unmarshal(op.Result, &result); err != nil {
				return err
			}
			result.Replayed = true
			return nil
		}
		var observed model.FleetGrantAuthority
		var runtime model.FleetRuntimeJournal
		var grant model.FleetGrant
		var node model.FleetNode
		var share model.FleetRateShare
		for _, read := range []struct {
			value any
			query string
			args  []any
		}{
			{&observed, "grant_id = ?", []any{request.GrantID}},
			{&runtime, "node_id = ? AND runtime_id = ?", []any{placement.NodeID, placement.RuntimeID}},
			{&grant, "id = ?", []any{request.GrantID}},
			{&node, "id = ?", []any{placement.NodeID}},
			{&share, "id = ?", []any{request.GrantID}},
		} {
			if err := tx.Where(read.query, read.args...).First(read.value).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrFleetRefill
				}
				return err
			}
		}
		current, err := currentFleetCreditPublication(tx, snapshot, placement, node.AppliedRevision, grant, map[int64]fleetNativeCreditPublication{})
		if err != nil {
			return err
		}
		if !current {
			return ErrFleetRefill
		}
		now := time.Now().UnixMilli()
		if !runtime.Ready || runtime.Sequence < observed.ReportSequence || runtime.ReceivedUnixMilli < now-30_000 ||
			runtime.ObservedUnixMilli < now-30_000 || runtime.ObservedUnixMilli > now+5_000 ||
			uint64(observed.ReportSequence) != request.AuthoritySequence || observed.NodeID != placement.NodeID || observed.RuntimeID != placement.RuntimeID ||
			!observed.AuthorizationActive || !observed.SpendableKnown || observed.QuotaUnlimited || grant.Unlimited || observed.CreditCeiling != grant.IssuedBytes ||
			observed.SpendableBytes > request.LowWaterBytes || observed.SpendableBytes < 0 || observed.PolicyRevision <= 0 ||
			observed.PolicyRevision != node.AppliedRevision || node.AppliedRevision != node.DesiredRevision ||
			grant.PlacementID == nil || *grant.PlacementID != placement.ID || grant.TermID != request.TermID || grant.ClientID != snapshot.Fleet.ID ||
			share.Revision <= 0 || share.AppliedRevision != share.Revision {
			return ErrFleetRefill
		}
		var rates controlplane.Rates
		if err := json.Unmarshal([]byte(share.Desired), &rates); err != nil {
			return err
		}
		result, err = allocateFleetCreditTx(ctx, tx, FleetCreditRequest{
			OperationID: uuid.NewSHA1(uuid.MustParse(operationID), []byte("allocation")).String(), PlacementID: placement.ID, TermID: request.TermID,
			ExpectedPlacementRevision: request.ExpectedPlacementRevision, ExpectedCredentialGeneration: request.ExpectedCredentialGeneration,
			Bytes: request.TargetBytes - observed.SpendableBytes, Rates: rates, AllowPartial: true,
		}, true)
		if err != nil {
			return err
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		if err := store.QueueCreditUpdate(ctx, result.Grant.ID, uint64(observed.PolicyRevision)); err != nil {
			return err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(&op).Update("result", encoded).Error
	})
	return result, err
}
