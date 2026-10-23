package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	nodeadmission "github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type FleetTransitionRequest struct {
	OperationID, PlacementID, Kind                          string
	ExpectedPlacementRevision, ExpectedCredentialGeneration uint64
}

type FleetFinishRequest struct {
	OperationID, PlacementID, PendingTermID                 string
	ExpectedPlacementRevision, ExpectedCredentialGeneration uint64
	Cancel                                                  bool
}

func fleetAction(tx *gorm.DB, id, kind string, request any, apply func() (controlplane.TransitionResult, error)) (controlplane.TransitionResult, error) {
	var result controlplane.TransitionResult
	if !fleetID(id) {
		return result, controlplane.ErrInvalid
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(append([]byte("panel-fleet-action/v1/"+kind+":"), encoded...))
	op := model.FleetOperation{ID: id, Digest: digest[:], Result: []byte{}}
	claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&op)
	if claim.Error != nil {
		return result, claim.Error
	}
	if claim.RowsAffected == 0 {
		if err := tx.First(&op, "id = ?", id).Error; err != nil {
			return result, err
		}
		if !bytes.Equal(op.Digest, digest[:]) {
			return result, controlplane.ErrConflict
		}
		if err := json.Unmarshal(op.Result, &result); err != nil {
			return result, err
		}
		result.Replayed = true
		return result, nil
	}
	result, err = apply()
	if err != nil {
		return result, err
	}
	encoded, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	return result, tx.Model(&op).Update("result", encoded).Error
}

func (FleetClientService) Transition(ctx context.Context, actor *model.User, request FleetTransitionRequest) (controlplane.TransitionResult, error) {
	var result controlplane.TransitionResult
	err := submitTrafficWrite(func() error {
		return database.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := requireFleetAdmin(tx, actor); err != nil {
				return err
			}
			snapshot, placement, _, err := fleetPlacementSnapshot(tx, request.PlacementID)
			if err != nil {
				return err
			}
			if request.Kind == controlplane.Activation && snapshot.Client.ExpiryTime < 0 {
				return ErrFleetSource
			}
			result, err = fleetTransitionTx(ctx, tx, snapshot, placement, request)
			return err
		})
	})
	return result, err
}

// The control stream supplies the authenticated principal/session and its locally authenticated placement.
func (FleetClientService) ActivateFromDemand(ctx context.Context, principal identity.Principal, generation uint64, placementID string, demand *nodev1.ActivationRequest) (controlplane.TransitionResult, error) {
	return activateFleetDemand(ctx, database.GetDB(), principal, generation, placementID, demand)
}

func HandleFleetActivation(ctx context.Context, db *gorm.DB, principal identity.Principal, generation uint64, demand *nodev1.ActivationRequest) (*nodev1.ActivationResult, error) {
	if db == nil || demand == nil || !fleetID(demand.ClientId) || !fleetID(demand.PlacementId) {
		return nil, controlplane.ErrInvalid
	}
	result, err := activateFleetDemand(ctx, db, principal, generation, demand.PlacementId, demand)
	if err != nil {
		return nil, err
	}
	return &nodev1.ActivationResult{RequestId: demand.RequestId, ClientId: demand.ClientId, TermId: result.Term.ID, ExpiresUnixMilli: result.Term.ExpiresUnixMilli}, nil
}

