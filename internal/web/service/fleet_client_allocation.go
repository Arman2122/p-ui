package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type fleetRateMember struct {
	ID, NodeID, PlacementID string
	Desired, Outstanding    string
	Revision                int64
	Existing, Eligible      bool
}

type fleetRatePlan struct {
	Members     map[string]fleetRateMember
	Allocations []controlplane.RateAllocation
	Capacity    error
}

const fleetRateParticipantLimit = 32768

func fleetLiveAdmissionDemands(tx *gorm.DB) *gorm.DB {
	return tx.Table("fleet_admission_demands d").Select("d.*").
		Joins(`JOIN fleet_nodes n ON n.id = d.node_id AND n.installation_id = d.installation_id AND n.authority_generation = d.session_generation
AND n.applied_revision = d.deployment_revision AND n.desired_revision = n.applied_revision AND n.enabled`).
		Joins(`JOIN fleet_placements p ON p.id = d.placement_id AND p.node_id = d.node_id AND p.client_id = d.client_id AND p.runtime_id = d.runtime_id
AND p.revision = d.placement_revision AND p.credential_generation = d.credential_generation AND p.source_policy_digest = d.source_policy_digest AND p.enabled AND octet_length(p.graph_digest) = 32`).
		Joins("JOIN fleet_node_journals j ON j.node_id = d.node_id AND j.state_journal_id = d.agent_state_journal_id AND NOT j.recovery_required").
		Joins("JOIN fleet_runtime_journals r ON r.node_id = d.node_id AND r.runtime_id = d.runtime_id AND r.state_journal_id = d.state_journal_id AND r.credit_journal_id = d.credit_journal_id").
		Joins("JOIN fleet_published_runtimes pr ON pr.node_id = d.node_id AND pr.revision = d.deployment_revision AND pr.runtime_id = d.runtime_id AND pr.deployment_digest = d.deployment_digest").
		Joins(`JOIN fleet_published_client_admissions a ON a.node_id = d.node_id AND a.revision = d.deployment_revision AND a.runtime_id = d.runtime_id
AND a.client_id = d.client_id AND a.placement_id = d.placement_id AND a.placement_revision = d.placement_revision AND a.credential_generation = d.credential_generation`).
		Joins(`JOIN fleet_published_admission_bindings b ON b.node_id = d.node_id AND b.revision = d.deployment_revision AND b.runtime_id = d.runtime_id
AND b.client_id = d.client_id AND b.inbound_instance_id = d.inbound_instance_id AND b.inbound_tag = d.inbound_tag`)
}

