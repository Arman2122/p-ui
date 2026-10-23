package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type FleetCreditRequest struct {
	OperationID, PlacementID, TermID                        string
	ExpectedPlacementRevision, ExpectedCredentialGeneration uint64
	Bytes                                                   int64
	Rates                                                   controlplane.Rates
	AllowPartial                                            bool
}

type FleetCredit struct {
	Grant    model.FleetGrant
	Rates    model.FleetRateShare
	Replayed bool `json:"-"`
}

func (FleetClientService) AllocateCredit(ctx context.Context, request FleetCreditRequest) (FleetCredit, error) {
	return AllocateCreditTx(ctx, database.GetDB(), request)
}

// Use the compiler's transaction so a failed publication also rolls back newly reserved authority.
func AllocateCreditTx(ctx context.Context, db *gorm.DB, request FleetCreditRequest) (FleetCredit, error) {
	return allocateFleetCreditTx(ctx, db, request, false)
}

func allocateFleetCreditTx(ctx context.Context, db *gorm.DB, request FleetCreditRequest, preserveRates bool) (FleetCredit, error) {
	var result FleetCredit
	if db == nil || !fleetID(request.OperationID) || !fleetID(request.TermID) || request.Bytes <= 0 {
		return result, controlplane.ErrInvalid
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			uint64(snapshot.Fleet.CredentialGeneration) != request.ExpectedCredentialGeneration || snapshot.Term == nil || snapshot.Term.ID != request.TermID {
			return ErrFleetSource
		}
		if err := fleetEntitlementMatches(snapshot); err != nil {
			return err
		}
		if snapshot.Assignment != nil && !snapshot.Assignment.OverrideQuota && snapshot.Assignment.AppliedOnDepletion != "" && snapshot.Assignment.AppliedOnDepletion != "disable" {
			return ErrFleetPhase
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			return err
		}
		kind := "panel-fleet-credit/v1:"
		if preserveRates {
			kind = "panel-fleet-credit-refill/v1:"
		}
		digest := sha256.Sum256(append([]byte(kind), encoded...))
		op := model.FleetOperation{ID: request.OperationID, Digest: digest[:], Result: []byte{}}
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&op)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			if err := tx.First(&op, "id = ?", request.OperationID).Error; err != nil {
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
		parents, parentLimits, err := fleetParentBudgets(tx, snapshot)
		if err != nil {
			return err
		}
		var budget model.FleetBudget
		if err := tx.First(&budget, "id = ?", snapshot.Term.BudgetID).Error; err != nil {
			return err
		}
		if budget.Unlimited != (snapshot.Client.TotalGB == 0) || !budget.Unlimited && budget.LimitBytes != snapshot.Client.TotalGB {
			return ErrFleetSource
		}
		var pool model.FleetRateBudget
		if err := tx.First(&pool, "id = ?", snapshot.Fleet.ID).Error; err != nil {
			return err
		}
		var current controlplane.Rates
		if err := json.Unmarshal([]byte(pool.Desired), &current); err != nil {
			return err
		}
		if current != fleetRates(snapshot.Tiers, budget.UsedBytes) {
			return ErrFleetPhase
		}
		var ceiling int64
		for _, tier := range snapshot.Tiers.Tiers {
			if tier.FromBytes > budget.UsedBytes && (ceiling == 0 || tier.FromBytes < ceiling) {
				ceiling = tier.FromBytes
			}
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		pending, err := store.RateBudgetPending(ctx, snapshot.Fleet.ID)
		if err != nil {
			return err
		}
		if pending {
			return ErrFleetPhase
		}
		result.Grant, err = store.ReserveClientCredit(ctx, controlplane.ClientCreditRequest{
			OperationID: uuid.NewSHA1(uuid.MustParse(request.OperationID), []byte("quota")).String(), PlacementID: placement.ID,
			ExpectedPlacementRevision: request.ExpectedPlacementRevision, ExpectedRevision: uint64(snapshot.Fleet.Revision),
			ExpectedCredentialGeneration: request.ExpectedCredentialGeneration, TermID: request.TermID, SourcePolicyDigest: snapshot.PolicyDigest,
			ParentBudgetIDs: parents, ParentLimitBytes: parentLimits, Bytes: request.Bytes, AuthorityCeiling: ceiling, AllowPartial: request.AllowPartial,
		})
		if err != nil {
			return err
		}
		if preserveRates {
			err = tx.First(&result.Rates, "id = ?", result.Grant.ID).Error
		} else {
			result.Rates, err = store.RequestRateShare(ctx, controlplane.RateRequest{
				OperationID: uuid.NewSHA1(uuid.MustParse(request.OperationID), []byte("rate")).String(),
				GrantID:     result.Grant.ID, BudgetID: snapshot.Fleet.ID, NodeID: placement.NodeID, InstallationID: placement.InstallationID, RuntimeID: placement.RuntimeID, Desired: request.Rates,
			})
		}
		if err != nil {
			return err
		}
		encoded, err = json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(&op).Update("result", encoded).Error
	})
	return result, err
}

