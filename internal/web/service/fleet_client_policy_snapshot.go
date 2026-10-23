package service

import (
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
)

func fleetBillingSnapshotTx(tx *gorm.DB, clientID int) (fleetClientSnapshot, error) {
	out, err := fleetClientPolicySnapshotTx(tx, clientID)
	if err != nil {
		return out, err
	}
	err = completeFleetClientSnapshotTx(tx, &out)
	return out, err
}

func fleetClientPolicySnapshotTx(tx *gorm.DB, clientID int) (fleetClientSnapshot, error) {
	var out fleetClientSnapshot
	if clientID <= 0 {
		return out, ErrFleetSource
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&out.Client, clientID).Error; err != nil {
		return out, err
	}
	if out.Client.TotalGB < 0 || out.Client.ExpiryTime == math.MinInt64 {
		return out, ErrFleetSource
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("panel_client_id = ?", clientID).Find(&out.Fleet).Error; err != nil {
		return out, err
	}
	if out.Fleet.CurrentTermID != nil {
		out.Term = new(model.FleetTerm)
		if err := tx.First(out.Term, "id = ? AND client_id = ?", *out.Fleet.CurrentTermID, out.Fleet.ID).Error; err != nil {
			return out, err
		}
	}
	var assignment model.ClientPolicy
	found := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("email = ?", out.Client.Email).Find(&assignment)
	if found.Error != nil {
		return out, found.Error
	}
	if found.RowsAffected != 0 {
		if assignment.PolicyId == nil || assignment.ResolvedTiers != nil {
			return out, ErrFleetSource
		}
		out.Assignment, out.Policy = &assignment, new(model.Policy)
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(out.Policy, *assignment.PolicyId).Error; err != nil {
			return out, err
		}
		if !sameOwner(out.Client.OwnerUserID, out.Policy.OwnerUserID) || out.Policy.Burst || assignment.AppliedRev != out.Policy.Rev {
			return out, ErrFleetSource
		}
		if !assignment.OverrideQuota && out.Policy.QuotaBytes != nil && out.Client.TotalGB != *out.Policy.QuotaBytes {
			return out, ErrFleetSource
		}
		if err := tx.Model(&model.PolicyInbound{}).Where("policy_id = ?", out.Policy.Id).Order("inbound_id").Pluck("inbound_id", &out.Policy.InboundIDs).Error; err != nil {
			return out, err
		}
		var err error
		out.Tiers, err = ParsePlan(out.Policy.Tiers)
		if err != nil {
			return out, ErrFleetSource
		}
		for _, tier := range out.Tiers.Tiers {
			if tier.FromBytes < 0 || tier.UpBps < 0 || tier.DownBps < 0 || tier.UpBps > 1<<53 || tier.DownBps > 1<<53 {
				return out, ErrFleetSource
			}
		}
		out.Tiers.Tiers = SortTiers(out.Tiers.Tiers)
	}
	if out.Client.OwnerUserID != nil {
		out.Owner = new(model.ResellerAccount)
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(out.Owner, "user_id = ?", *out.Client.OwnerUserID).Error; err != nil {
			return out, err
		}
		if out.Client.Group != "" || out.Owner.Suspended || out.Owner.BudgetBlocked || out.Owner.ExpiresAt > 0 && out.Owner.ExpiresAt <= time.Now().UnixMilli() {
			return out, ErrResellerBudget
		}
	} else if out.Client.Group != "" {
		var group model.ClientGroup
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("name = ?", out.Client.Group).Find(&group).Error; err != nil {
			return out, err
		}
		if group.Id != 0 {
			out.Group = &group
		}
		var quota model.GroupQuota
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("name = ?", out.Client.Group).Find(&quota).Error; err != nil {
			return out, err
		}
		out.GroupQuota = quota.QuotaBytes
		if out.GroupQuota > 0 && out.Group == nil {
			return out, ErrFleetSource
		}
	}
	return out, nil
}

func completeFleetClientSnapshotTx(tx *gorm.DB, out *fleetClientSnapshot) error {
	var credentials []model.ClientCredential
	if err := tx.Where("client_id = ?", out.Client.Id).Order("inbound_id, key").Find(&credentials).Error; err != nil {
		return err
	}
	out.Credentials = credentials
	var err error
	out.CredentialDigest, err = fleetDigest(struct {
		Email, UUID, Password, Auth, PrivateKey, PublicKey, PreSharedKey string
		Credentials                                                      []model.ClientCredential
	}{out.Client.Email, out.Client.UUID, out.Client.Password, out.Client.Auth, out.Client.PrivateKey, out.Client.PublicKey, out.Client.PreSharedKey, credentials})
	if err != nil {
		return err
	}
	var policyID, policyRev int
	var overrides [4]bool
	var depletion, cycle string
	if out.Policy != nil {
		policyID, policyRev = out.Policy.Id, out.Policy.Rev
		overrides = [4]bool{out.Assignment.OverrideQuota, out.Assignment.OverrideExpiry, out.Assignment.OverrideLimitIP, out.Assignment.OverrideInbounds}
		depletion, cycle = out.Assignment.AppliedOnDepletion, out.Assignment.AppliedResetCycle
	}
	var ownerPolicy any
	if out.Owner != nil {
		ownerPolicy = struct {
			ID                 int
			Limit, Expires     int64
			Suspended, Blocked bool
			Depletion, Cycle   string
		}{
			out.Owner.UserID, out.Owner.TrafficLimit, out.Owner.ExpiresAt, out.Owner.Suspended, out.Owner.BudgetBlocked, out.Owner.OnDepletion, out.Owner.ResetCycle,
		}
	}
	out.PolicyDigest, err = fleetDigest(struct {
		ClientID         int
		Owner            *int
		Enabled          bool
		Quota            int64
		Reset, IPLimit   int
		Group            string
		GroupQuota       int64
		PlanID, PlanRev  int
		Overrides        [4]bool
		Depletion, Cycle string
		Tiers            []policy.Tier
		OwnerPolicy      any
	}{
		out.Client.Id, out.Client.OwnerUserID, out.Client.Enable, out.Client.TotalGB, out.Client.Reset, out.Client.LimitIP, out.Client.Group, out.GroupQuota,
		policyID, policyRev, overrides, depletion, cycle, out.Tiers.Tiers, ownerPolicy,
	})
	if err != nil {
		return err
	}
	if err := tx.Where("email = ?", out.Client.Email).Find(&out.Traffic).Error; err != nil {
		return err
	}
	if out.Fleet.Owned && out.Fleet.CurrentTermID == nil {
		var history bool
		if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM fleet_terms WHERE client_id=?)", out.Fleet.ID).Scan(&history).Error; err != nil {
			return err
		}
		fresh, err := fleetFreshUsageProof(out.Fleet.CredentialClaimsVersion, out.Fleet.CurrentTermID, out.Fleet.PendingTermID, history)
		if err != nil {
			return err
		}
		if fresh {
			out.Traffic.Up, out.Traffic.Down, out.Traffic.LastOnline = 0, 0, 0
		}
	}
	return nil
}