func fleetAdmissionRates(tx *gorm.DB, snapshot fleetClientSnapshot) (fleetRatePlan, error) {
	result := fleetRatePlan{Members: map[string]fleetRateMember{}}
	if snapshot.Term == nil || snapshot.Term.State != "active" || snapshot.Term.ExpiresUnixMilli != 0 && snapshot.Term.ExpiresUnixMilli <= time.Now().UnixMilli() {
		return result, ErrFleetSource
	}
	var pool model.FleetRateBudget
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&pool, "id = ? AND term_id = ?", snapshot.Fleet.ID, snapshot.Term.ID).Error; err != nil {
		return result, err
	}
	var budget model.FleetBudget
	if err := tx.First(&budget, "id = ?", snapshot.Term.BudgetID).Error; err != nil {
		return result, err
	}
	var bound controlplane.Rates
	if err := json.Unmarshal([]byte(pool.Desired), &bound); err != nil {
		return result, err
	}
	if bound != fleetRates(snapshot.Tiers, budget.UsedBytes) {
		return result, ErrFleetPhase
	}
	now := time.Now().UnixMilli()
	var eligible []model.FleetPlacement
	if err := tx.Table("fleet_placements p").Select("p.*").
		Joins("JOIN fleet_nodes n ON n.id = p.node_id AND n.installation_id = p.installation_id").
		Joins("JOIN fleet_runtime_journals r ON r.node_id = p.node_id AND r.runtime_id = p.runtime_id AND r.installation_id = p.installation_id").
		Joins("JOIN fleet_node_journals j ON j.node_id = p.node_id AND j.installation_id = p.installation_id").
		Joins(`JOIN fleet_published_client_admissions a ON a.node_id = p.node_id AND a.runtime_id = p.runtime_id AND a.client_id = p.client_id
AND a.revision = n.applied_revision AND a.placement_id = p.id AND a.placement_revision = p.revision AND a.credential_generation = p.credential_generation`).
		Where(`p.client_id = ? AND p.enabled AND octet_length(p.graph_digest) = 32 AND p.credential_generation = ? AND p.source_policy_digest = ?
AND n.enabled AND n.desired_revision = n.applied_revision AND n.last_seen_unix_milli > ? AND r.ready AND r.received_unix_milli > ?
AND r.observed_unix_milli > ? AND r.observed_unix_milli <= ? AND NOT j.recovery_required AND j.state_journal_id IS NOT NULL`,
			snapshot.Fleet.ID, snapshot.Fleet.CredentialGeneration, snapshot.PolicyDigest, now-45_000, now-30_000, now-30_000, now+5_000).
		Limit(fleetRateParticipantLimit + 1).Scan(&eligible).Error; err != nil {
		return result, err
	}
	if len(eligible) > fleetRateParticipantLimit {
		return result, controlplane.ErrInvalid
	}
	placements := make(map[string]model.FleetPlacement, len(eligible))
	for _, placement := range eligible {
		placements[placement.ID] = placement
	}
	var holders []struct {
		Member fleetRateMember `gorm:"embedded"`
		TermID string
	}
	if err := tx.Table("fleet_rate_shares s").Select("s.id,s.node_id,g.placement_id,g.term_id,s.desired,s.outstanding,s.revision").
		Joins("JOIN fleet_grants g ON g.id = s.id").Where("s.budget_id = ?", pool.ID).Order("s.id").Limit(fleetRateParticipantLimit + 1).Scan(&holders).Error; err != nil {
		return result, err
	}
	if len(holders) > fleetRateParticipantLimit {
		return result, controlplane.ErrInvalid
	}
	var participants []controlplane.RateParticipant
	for _, holder := range holders {
		member := holder.Member
		placement, exists := placements[member.PlacementID]
		member.Existing = true
		member.Eligible = exists && holder.TermID == snapshot.Term.ID && placement.CurrentGrantID != nil && *placement.CurrentGrantID == member.ID && placement.CurrentGrantTermID != nil && *placement.CurrentGrantTermID == snapshot.Term.ID
		var outstanding controlplane.Rates
		if err := json.Unmarshal([]byte(member.Outstanding), &outstanding); err != nil {
			return result, err
		}
		result.Members[member.ID] = member
		participants = append(participants, controlplane.RateParticipant{ID: member.ID, Outstanding: outstanding, Eligible: member.Eligible, Frozen: !member.Eligible, MinimumBurst: 64 << 10})
	}
	var demands []model.FleetAdmissionDemand
	if err := fleetLiveAdmissionDemands(tx).
		Where("d.client_id = ? AND d.term_id = ? AND d.pending AND d.deadline_unix_milli > ? AND d.credential_generation = ? AND d.source_policy_digest = ?", snapshot.Fleet.ID, snapshot.Term.ID, now, snapshot.Fleet.CredentialGeneration, snapshot.PolicyDigest).
		Order("d.placement_id").Limit(fleetRateParticipantLimit + 1).Scan(&demands).Error; err != nil {
		return result, err
	}
	if len(demands) > fleetRateParticipantLimit {
		return result, controlplane.ErrInvalid
	}
	for _, demand := range demands {
		placement, exists := placements[demand.PlacementID]
		if !exists || placement.Revision != demand.PlacementRevision {
			continue
		}
		if placement.CurrentGrantID != nil && placement.CurrentGrantTermID != nil && *placement.CurrentGrantTermID == snapshot.Term.ID {
			if _, exists := result.Members[*placement.CurrentGrantID]; !exists {
				return result, controlplane.ErrRecovery
			}
			continue
		}
		member := fleetRateMember{ID: placement.ID, NodeID: placement.NodeID, PlacementID: placement.ID, Eligible: true}
		result.Members[member.ID] = member
		participants = append(participants, controlplane.RateParticipant{ID: member.ID, Eligible: true, MinimumBurst: 64 << 10})
	}
	var err error
	result.Allocations, err = controlplane.AllocateSharedRates(bound, participants)
	var capacity *controlplane.RateCapacityError
	if errors.As(err, &capacity) {
		result.Capacity = err
		return result, nil
	}
	return result, err
}