func activateFleetDemand(ctx context.Context, db *gorm.DB, principal identity.Principal, generation uint64, placementID string, demand *nodev1.ActivationRequest) (controlplane.TransitionResult, error) {
	var result controlplane.TransitionResult
	if demand == nil || !fleetID(demand.RequestId) || !fleetID(demand.ClientId) || !fleetID(demand.PlacementId) || !fleetID(demand.InboundInstanceId) || !fleetRuntimeName.MatchString(demand.RuntimeId) ||
		demand.CredentialGeneration == 0 || demand.CredentialGeneration > math.MaxInt64 || demand.DeploymentRevision == 0 || demand.DeploymentRevision > math.MaxInt64 || demand.PlacementRevision == 0 || demand.PlacementRevision > math.MaxInt64 {
		return result, controlplane.ErrInvalid
	}
	if demand.PlacementId != placementID {
		return result, ErrFleetSource
	}
	now := time.Now().UnixMilli()
	if demand.DeadlineUnixMilli <= now || demand.DeadlineUnixMilli > now+15000 {
		return result, ErrFleetSource
	}
	err := submitTrafficWrite(func() error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			snapshot, placement, references, err := fleetPlacementSnapshot(tx, placementID)
			if err != nil {
				return err
			}
			if err := requireFleetPlacementSourceReadyTx(tx, placement); err != nil {
				return err
			}
			if principal.Require(placement.NodeID, placement.InstallationID) != nil || demand.ClientId != snapshot.Fleet.ID ||
				!nodeadmission.SupportsRuntime(placement.CoreID) ||
				int64(demand.CredentialGeneration) != snapshot.Fleet.CredentialGeneration || demand.DeadlineUnixMilli <= time.Now().UnixMilli() ||
				placement.RuntimeID != demand.RuntimeId || uint64(placement.Revision) != demand.PlacementRevision ||
				!slices.ContainsFunc(references, func(reference model.FleetPlacementInbound) bool {
					return reference.InstanceID == demand.InboundInstanceId && reference.PanelInbound != nil
				}) ||
				!placement.Enabled || len(placement.GraphDigest) != sha256.Size || !snapshot.Fleet.Owned {
				return ErrFleetSource
			}
			store, err := controlplane.NewStore(tx)
			if err != nil {
				return err
			}
			if err := store.CheckSession(ctx, principal, generation, false); err != nil {
				return err
			}
			if err := controlplane.RequireRuntimeJournalTx(tx, placement.NodeID, placement.InstallationID, placement.RuntimeID); err != nil {
				return err
			}
			var node model.FleetNode
			if err := tx.First(&node, "id = ?", placement.NodeID).Error; err != nil {
				return err
			}
			if node.AppliedRevision != int64(demand.DeploymentRevision) || node.DesiredRevision != node.AppliedRevision {
				return ErrFleetSource
			}
			if _, _, err := controlplane.DeployedAdmissionTx(tx, placement, demand); err != nil {
				return ErrFleetSource
			}
			if snapshot.Term != nil && snapshot.Term.State == "active" && snapshot.Client.ExpiryTime == snapshot.Term.ExpiresUnixMilli && snapshot.Client.Enable &&
				(snapshot.Term.ExpiresUnixMilli == 0 || snapshot.Term.ExpiresUnixMilli > time.Now().UnixMilli()) {
				result = controlplane.TransitionResult{Client: snapshot.Fleet, Term: *snapshot.Term, Replayed: true}
				_, err := store.EnqueueAdmissionDemand(ctx, principal, generation, demand, result.Term.ID, snapshot.PolicyDigest)
				return err
			}
			request := FleetTransitionRequest{
				OperationID: demand.RequestId, PlacementID: placementID, Kind: controlplane.Activation,
				ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: demand.CredentialGeneration,
			}
			result, err = fleetTransitionTx(ctx, tx, snapshot, placement, request)
			if err != nil {
				return err
			}
			_, err = store.EnqueueAdmissionDemand(ctx, principal, generation, demand, result.Term.ID, snapshot.PolicyDigest)
			return err
		})
	})
	return result, err
}

func fleetTransitionTx(ctx context.Context, tx *gorm.DB, snapshot fleetClientSnapshot, placement model.FleetPlacement, request FleetTransitionRequest) (controlplane.TransitionResult, error) {
	if !snapshot.Fleet.Owned || !placement.Enabled || len(placement.GraphDigest) != sha256.Size || uint64(placement.Revision) != request.ExpectedPlacementRevision ||
		uint64(snapshot.Fleet.CredentialGeneration) != request.ExpectedCredentialGeneration {
		return controlplane.TransitionResult{}, ErrFleetSource
	}
	return fleetAction(tx, request.OperationID, request.Kind, request, func() (controlplane.TransitionResult, error) {
		spec, err := fleetTermSpec(snapshot, request.Kind)
		if err != nil {
			return controlplane.TransitionResult{}, err
		}
		previous := ""
		if snapshot.Term != nil {
			if snapshot.Client.ExpiryTime != snapshot.Term.ExpiresUnixMilli {
				return controlplane.TransitionResult{}, ErrFleetSource
			}
			previous = snapshot.Term.ID
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return controlplane.TransitionResult{}, err
		}
		result, err := store.Transition(ctx, controlplane.TransitionRequest{
			OperationID: uuid.NewSHA1(uuid.MustParse(request.OperationID), []byte("fleet-lifecycle")).String(), ClientID: snapshot.Fleet.ID,
			ExpectedRevision: uint64(snapshot.Fleet.Revision), ExpectedCredentialGeneration: request.ExpectedCredentialGeneration,
			ExpectedTermID: previous, Kind: request.Kind, Spec: spec, SourcePolicyDigest: snapshot.PolicyDigest,
		})
		if err != nil {
			return result, err
		}
		if !result.Replayed {
			if err := recordFleetTermGroupTx(tx, snapshot, result.Term); err != nil {
				return result, err
			}
		}
		if !result.Replayed && !result.PendingDrain {
			err = applyFleetTermProjection(tx, snapshot, result.Term)
		}
		return result, err
	})
}

