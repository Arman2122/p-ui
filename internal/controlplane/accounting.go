package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"slices"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

var (
	ErrInvalid  = errors.New("invalid control-plane operation")
	ErrBudget   = errors.New("insufficient or inconsistent budget")
	ErrConflict = errors.New("conflicting operation replay")
	ErrSequence = errors.New("noncontiguous accounting sequence")
	ErrScope    = errors.New("accounting scope is not authorized")
	ErrRevision = errors.New("stale or conflicting deployment revision")
	ErrRecovery = errors.New("control-plane recovery reconciliation required")
)

type (
	Store          struct{ db *gorm.DB }
	ReserveRequest struct {
		OperationID    string
		GrantID        string
		NodeID         string
		InstallationID string
		RuntimeID      string
		ClientID       string
		TermID         string
		BudgetIDs      []string
		Bytes          int64
		Unlimited      bool
	}
)

func NewStore(db *gorm.DB) (*Store, error) {
	if db == nil || db.Name() != "postgres" {
		return nil, ErrInvalid
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.FleetFence{ID: 1}).Error; err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func validID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id != uuid.Nil && id.String() == s
}

func lockNode(tx *gorm.DB, nodeID, installationID string) error {
	var node model.FleetNode
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", nodeID).Error
	if err != nil {
		return err
	}
	if !node.Enabled || node.InstallationID != installationID {
		return ErrScope
	}
	return nil
}

func lockBudgets(tx *gorm.DB, ids []string) (map[string]*model.FleetBudget, error) {
	if len(ids) == 0 {
		return nil, ErrScope
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	result := make(map[string]*model.FleetBudget, len(ids))
	for offset := 0; offset < len(ids); offset += 4096 {
		var budgets []*model.FleetBudget
		chunk := ids[offset:min(offset+4096, len(ids))]
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", chunk).Order("id").Find(&budgets).Error; err != nil {
			return nil, err
		}
		for _, b := range budgets {
			result[b.ID] = b
		}
	}
	if len(result) != len(ids) {
		return nil, ErrScope
	}
	return result, nil
}
func exceeds(a, b int64) bool { return a < 0 || b < 0 || b > math.MaxInt64-a }

func (s *Store) Reserve(ctx context.Context, in ReserveRequest) (model.FleetGrant, error) {
	return s.reserve(ctx, in, "")
}

func (s *Store) reserve(ctx context.Context, in ReserveRequest, placementID string) (model.FleetGrant, error) {
	if !validID(in.OperationID) || !validID(in.GrantID) || !validID(in.NodeID) || !validID(in.InstallationID) ||
		!validID(in.ClientID) || !validID(in.TermID) || !runtimeName.MatchString(in.RuntimeID) || len(in.BudgetIDs) == 0 || len(in.BudgetIDs) > 64 ||
		in.Unlimited && in.Bytes != 0 || !in.Unlimited && in.Bytes <= 0 {
		return model.FleetGrant{}, ErrInvalid
	}
	in.BudgetIDs = slices.Clone(in.BudgetIDs)
	slices.Sort(in.BudgetIDs)
	for i, id := range in.BudgetIDs {
		if !validID(id) || i > 0 && in.BudgetIDs[i-1] == id {
			return model.FleetGrant{}, ErrInvalid
		}
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return model.FleetGrant{}, err
	}
	digest := sha256.Sum256(encoded)
	var result model.FleetGrant
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		op := model.FleetOperation{ID: in.OperationID, Digest: digest[:], Result: []byte{}}
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&op)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			if err := tx.First(&op, "id = ?", in.OperationID).Error; err != nil {
				return err
			}
			if !bytes.Equal(op.Digest, digest[:]) {
				return ErrConflict
			}
			return json.Unmarshal(op.Result, &result)
		}
		if err := lockNode(tx, in.NodeID, in.InstallationID); err != nil {
			return err
		}
		if err := RequireRuntimeJournalTx(tx, in.NodeID, in.InstallationID, in.RuntimeID); err != nil {
			return err
		}
		if err := guardManagedReservation(tx, in, placementID); err != nil {
			return err
		}
		budgets, err := lockBudgets(tx, in.BudgetIDs)
		if err != nil {
			return err
		}
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&result, "id = ?", in.GrantID).Error
		isNew := errors.Is(err, gorm.ErrRecordNotFound)
		if err != nil && !isNew {
			return err
		}
		if isNew {
			result = model.FleetGrant{
				ID: in.GrantID, NodeID: in.NodeID, InstallationID: in.InstallationID,
				ClientID: in.ClientID, TermID: in.TermID, RuntimeID: in.RuntimeID, Unlimited: in.Unlimited, Revision: 1,
			}
			if placementID != "" {
				result.PlacementID = &placementID
			}
		} else {
			if result.NodeID != in.NodeID || result.InstallationID != in.InstallationID || result.ClientID != in.ClientID ||
				result.TermID != in.TermID || result.RuntimeID != in.RuntimeID || result.Unlimited != in.Unlimited || result.Revision == math.MaxInt64 {
				return ErrScope
			}
			if placementID != "" && (result.PlacementID == nil || *result.PlacementID != placementID) {
				return ErrScope
			}
			var links []model.FleetGrantBudget
			if err := tx.Where("grant_id = ?", in.GrantID).Order("budget_id").Find(&links).Error; err != nil {
				return err
			}
			if len(links) != len(in.BudgetIDs) {
				return ErrScope
			}
			for i, link := range links {
				if link.BudgetID != in.BudgetIDs[i] {
					return ErrScope
				}
			}
			result.Revision++
		}
		if exceeds(result.IssuedBytes, in.Bytes) {
			return ErrBudget
		}
		for _, id := range in.BudgetIDs {
			b := budgets[id]
			if in.Unlimited && !b.Unlimited {
				return ErrBudget
			}
			if !b.Unlimited {
				if b.UsedBytes > b.LimitBytes || b.ReservedBytes > b.LimitBytes-b.UsedBytes || in.Bytes > b.LimitBytes-b.UsedBytes-b.ReservedBytes {
					return ErrBudget
				}
				b.ReservedBytes += in.Bytes
				if err := tx.Model(b).Update("reserved_bytes", b.ReservedBytes).Error; err != nil {
					return err
				}
			}
		}
		result.IssuedBytes += in.Bytes
		if isNew {
			if err := tx.Create(&result).Error; err != nil {
				return err
			}
			for _, id := range in.BudgetIDs {
				if err := tx.Create(&model.FleetGrantBudget{GrantID: result.ID, BudgetID: id}).Error; err != nil {
					return err
				}
			}
		} else if err := tx.Model(&result).Updates(map[string]any{"issued_bytes": result.IssuedBytes, "revision": result.Revision}).Error; err != nil {
			return err
		}
		out, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Model(&op).Update("result", out).Error
	})
	return result, err
}