func fleetParentBudgets(tx *gorm.DB, snapshot fleetClientSnapshot) ([]string, map[string]int64, error) {
	var kind string
	var source int
	var limit, opening, epoch int64
	members := tx.Model(&model.ClientRecord{})
	if snapshot.Owner != nil {
		if snapshot.Owner.TrafficLimit > 0 && snapshot.Owner.OnDepletion != "" && snapshot.Owner.OnDepletion != "disable" {
			return nil, nil, ErrFleetPhase
		}
		kind, source, limit, opening, epoch = "reseller", snapshot.Owner.UserID, snapshot.Owner.TrafficLimit, snapshot.Owner.TrafficUsed, snapshot.Owner.PeriodStartedAt
		members = members.Where("owner_user_id = ?", source)
	} else if snapshot.Group != nil {
		kind, source, limit = "group", snapshot.Group.Id, snapshot.GroupQuota
		members = members.Where("owner_user_id IS NULL AND group_name = ?", snapshot.Group.Name)
	} else {
		return nil, nil, nil
	}
	if limit < 0 || opening < 0 {
		return nil, nil, ErrFleetSource
	}
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.fleet-parent/' || ?, 0))", kind+"/"+strconv.Itoa(source)).Error; err != nil {
		return nil, nil, err
	}
	var scope model.FleetBudgetScope
	if err := tx.Where("kind = ? AND source_id = ?", kind, source).Find(&scope).Error; err != nil {
		return nil, nil, err
	}
	if limit == 0 {
		if scope.BudgetID != "" {
			return nil, nil, ErrFleetPool
		}
		return nil, nil, nil
	}
	var unmanaged int64
	if err := tx.Model(&model.ClientRecord{}).Where("id IN (?)", members.Select("id")).Where("NOT EXISTS (SELECT 1 FROM fleet_clients fc WHERE fc.panel_client_id = clients.id AND fc.owned)").Count(&unmanaged).Error; err != nil {
		return nil, nil, err
	}
	if unmanaged != 0 {
		return nil, nil, ErrFleetPool
	}
	if scope.BudgetID == "" {
		var previous int64
		if err := tx.Model(&model.FleetGrant{}).Where("client_id IN (?)", tx.Model(&model.FleetClient{}).Select("id").Where("panel_client_id IN (?)", members.Select("id"))).Count(&previous).Error; err != nil {
			return nil, nil, err
		}
		if previous != 0 {
			return nil, nil, ErrFleetPool
		}
		if kind == "group" {
			var err error
			opening, err = fleetGroupOpeningTx(tx, *snapshot.Group)
			if err != nil {
				return nil, nil, err
			}
		}
		if opening > limit {
			return nil, nil, controlplane.ErrBudget
		}
		budget := model.FleetBudget{ID: uuid.NewString(), LimitBytes: limit, UsedBytes: opening}
		if err := tx.Create(&budget).Error; err != nil {
			return nil, nil, err
		}
		scope = model.FleetBudgetScope{BudgetID: budget.ID, Kind: kind, SourceID: source, SourceEpoch: epoch}
		if err := tx.Create(&scope).Error; err != nil {
			return nil, nil, err
		}
	} else {
		if scope.SourceEpoch != epoch {
			return nil, nil, ErrFleetPool
		}
	}
	return []string{scope.BudgetID}, map[string]int64{scope.BudgetID: limit}, nil
}