func allocateFleetAdmission(ctx context.Context, tx *gorm.DB, authorization FleetAdmissionAuthorization) (FleetAdmissionResult, error) {
	result := FleetAdmissionResult{Outcome: FleetAdmissionAwaitingRate}
	pendingStop, err := fleetPlacementStopPending(tx, authorization.Placement.ID)
	if err != nil {
		return result, err
	}
	if pendingStop {
		return result, controlplane.ErrDrain
	}
	if err := requireFleetPlacementSourceReadyTx(tx, authorization.Placement); err != nil {
		return result, err
	}
	plan, err := fleetAdmissionRates(tx, authorization.snapshot)
	if err != nil {
		return result, err
	}
	if plan.Capacity != nil {
		result.Diagnostic = plan.Capacity.Error()
	}
	store, err := controlplane.NewStore(tx)
	if err != nil {
		return result, err
	}
	for _, allocation := range plan.Allocations {
		member := plan.Members[allocation.ID]
		if !member.Eligible {
			continue
		}
		if member.PlacementID != authorization.Placement.ID {
			var desired controlplane.Rates
			if member.Existing {
				if err := json.Unmarshal([]byte(member.Desired), &desired); err != nil {
					return result, err
				}
				if desired != allocation.Next {
					work := model.FleetRateReconciliation{
						GrantID: member.ID, NodeID: member.NodeID, PlacementID: authorization.Placement.ID,
						RequestID: authorization.Demand.RequestID, DemandRevision: authorization.Demand.Revision, DeadlineUnixMilli: authorization.Demand.DeadlineUnixMilli, AvailableUnixMilli: time.Now().UnixMilli(), Pending: true,
					}
					if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&work).Error; err != nil {
						return result, err
					}
				}
			}
			continue
		}
		if !usableFleetRates(allocation.Next) || plan.Capacity != nil {
			continue
		}
		if member.Existing {
			if err := applyFleetAdmissionRate(ctx, tx, store, member, allocation.Next, authorization.Demand.RequestID, uint64(authorization.Demand.DeploymentRevision)); err != nil {
				return result, err
			}
		} else {
			credit, err := AllocateCreditTx(ctx, tx, FleetCreditRequest{
				OperationID: uuid.NewSHA1(uuid.MustParse(authorization.Demand.RequestID), []byte("initial-credit")).String(),
				PlacementID: authorization.Placement.ID, TermID: authorization.Term.ID, ExpectedPlacementRevision: uint64(authorization.Placement.Revision),
				ExpectedCredentialGeneration: uint64(authorization.Client.CredentialGeneration), Bytes: controlplane.CreditRefillTargetBytes, Rates: allocation.Next, AllowPartial: true,
			})
			if err != nil {
				return result, err
			}
			if err := store.QueueAuthorityUpdate(ctx, credit.Grant.ID, uint64(authorization.Demand.DeploymentRevision), true); err != nil {
				return result, err
			}
		}
		result.Outcome = FleetAdmissionSatisfied
	}
	return result, nil
}

func usableFleetRates(rates controlplane.Rates) bool {
	return rates.Upload.Mode != limits.Blocked && rates.Download.Mode != limits.Blocked
}

func applyFleetAdmissionRate(ctx context.Context, tx *gorm.DB, store *controlplane.Store, member fleetRateMember, rates controlplane.Rates, requestID string, baseRevision uint64) error {
	var desired controlplane.Rates
	if err := json.Unmarshal([]byte(member.Desired), &desired); err != nil {
		return err
	}
	if desired != rates {
		var grant model.FleetGrant
		if err := tx.First(&grant, "id = ?", member.ID).Error; err != nil {
			return err
		}
		encoded, err := json.Marshal(rates)
		if err != nil {
			return err
		}
		operation := uuid.NewSHA1(uuid.MustParse(requestID), []byte(member.ID+"/"+strconv.FormatInt(member.Revision, 10)+"/"+string(encoded))).String()
		if _, err := store.RequestRateShare(ctx, controlplane.RateRequest{
			OperationID: operation, GrantID: grant.ID, BudgetID: grant.ClientID,
			NodeID: grant.NodeID, InstallationID: grant.InstallationID, RuntimeID: grant.RuntimeID, Desired: rates,
		}); err != nil {
			return err
		}
	}
	err := store.QueueAuthorityUpdate(ctx, member.ID, baseRevision, true)
	if desired == rates && errors.Is(err, controlplane.ErrCreditDeliveryRequired) {
		return nil
	}
	return err
}
