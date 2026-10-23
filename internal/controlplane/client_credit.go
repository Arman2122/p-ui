package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"slices"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type ClientCreditRequest struct {
	OperationID                  string
	PlacementID                  string
	ExpectedPlacementRevision    uint64
	ExpectedRevision             uint64
	ExpectedCredentialGeneration uint64
	TermID                       string
	SourcePolicyDigest           []byte
	ParentBudgetIDs              []string
	ParentLimitBytes             map[string]int64
	Bytes                        int64
	AuthorityCeiling             int64
	AllowPartial                 bool
}

func (s *Store) ReserveClientCredit(ctx context.Context, request ClientCreditRequest) (model.FleetGrant, error) {
	var grant model.FleetGrant
	if !validID(request.OperationID) || !validID(request.PlacementID) || !validID(request.TermID) || request.ExpectedPlacementRevision == 0 ||
		request.ExpectedPlacementRevision > math.MaxInt64 || request.ExpectedRevision == 0 || request.ExpectedRevision > math.MaxInt64 ||
		request.ExpectedCredentialGeneration == 0 || request.ExpectedCredentialGeneration > math.MaxInt64 || len(request.SourcePolicyDigest) != 32 ||
		request.Bytes <= 0 || request.AuthorityCeiling < 0 || len(request.ParentBudgetIDs) > 63 {
		return grant, ErrInvalid
	}
	request.ParentBudgetIDs = slices.Clone(request.ParentBudgetIDs)
	slices.Sort(request.ParentBudgetIDs)
	for i, id := range request.ParentBudgetIDs {
		if !validID(id) || i > 0 && request.ParentBudgetIDs[i-1] == id {
			return grant, ErrInvalid
		}
	}
	if len(request.ParentLimitBytes) != len(request.ParentBudgetIDs) {
		return grant, ErrInvalid
	}
	for _, id := range request.ParentBudgetIDs {
		if limit, ok := request.ParentLimitBytes[id]; !ok || limit < 0 {
			return grant, ErrInvalid
		}
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return grant, err
	}
	digest := sha256.Sum256(append([]byte("client-credit/v1:"), encoded...))
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		var placement model.FleetPlacement
		if err := tx.First(&placement, "id = ?", request.PlacementID).Error; err != nil {
			return err
		}
		if err := lockNode(tx, placement.NodeID, placement.InstallationID); err != nil {
			return err
		}
		if err := RequireRuntimeJournalTx(tx, placement.NodeID, placement.InstallationID, placement.RuntimeID); err != nil {
			return err
		}
		client, err := lockClient(tx, placement.ClientID)
		if err != nil {
			return err
		}
		if !client.Owned || uint64(client.Revision) != request.ExpectedRevision || uint64(client.CredentialGeneration) != request.ExpectedCredentialGeneration ||
			client.CurrentTermID == nil || *client.CurrentTermID != request.TermID {
			return ErrRevision
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&placement, "id = ?", placement.ID).Error; err != nil {
			return err
		}
		if !placement.Enabled || len(placement.GraphDigest) != sha256.Size || uint64(placement.Revision) != request.ExpectedPlacementRevision || placement.CredentialGeneration != client.CredentialGeneration ||
			!bytes.Equal(placement.SourcePolicyDigest, request.SourcePolicyDigest) {
			return ErrScope
		}
		var references []model.FleetPlacementInbound
		if err := tx.Where("placement_id = ?", placement.ID).Find(&references).Error; err != nil {
			return err
		}
		if len(references) == 0 {
			return ErrScope
		}
		for _, reference := range references {
			if reference.PanelInbound == nil {
				return ErrScope
			}
		}
		term, err := managedAuthority(tx, client.ID, request.TermID, true)
		if err != nil || term == nil {
			return errorsOrScope(err)
		}
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
				return ErrConflict
			}
			return json.Unmarshal(op.Result, &grant)
		}
		ids := append([]string{term.BudgetID}, request.ParentBudgetIDs...)
		budgets, err := lockBudgets(tx, ids)
		if err != nil {
			return err
		}
		for _, id := range request.ParentBudgetIDs {
			parent, limit := budgets[id], request.ParentLimitBytes[id]
			if parent.Unlimited || parent.UsedBytes > limit || parent.ReservedBytes > limit-parent.UsedBytes {
				return ErrBudget
			}
			if parent.LimitBytes != limit {
				if err := tx.Model(parent).Update("limit_bytes", limit).Error; err != nil {
					return err
				}
				parent.LimitBytes = limit
			}
		}
		own := budgets[term.BudgetID]
		amount := request.Bytes
		if request.AllowPartial {
			for _, budget := range budgets {
				if !budget.Unlimited {
					if budget.UsedBytes > budget.LimitBytes || budget.ReservedBytes > budget.LimitBytes-budget.UsedBytes {
						return ErrBudget
					}
					amount = min(amount, budget.LimitBytes-budget.UsedBytes-budget.ReservedBytes)
				}
			}
		}
		if request.AuthorityCeiling != 0 {
			used, reserved := own.UsedBytes, own.ReservedBytes
			if own.Unlimited {
				var totals struct {
					Issued, Consumed int64
					Unlimited        bool
				}
				if err := tx.Model(&model.FleetGrant{}).Select("COALESCE(SUM(issued_bytes),0) AS issued, COALESCE(SUM(consumed_bytes),0) AS consumed, COALESCE(BOOL_OR(unlimited),FALSE) AS unlimited").Where("client_id = ? AND term_id = ?", client.ID, term.ID).Scan(&totals).Error; err != nil {
					return err
				}
				if totals.Unlimited || totals.Consumed > used || totals.Consumed > totals.Issued {
					return ErrBudget
				}
				used, reserved = used-totals.Consumed, totals.Issued
			}
			if used > request.AuthorityCeiling || reserved > request.AuthorityCeiling-used {
				return ErrBudget
			}
			remaining := request.AuthorityCeiling - used - reserved
			if request.AllowPartial {
				amount = min(amount, remaining)
			} else if amount > remaining {
				return ErrBudget
			}
		}
		if amount <= 0 {
			return ErrBudget
		}
		grantID := uuid.NewString()
		if placement.CurrentGrantID != nil && placement.CurrentGrantTermID != nil && *placement.CurrentGrantTermID == term.ID {
			grantID = *placement.CurrentGrantID
		}
		ledgerOperation := uuid.NewSHA1(uuid.MustParse(request.OperationID), []byte("credit-ledger")).String()
		grant, err = (&Store{db: tx}).reserve(ctx, ReserveRequest{
			OperationID: ledgerOperation, GrantID: grantID,
			NodeID: placement.NodeID, InstallationID: placement.InstallationID, RuntimeID: placement.RuntimeID,
			ClientID: client.ID, TermID: term.ID, BudgetIDs: ids, Bytes: amount,
		}, placement.ID)
		if err != nil {
			return err
		}
		if err := tx.Model(&placement).Updates(map[string]any{"current_grant_id": grant.ID, "current_grant_term_id": term.ID}).Error; err != nil {
			return err
		}
		encoded, err := json.Marshal(grant)
		if err != nil {
			return err
		}
		return tx.Model(&op).Update("result", encoded).Error
	})
	return grant, err
}

func errorsOrScope(err error) error {
	if err != nil {
		return err
	}
	return ErrScope
}