func validBatch(b *nodev1.UsageBatch) bool {
	if b == nil || !validID(b.NodeId) || !validID(b.InstallationId) || b.Sequence == 0 || b.Sequence > math.MaxInt64 ||
		len(b.Checkpoints) == 0 || len(b.Checkpoints) > 4096 || proto.Size(b) > 1<<20 {
		return false
	}
	seen := map[[3]string]bool{}
	for _, c := range b.Checkpoints {
		if c == nil || !validID(c.GrantId) || !validID(c.ClientId) || !validID(c.TermId) || !validID(c.MeterIncarnation) ||
			c.MeterId == "" || len(c.MeterId) > 128 || c.ObservedUnixMilli <= 0 || c.Uploaded > math.MaxInt64 || c.Downloaded > math.MaxInt64 {
			return false
		}
		k := [3]string{c.GrantId, c.MeterId, c.MeterIncarnation}
		if seen[k] {
			return false
		}
		seen[k] = true
	}
	return true
}

func (s *Store) Ingest(ctx context.Context, p identity.Principal, b *nodev1.UsageBatch) (*nodev1.UsageReceipt, error) {
	if !validBatch(b) {
		return nil, ErrInvalid
	}
	if err := p.Require(b.NodeId, b.InstallationId); err != nil {
		return nil, ErrScope
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(b)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	var acknowledged uint64
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		if err := lockAuthenticatedNode(tx, p); err != nil {
			return err
		}
		cursor := model.FleetUsageCursor{NodeID: p.NodeID, InstallationID: p.InstallationID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&cursor).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cursor, "node_id = ? AND installation_id = ?", p.NodeID, p.InstallationID).Error; err != nil {
			return err
		}
		if int64(b.Sequence) <= cursor.Sequence {
			if int64(b.Sequence) == cursor.Sequence && !bytes.Equal(cursor.Digest, digest[:]) {
				return ErrConflict
			}
			acknowledged = uint64(cursor.Sequence)
			return nil
		}
		if cursor.Sequence == math.MaxInt64 || int64(b.Sequence) != cursor.Sequence+1 {
			return ErrSequence
		}
		grantIDs := make([]string, 0, len(b.Checkpoints))
		for _, c := range b.Checkpoints {
			grantIDs = append(grantIDs, c.GrantId)
		}
		slices.Sort(grantIDs)
		grantIDs = slices.Compact(grantIDs)
		var links []model.FleetGrantBudget
		if err := tx.Where("grant_id IN ?", grantIDs).Find(&links).Error; err != nil {
			return err
		}
		budgetIDs := make([]string, 0, len(links))
		byGrant := make(map[string][]string)
		for _, link := range links {
			budgetIDs = append(budgetIDs, link.BudgetID)
			byGrant[link.GrantID] = append(byGrant[link.GrantID], link.BudgetID)
		}
		slices.Sort(budgetIDs)
		budgetIDs = slices.Compact(budgetIDs)
		budgets, err := lockBudgets(tx, budgetIDs)
		if err != nil {
			return err
		}
		var rows []*model.FleetGrant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", grantIDs).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(grantIDs) {
			return ErrScope
		}
		grants := make(map[string]*model.FleetGrant, len(rows))
		for _, g := range rows {
			if g.NodeID != p.NodeID || g.InstallationID != p.InstallationID || len(byGrant[g.ID]) == 0 {
				return ErrScope
			}
			grants[g.ID] = g
		}
		meterKeys := make([][]any, 0, len(b.Checkpoints))
		for _, c := range b.Checkpoints {
			meterKeys = append(meterKeys, []any{c.GrantId, c.MeterId, c.MeterIncarnation})
		}
		var previousMeters []*model.FleetMeter
		if err := tx.Where("(grant_id,meter_id,incarnation) IN ?", meterKeys).Find(&previousMeters).Error; err != nil {
			return err
		}
		meters := make(map[[3]string]*model.FleetMeter, len(previousMeters))
		for _, meter := range previousMeters {
			meters[[3]string{meter.GrantID, meter.MeterID, meter.Incarnation}] = meter
		}
		var changedMeters []*model.FleetMeter
		changedGrants := map[string]bool{}
		changedBudgets := map[string]bool{}
		termDeltas := make(map[[2]string]*termUsageDelta)
		for _, c := range b.Checkpoints {
			g := grants[c.GrantId]
			if g.ClientID != c.ClientId || g.TermID != c.TermId || g.RuntimeID != c.MeterId {
				return ErrScope
			}
			meter := meters[[3]string{c.GrantId, c.MeterId, c.MeterIncarnation}]
			if meter == nil {
				meter = &model.FleetMeter{GrantID: c.GrantId, MeterID: c.MeterId, Incarnation: c.MeterIncarnation}
			}
			up, down := int64(c.Uploaded), int64(c.Downloaded)
			if up < meter.Uploaded || down < meter.Downloaded {
				return ErrInvalid
			}
			dUp, dDown := up-meter.Uploaded, down-meter.Downloaded
			if exceeds(dUp, dDown) {
				return ErrBudget
			}
			delta := dUp + dDown
			if delta > 0 {
				key := [2]string{g.ClientID, g.TermID}
				if termDeltas[key] == nil {
					termDeltas[key] = &termUsageDelta{}
				}
				total := termDeltas[key]
				if exceeds(total.Uploaded, dUp) || exceeds(total.Downloaded, dDown) {
					return ErrBudget
				}
				total.Uploaded, total.Downloaded, total.Observed = total.Uploaded+dUp, total.Downloaded+dDown, max(total.Observed, c.ObservedUnixMilli)
			}
			if exceeds(g.ConsumedBytes, delta) || !g.Unlimited && delta > g.IssuedBytes-g.ConsumedBytes {
				return ErrBudget
			}
			for _, id := range byGrant[g.ID] {
				budget := budgets[id]
				if exceeds(budget.UsedBytes, delta) || !budget.Unlimited && delta > budget.ReservedBytes {
					return ErrBudget
				}
				budget.UsedBytes += delta
				if !budget.Unlimited {
					budget.ReservedBytes -= delta
				}
				if delta > 0 {
					changedBudgets[id] = true
				}
			}
			g.ConsumedBytes += delta
			if delta > 0 {
				changedGrants[g.ID] = true
			}
			meter.Uploaded = up
			meter.Downloaded = down
			changedMeters = append(changedMeters, meter)
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(changedMeters, 500).Error; err != nil {
			return err
		}
		var budgetWrites []*model.FleetBudget
		for _, id := range budgetIDs {
			if changedBudgets[id] {
				budgetWrites = append(budgetWrites, budgets[id])
			}
		}
		if len(budgetWrites) > 0 {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"used_bytes", "reserved_bytes"})}).CreateInBatches(budgetWrites, 500).Error; err != nil {
				return err
			}
		}
		var grantWrites []*model.FleetGrant
		for _, id := range grantIDs {
			if changedGrants[id] {
				grantWrites = append(grantWrites, grants[id])
			}
		}
		if len(grantWrites) > 0 {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"consumed_bytes"})}).CreateInBatches(grantWrites, 500).Error; err != nil {
				return err
			}
		}
		if err := recordTermUsage(tx, termDeltas); err != nil {
			return err
		}
		if err := tx.Model(&cursor).Updates(map[string]any{"sequence": int64(b.Sequence), "digest": digest[:]}).Error; err != nil {
			return err
		}
		acknowledged = b.Sequence
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &nodev1.UsageReceipt{DurableSequence: acknowledged, AccountedSequence: acknowledged}, nil
}