func (FleetClientService) FinishTransition(ctx context.Context, actor *model.User, request FleetFinishRequest) (controlplane.TransitionResult, error) {
	var result controlplane.TransitionResult
	err := submitTrafficWrite(func() error {
		return database.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := requireFleetAdmin(tx, actor); err != nil {
				return err
			}
			if request.Cancel {
				var err error
				result, err = cancelFleetTransitionTx(ctx, tx, request)
				return err
			}
			snapshot, placement, _, err := fleetPlacementSnapshot(tx, request.PlacementID)
			if err != nil {
				return err
			}
			if !snapshot.Fleet.Owned || uint64(placement.Revision) != request.ExpectedPlacementRevision || uint64(snapshot.Fleet.CredentialGeneration) != request.ExpectedCredentialGeneration {
				return ErrFleetSource
			}
			result, err = fleetAction(tx, request.OperationID, "finish", request, func() (controlplane.TransitionResult, error) {
				if snapshot.Term == nil || snapshot.Client.ExpiryTime != snapshot.Term.ExpiresUnixMilli {
					return controlplane.TransitionResult{}, ErrFleetSource
				}
				store, err := controlplane.NewStore(tx)
				if err != nil {
					return controlplane.TransitionResult{}, err
				}
				operationID := uuid.NewSHA1(uuid.MustParse(request.OperationID), []byte("fleet-lifecycle")).String()
				result, err := store.CompleteTransition(ctx, controlplane.CompleteTransitionRequest{
					OperationID: operationID, ClientID: snapshot.Fleet.ID,
					PendingTermID: request.PendingTermID, ExpectedRevision: uint64(snapshot.Fleet.Revision), ExpectedCredentialGeneration: request.ExpectedCredentialGeneration, SourcePolicyDigest: snapshot.PolicyDigest,
				})
				if err == nil && !result.Replayed {
					err = applyFleetTermProjection(tx, snapshot, result.Term)
				}
				return result, err
			})
			return err
		})
	})
	return result, err
}

func cancelFleetTransitionTx(ctx context.Context, tx *gorm.DB, request FleetFinishRequest) (controlplane.TransitionResult, error) {
	var placement model.FleetPlacement
	if !fleetID(request.PlacementID) {
		return controlplane.TransitionResult{}, ErrFleetSource
	}
	if err := tx.First(&placement, "id = ?", request.PlacementID).Error; err != nil {
		return controlplane.TransitionResult{}, err
	}
	var client model.FleetClient
	if err := tx.First(&client, "id = ?", placement.ClientID).Error; err != nil {
		return controlplane.TransitionResult{}, err
	}
	if client.PanelClientID == nil || !client.Owned || uint64(placement.Revision) != request.ExpectedPlacementRevision || uint64(client.CredentialGeneration) != request.ExpectedCredentialGeneration {
		return controlplane.TransitionResult{}, ErrFleetSource
	}
	var panel model.ClientRecord
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&panel, *client.PanelClientID).Error; err != nil {
		return controlplane.TransitionResult{}, err
	}
	return fleetAction(tx, request.OperationID, "finish", request, func() (controlplane.TransitionResult, error) {
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return controlplane.TransitionResult{}, err
		}
		return store.CancelTransition(ctx, controlplane.CancelTransitionRequest{
			OperationID: uuid.NewSHA1(uuid.MustParse(request.OperationID), []byte("fleet-lifecycle")).String(), ClientID: client.ID,
			PendingTermID: request.PendingTermID, ExpectedRevision: uint64(client.Revision), ExpectedCredentialGeneration: request.ExpectedCredentialGeneration,
		})
	})
}

func applyFleetTermProjection(tx *gorm.DB, snapshot fleetClientSnapshot, term model.FleetTerm) error {
	if term.Kind != controlplane.Activation {
		if err := CaptureClientHistoryBeforeReset(tx, []string{snapshot.Client.Email}, time.Now()); err != nil {
			return err
		}
	}
	if err := tx.Model(&model.ClientRecord{}).Where("id = ?", snapshot.Client.Id).Update("expiry_time", term.ExpiresUnixMilli).Error; err != nil {
		return err
	}
	values := map[string]any{"expiry_time": term.ExpiresUnixMilli}
	if term.Kind != controlplane.Activation {
		values["up"], values["down"] = 0, 0
	}
	if term.Kind == controlplane.Renewal {
		count, err := nextRenewalCount(snapshot.Traffic.ResetCount, 1)
		if err != nil {
			return err
		}
		values["reset_count"] = count
	}
	if err := tx.Model(&core.ClientTraffic{}).Where("email = ?", snapshot.Client.Email).Updates(values).Error; err != nil {
		return err
	}
	return refreshFleetResetTermTx(tx, snapshot.Fleet, snapshot.Client, term)
}
