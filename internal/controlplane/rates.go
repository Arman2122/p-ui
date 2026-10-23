package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type Rates struct {
	Upload   limits.Rate
	Download limits.Rate
}

func (r Rates) Contains(other Rates) bool {
	return r.validate() == nil && other.validate() == nil && fitsRates(r.Upload, []limits.Rate{other.Upload}) && fitsRates(r.Download, []limits.Rate{other.Download})
}

func (r Rates) validate() error {
	_, err := limits.New(limits.Policy{Term: "rate-validation", Revision: 1, Upload: r.Upload, Download: r.Download})
	return err
}

func encodeRates(r Rates) string {
	data, _ := json.Marshal(r)
	return string(data)
}

func decodeRates(encoded string) (Rates, error) {
	var result Rates
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		return result, err
	}
	return result, result.validate()
}

func (s *Store) SetRateBudget(ctx context.Context, id, clientID, termID string, revision uint64, desired Rates) error {
	if !validID(id) || !validID(clientID) || !validID(termID) || revision == 0 || revision > math.MaxInt64 || desired.validate() != nil {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		if _, err := managedAuthority(tx, clientID, termID, true); err != nil {
			return err
		}
		incoming := model.FleetRateBudget{ID: id, ClientID: clientID, TermID: termID, Revision: int64(revision), Desired: encodeRates(desired)}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&incoming)
		if created.Error != nil {
			return created.Error
		}
		var current model.FleetRateBudget
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrScope
			}
			return err
		}
		if current.ClientID != clientID {
			return ErrScope
		}
		previous, err := decodeRates(current.Desired)
		if err != nil {
			return err
		}
		if current.Revision > int64(revision) || current.Revision == int64(revision) && (previous != desired || current.TermID != termID) {
			return ErrRevision
		}
		return tx.Model(&current).Updates(map[string]any{"term_id": termID, "revision": int64(revision), "desired": incoming.Desired}).Error
	})
}

type RateRequest struct {
	OperationID    string
	GrantID        string
	BudgetID       string
	NodeID         string
	InstallationID string
	RuntimeID      string
	Desired        Rates
}

var runtimeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func envelope(a, b limits.Rate) limits.Rate {
	if a.Mode == limits.Unlimited || b.Mode == limits.Unlimited {
		return limits.Rate{Mode: limits.Unlimited}
	}
	if a.Mode == limits.Blocked {
		return b
	}
	if b.Mode == limits.Blocked {
		return a
	}
	return limits.Rate{Mode: limits.Limited, BitsPerSecond: max(a.BitsPerSecond, b.BitsPerSecond), BurstBytes: max(a.BurstBytes, b.BurstBytes)}
}

func fitsRates(bound limits.Rate, values []limits.Rate) bool {
	if bound.Mode == limits.Unlimited {
		return true
	}
	var bits, burst uint64
	for _, value := range values {
		if value.Mode == limits.Blocked {
			continue
		}
		if value.Mode == limits.Unlimited || bound.Mode == limits.Blocked || value.BitsPerSecond > math.MaxUint64-bits || uint64(value.BurstBytes) > math.MaxUint64-burst {
			return false
		}
		bits += value.BitsPerSecond
		burst += uint64(value.BurstBytes)
	}
	return bits <= bound.BitsPerSecond && burst <= uint64(bound.BurstBytes)
}

