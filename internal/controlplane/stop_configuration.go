package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

var ErrStopComposition = errors.New("current node authority cannot be composed for a stop-only publication")

type StopConfigurationRequest struct {
	OperationID          string
	ExpectedBaseRevision uint64
	ExpectedBaseSHA256   []byte
	GrantIDs             []string
}

type StopConfigurationValidators struct {
	Authority AuthorityDeliveryValidator
	Credit    CreditDeliveryValidator
}

type StopConfigurationResult struct {
	Revision      uint64
	PayloadSHA256 []byte
	GrantIDs      []string
	Replayed      bool `json:"-"`
}

type stopComposition struct {
	bundle     configuration.Bundle
	grants     map[string]governor.Grant
	runtimes   map[string]string
	clients    map[string]string
	placements map[string]string
	targets    map[string]bool
	ledgers    map[string]model.FleetGrant
	shares     map[string]model.FleetRateShare
}

// Stops carry forward proven current authority without rematerializing credentials or networking from source rows.
func (s *Store) StopConfiguration(ctx context.Context, principal identity.Principal, request StopConfigurationRequest, validators StopConfigurationValidators) (StopConfigurationResult, error) {
	var result StopConfigurationResult
	if !validID(request.OperationID) || !validID(principal.NodeID) || !validID(principal.InstallationID) || request.ExpectedBaseRevision == 0 || request.ExpectedBaseRevision >= math.MaxInt64 || len(request.ExpectedBaseSHA256) != sha256.Size || len(request.GrantIDs) == 0 || len(request.GrantIDs) > 1024 {
		return result, ErrInvalid
	}
	request.GrantIDs = slices.Clone(request.GrantIDs)
	request.ExpectedBaseSHA256 = bytes.Clone(request.ExpectedBaseSHA256)
	slices.Sort(request.GrantIDs)
	for i, id := range request.GrantIDs {
		if !validID(id) || i > 0 && id == request.GrantIDs[i-1] {
			return result, ErrInvalid
		}
	}
	encoded, err := json.Marshal(struct {
		Node, Installation string
		Request            StopConfigurationRequest
	}{principal.NodeID, principal.InstallationID, request})
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(append([]byte("stop-configuration/v1:"), encoded...))
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		op := model.FleetOperation{ID: request.OperationID, Digest: digest[:], Result: []byte{}}
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&op)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			if err := tx.First(&op, "id = ?", op.ID).Error; err != nil {
				return err
			}
			if !bytes.Equal(op.Digest, digest[:]) {
				return ErrConflict
			}
			if err := json.Unmarshal(op.Result, &result); err != nil {
				return err
			}
			result.Replayed = true
			return nil
		}
		if err := lockNode(tx, principal.NodeID, principal.InstallationID); err != nil {
			return err
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", principal.NodeID).Error; err != nil {
			return err
		}
		if node.DesiredRevision != node.AppliedRevision || node.AppliedRevision != int64(request.ExpectedBaseRevision) {
			return ErrRevision
		}
		var base model.FleetDeployment
		if err := tx.First(&base, "node_id = ? AND revision = ?", node.ID, node.AppliedRevision).Error; err != nil {
			return err
		}
		hash := sha256.Sum256(base.Payload)
		if !bytes.Equal(hash[:], base.Digest) || !bytes.Equal(base.Digest, request.ExpectedBaseSHA256) {
			return ErrRevision
		}
		state, err := composeStopAuthority(ctx, tx, principal, base, request.GrantIDs, validators)
		if err != nil {
			return err
		}
		if err := lockStopRatePools(tx, state, request.GrantIDs); err != nil {
			return err
		}
		store := &Store{db: tx}
		for _, id := range request.GrantIDs {
			grant := state.grants[id]
			share, err := zeroStopRateShare(tx, state.ledgers[id], state.shares[id])
			if err != nil {
				return err
			}
			grant.RateRevision, grant.Policy.AuthorityRevision = uint64(share.Revision), uint64(share.Revision)
			grant.Policy.Enabled, grant.Policy.Upload, grant.Policy.Download = false, limits.Rate{}, limits.Rate{}
			state.grants[id] = grant
		}
		node.DesiredRevision++
		for i := range state.bundle.Runtimes {
			runtime := &state.bundle.Runtimes[i]
			runtime.Grants = nil
			for id, grant := range state.grants {
				if state.runtimes[id] == runtime.ID {
					grant.Policy.Revision = uint64(node.DesiredRevision)
					runtime.Grants = append(runtime.Grants, grant)
				}
			}
			slices.SortFunc(runtime.Grants, func(a, b governor.Grant) int { return bytes.Compare([]byte(a.ID), []byte(b.ID)) })
		}
		payload, err := json.Marshal(state.bundle)
		if err != nil {
			return err
		}
		if _, err := configuration.Decode(payload); err != nil {
			return errors.Join(ErrStopComposition, err)
		}
		hash = sha256.Sum256(payload)
		deployment := model.FleetDeployment{NodeID: node.ID, Revision: node.DesiredRevision, Digest: hash[:], Payload: payload}
		if err := tx.Create(&deployment).Error; err != nil {
			return err
		}
		if err := tx.Model(&node).Update("desired_revision", node.DesiredRevision).Error; err != nil {
			return err
		}
		if err := indexDeploymentAdmissions(tx, node, deployment); err != nil {
			return err
		}
		if err := carryStopProvenance(tx, principal, request.OperationID, base.Revision, deployment.Revision); err != nil {
			return err
		}
		if err := pruneDeployments(tx, node); err != nil {
			return err
		}
		cursor := ""
		for {
			cursor, err = store.RequeueNodeCredit(ctx, node.ID, cursor, 128)
			if err != nil {
				return err
			}
			if cursor == "" {
				break
			}
		}
		result = StopConfigurationResult{Revision: uint64(node.DesiredRevision), PayloadSHA256: hash[:], GrantIDs: request.GrantIDs}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(&op).Update("result", encoded).Error
	})
	return result, err
}

// APPLIED receipts on other nodes lock these pools without taking normalized source locks.
func lockStopRatePools(tx *gorm.DB, state stopComposition, grants []string) error {
	ids := make([]string, 0, len(grants))
	for _, id := range grants {
		ids = append(ids, state.shares[id].BudgetID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var pools []model.FleetRateBudget
	if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids).Order("id").Find(&pools).Error; err != nil {
		return err
	}
	if len(pools) != len(ids) {
		return ErrStopComposition
	}
	return nil
}

func zeroStopRateShare(tx *gorm.DB, grant model.FleetGrant, expected model.FleetRateShare) (model.FleetRateShare, error) {
	var client model.FleetClient
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&client, "id = ?", grant.ClientID).Error; err != nil {
		return model.FleetRateShare{}, err
	}
	if !client.Owned {
		return model.FleetRateShare{}, ErrStopComposition
	}
	var pool model.FleetRateBudget
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&pool, "id = ? AND client_id = ?", expected.BudgetID, grant.ClientID).Error; err != nil {
		return model.FleetRateShare{}, err
	}
	var share model.FleetRateShare
	if err := tx.First(&share, "id = ?", grant.ID).Error; err != nil {
		return share, err
	}
	if share != expected || share.Revision == math.MaxInt64 {
		return share, ErrRevision
	}
	if _, err := decodeRates(share.Outstanding); err != nil {
		return share, err
	}
	share.Revision++
	share.Desired = encodeRates(Rates{})
	err := tx.Model(&share).Updates(map[string]any{"revision": share.Revision, "desired": share.Desired}).Error
	return share, err
}