func (s *Store) RequestRateShare(ctx context.Context, request RateRequest) (model.FleetRateShare, error) {
	if !validID(request.OperationID) || !validID(request.GrantID) || !validID(request.BudgetID) || !validID(request.NodeID) ||
		!validID(request.InstallationID) || !runtimeName.MatchString(request.RuntimeID) || request.Desired.validate() != nil {
		return model.FleetRateShare{}, ErrInvalid
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return model.FleetRateShare{}, err
	}
	digest := sha256.Sum256(append([]byte("rate-share/v1:"), encoded...))
	var result model.FleetRateShare
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
			if err := tx.First(&op, "id = ?", request.OperationID).Error; err != nil {
				return err
			}
			if !bytes.Equal(op.Digest, digest[:]) {
				return ErrConflict
			}
			return json.Unmarshal(op.Result, &result)
		}
		if err := lockNode(tx, request.NodeID, request.InstallationID); err != nil {
			return err
		}
		var grant model.FleetGrant
		if err := tx.First(&grant, "id = ?", request.GrantID).Error; err != nil {
			return err
		}
		term, err := managedAuthority(tx, grant.ClientID, grant.TermID, false)
		if err != nil {
			return err
		}
		var budget model.FleetRateBudget
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&budget, "id = ?", request.BudgetID).Error; err != nil {
			return err
		}
		if grant.NodeID != request.NodeID || grant.InstallationID != request.InstallationID || grant.RuntimeID != request.RuntimeID || grant.ClientID != budget.ClientID {
			return ErrScope
		}
		var shares []model.FleetRateShare
		if err := tx.Where("budget_id = ?", budget.ID).Order("id").Find(&shares).Error; err != nil {
			return err
		}
		var previous Rates
		var uploads, downloads []limits.Rate
		for _, share := range shares {
			outstanding, err := decodeRates(share.Outstanding)
			if err != nil {
				return err
			}
			if share.ID == grant.ID {
				if share.NodeID != request.NodeID || share.InstallationID != request.InstallationID || share.RuntimeID != request.RuntimeID {
					return ErrScope
				}
				result, previous = share, outstanding
				continue
			}
			uploads, downloads = append(uploads, outstanding.Upload), append(downloads, outstanding.Download)
		}
		outstanding := Rates{Upload: envelope(previous.Upload, request.Desired.Upload), Download: envelope(previous.Download, request.Desired.Download)}
		if grant.TermID != budget.TermID && outstanding != previous {
			return ErrScope
		}
		if term != nil && outstanding != previous {
			if term.ExpiresUnixMilli != 0 && term.ExpiresUnixMilli <= time.Now().UnixMilli() {
				return ErrScope
			}
			if _, err := managedAuthority(tx, grant.ClientID, grant.TermID, true); err != nil {
				return err
			}
		}
		bound, err := decodeRates(budget.Desired)
		if err != nil {
			return err
		}
		if outstanding != previous && (!fitsRates(bound.Upload, append(uploads, outstanding.Upload)) || !fitsRates(bound.Download, append(downloads, outstanding.Download))) {
			return ErrBudget
		}
		if result.ID == "" {
			result = model.FleetRateShare{ID: grant.ID, BudgetID: budget.ID, NodeID: request.NodeID, InstallationID: request.InstallationID, RuntimeID: request.RuntimeID}
		}
		if result.Revision == math.MaxInt64 {
			return ErrRevision
		}
		result.Revision++
		result.Desired, result.Outstanding = encodeRates(request.Desired), encodeRates(outstanding)
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&result).Error; err != nil {
			return err
		}
		var owner model.FleetRateShare
		if err := tx.First(&owner, "id = ?", grant.ID).Error; err != nil {
			return err
		}
		if owner.BudgetID != budget.ID {
			return ErrScope
		}
		if err := tx.Model(&owner).Updates(map[string]any{"revision": result.Revision, "desired": result.Desired, "outstanding": result.Outstanding}).Error; err != nil {
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

func (s *Store) ConfirmRateShare(ctx context.Context, p identity.Principal, grantID string, revision uint64, applied Rates) error {
	if !validID(grantID) || revision == 0 || revision > math.MaxInt64 || applied.validate() != nil {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		if err := lockNode(tx, p.NodeID, p.InstallationID); err != nil {
			return err
		}
		var share model.FleetRateShare
		if err := tx.First(&share, "id = ?", grantID).Error; err != nil {
			return err
		}
		if share.NodeID != p.NodeID || share.InstallationID != p.InstallationID {
			return ErrScope
		}
		var budget model.FleetRateBudget
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&budget, "id = ?", share.BudgetID).Error; err != nil {
			return err
		}
		if err := tx.First(&share, "id = ?", grantID).Error; err != nil {
			return err
		}
		desired, err := decodeRates(share.Desired)
		if err != nil {
			return err
		}
		if share.Revision != int64(revision) || desired != applied {
			return ErrRevision
		}
		return tx.Model(&share).Updates(map[string]any{"applied_revision": int64(revision), "outstanding": encodeRates(applied)}).Error
	})
}

func (s *Store) RateBudgetPending(ctx context.Context, budgetID string) (bool, error) {
	if !validID(budgetID) {
		return false, ErrInvalid
	}
	var pending bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var budget model.FleetRateBudget
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&budget, "id = ?", budgetID).Error; err != nil {
			return err
		}
		bound, err := decodeRates(budget.Desired)
		if err != nil {
			return err
		}
		var shares []model.FleetRateShare
		if err := tx.Where("budget_id = ?", budgetID).Find(&shares).Error; err != nil {
			return err
		}
		var uploads, downloads []limits.Rate
		for _, share := range shares {
			rate, err := decodeRates(share.Outstanding)
			if err != nil {
				return errors.Join(ErrBudget, err)
			}
			uploads, downloads = append(uploads, rate.Upload), append(downloads, rate.Download)
		}
		pending = !fitsRates(bound.Upload, uploads) || !fitsRates(bound.Download, downloads)
		return nil
	})
	return pending, err
}
