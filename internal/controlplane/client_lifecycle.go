package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

const (
	Activation   = "activation"
	Renewal      = "renewal"
	TrafficReset = "traffic_reset"
)

var ErrDrain = errors.New("previous forwarding authority has not been durably stopped")

type TermSpec struct {
	QuotaBytes        int64
	Unlimited         bool
	OpeningUsedBytes  int64
	OpeningUploaded   int64
	OpeningDownloaded int64
	DurationMillis    int64
	ExpiresUnixMilli  int64
	Rates             Rates
}

type TransitionRequest struct {
	OperationID                  string
	ClientID                     string
	ExpectedRevision             uint64
	ExpectedCredentialGeneration uint64
	ExpectedTermID               string
	SourcePolicyDigest           []byte
	Kind                         string
	Spec                         TermSpec
}

type CompleteTransitionRequest struct {
	OperationID                  string
	ClientID                     string
	PendingTermID                string
	ExpectedRevision             uint64
	ExpectedCredentialGeneration uint64
	SourcePolicyDigest           []byte
}

type CancelTransitionRequest struct {
	OperationID                  string
	ClientID                     string
	PendingTermID                string
	ExpectedRevision             uint64
	ExpectedCredentialGeneration uint64
}

type TransitionResult struct {
	Client       model.FleetClient
	Term         model.FleetTerm
	PendingDrain bool
	Replayed     bool `json:"-"`
}

func (s *Store) BindClient(ctx context.Context, panelClientID int, credentialDigest []byte) (model.FleetClient, error) {
	var result model.FleetClient
	if panelClientID <= 0 || len(credentialDigest) != sha256.Size {
		return result, ErrInvalid
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		var panel model.ClientRecord
		if err := tx.Select("id").Clauses(clause.Locking{Strength: "KEY SHARE"}).First(&panel, panelClientID).Error; err != nil {
			return err
		}
		incoming := model.FleetClient{ID: uuid.NewString(), PanelClientID: &panelClientID, Revision: 1, CredentialGeneration: 1, CredentialDigest: bytes.Clone(credentialDigest)}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&incoming).Error; err != nil {
			return err
		}
		if err := tx.First(&result, "panel_client_id = ?", panelClientID).Error; err != nil {
			return err
		}
		if !bytes.Equal(result.CredentialDigest, credentialDigest) {
			return ErrConflict
		}
		return nil
	})
	return result, err
}

func (s *Store) ReconcileCredentials(ctx context.Context, clientID string, expectedGeneration uint64, digest []byte) (model.FleetClient, error) {
	var result model.FleetClient
	if !validID(clientID) || expectedGeneration == 0 || expectedGeneration > math.MaxInt64 || len(digest) != sha256.Size {
		return result, ErrInvalid
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
			return err
		}
		var err error
		result, err = lockClient(tx, clientID)
		if err != nil {
			return err
		}
		if bytes.Equal(result.CredentialDigest, digest) && expectedGeneration <= uint64(result.CredentialGeneration) {
			return nil
		}
		if uint64(result.CredentialGeneration) != expectedGeneration || result.CredentialGeneration == math.MaxInt64 || result.Revision == math.MaxInt64 {
			return ErrRevision
		}
		result.CredentialGeneration++
		result.Revision++
		result.CredentialDigest = bytes.Clone(digest)
		return tx.Model(&result).Updates(map[string]any{"credential_generation": result.CredentialGeneration, "credential_digest": result.CredentialDigest, "revision": result.Revision}).Error
	})
	return result, err
}

func lockClient(tx *gorm.DB, id string) (model.FleetClient, error) {
	var client model.FleetClient
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&client, "id = ?", id).Error; err != nil {
		return client, err
	}
	if client.PanelClientID == nil {
		return client, ErrScope
	}
	return client, nil
}

func validTermSpec(kind string, spec TermSpec) bool {
	if spec.OpeningUploaded < 0 || spec.OpeningDownloaded < 0 || exceeds(spec.OpeningUploaded, spec.OpeningDownloaded) || spec.OpeningUploaded+spec.OpeningDownloaded != spec.OpeningUsedBytes {
		return false
	}
	if spec.QuotaBytes < 0 || spec.OpeningUsedBytes < 0 || spec.Unlimited && spec.QuotaBytes != 0 || !spec.Unlimited && spec.OpeningUsedBytes > spec.QuotaBytes ||
		spec.DurationMillis < 0 || spec.ExpiresUnixMilli < 0 || spec.DurationMillis != 0 && spec.ExpiresUnixMilli != 0 || spec.Rates.validate() != nil {
		return false
	}
	switch kind {
	case Activation:
		return true
	case Renewal:
		return spec.OpeningUsedBytes == 0 && spec.DurationMillis > 0 && spec.ExpiresUnixMilli == 0
	case TrafficReset:
		return spec.OpeningUsedBytes == 0 && spec.DurationMillis == 0 && spec.ExpiresUnixMilli == 0
	default:
		return false
	}
}

func (s *Store) Transition(ctx context.Context, request TransitionRequest) (TransitionResult, error) {
	if !validID(request.ClientID) || request.ExpectedRevision == 0 || request.ExpectedRevision > math.MaxInt64 ||
		request.ExpectedCredentialGeneration == 0 || request.ExpectedCredentialGeneration > math.MaxInt64 ||
		request.ExpectedTermID != "" && !validID(request.ExpectedTermID) || len(request.SourcePolicyDigest) != sha256.Size || !validTermSpec(request.Kind, request.Spec) {
		return TransitionResult{}, ErrInvalid
	}
	return s.lifecycleOperation(ctx, request.OperationID, "transition", request, func(tx *gorm.DB) (TransitionResult, error) {
		client, err := lockClient(tx, request.ClientID)
		if err != nil {
			return TransitionResult{}, err
		}
		if uint64(client.Revision) != request.ExpectedRevision || uint64(client.CredentialGeneration) != request.ExpectedCredentialGeneration || client.Revision == math.MaxInt64 {
			return TransitionResult{}, ErrRevision
		}
		if client.PendingTermID != nil {
			return TransitionResult{}, ErrDrain
		}
		currentID := ""
		if client.CurrentTermID != nil {
			currentID = *client.CurrentTermID
		}
		if currentID != request.ExpectedTermID {
			return TransitionResult{}, ErrRevision
		}
		now := time.Now().UnixMilli()
		var generation int64
		if err := tx.Model(&model.FleetTerm{}).Select("COALESCE(MAX(generation), 0)").Where("client_id = ?", client.ID).Scan(&generation).Error; err != nil {
			return TransitionResult{}, err
		}
		if generation == math.MaxInt64 {
			return TransitionResult{}, ErrRevision
		}
		term := model.FleetTerm{
			ID: uuid.NewString(), ClientID: client.ID, Generation: generation + 1, Kind: request.Kind,
			State: "active", CredentialGeneration: client.CredentialGeneration, DurationMillis: request.Spec.DurationMillis,
			ExpiresUnixMilli: request.Spec.ExpiresUnixMilli, Rates: encodeRates(request.Spec.Rates), SourcePolicyDigest: bytes.Clone(request.SourcePolicyDigest),
			OpeningUploaded: request.Spec.OpeningUploaded, OpeningDownloaded: request.Spec.OpeningDownloaded,
		}
		term.BudgetID = term.ID
		if request.Kind == Activation {
			if currentID != "" {
				return TransitionResult{}, ErrConflict
			}
			if err := startTerm(&term, now); err != nil {
				return TransitionResult{}, err
			}
			client.CurrentTermID = &term.ID
		} else {
			if currentID == "" {
				return TransitionResult{}, ErrScope
			}
			var previous model.FleetTerm
			if err := tx.First(&previous, "id = ? AND client_id = ?", currentID, client.ID).Error; err != nil {
				return TransitionResult{}, err
			}
			if previous.State != "active" || previous.Generation == math.MaxInt64 || request.Kind == Renewal && (previous.ExpiresUnixMilli == 0 || previous.ExpiresUnixMilli > now) {
				return TransitionResult{}, ErrRevision
			}
			term.PredecessorID, term.State = &previous.ID, "pending"
			if request.Kind == TrafficReset {
				term.ExpiresUnixMilli = previous.ExpiresUnixMilli
			}
			if err := tx.Model(&previous).Update("state", "draining").Error; err != nil {
				return TransitionResult{}, err
			}
			client.PendingTermID = &term.ID
		}
		budget := model.FleetBudget{ID: term.BudgetID, LimitBytes: request.Spec.QuotaBytes, Unlimited: request.Spec.Unlimited, UsedBytes: request.Spec.OpeningUsedBytes}
		if err := tx.Create(&budget).Error; err != nil {
			return TransitionResult{}, err
		}
		if err := tx.Create(&term).Error; err != nil {
			return TransitionResult{}, err
		}
		if err := tx.Create(&model.FleetTermUsage{TermID: term.ID}).Error; err != nil {
			return TransitionResult{}, err
		}
		client.Revision++
		if err := tx.Save(&client).Error; err != nil {
			return TransitionResult{}, err
		}
		if term.State == "active" {
			if err := activateRatePool(ctx, tx, client.ID, term); err != nil {
				return TransitionResult{}, err
			}
		}
		return TransitionResult{Client: client, Term: term, PendingDrain: term.State == "pending"}, nil
	})
}

func startTerm(term *model.FleetTerm, now int64) error {
	if term.DurationMillis > math.MaxInt64-now {
		return ErrInvalid
	}
	term.StartedUnixMilli = now
	if term.DurationMillis > 0 {
		term.ExpiresUnixMilli = now + term.DurationMillis
	}
	if term.Kind != TrafficReset && term.ExpiresUnixMilli != 0 && term.ExpiresUnixMilli <= now {
		return ErrInvalid
	}
	return nil
}

func activateRatePool(ctx context.Context, tx *gorm.DB, clientID string, term model.FleetTerm) error {
	var current model.FleetRateBudget
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", clientID).Find(&current).Error; err != nil {
		return err
	}
	if current.Revision == math.MaxInt64 {
		return ErrRevision
	}
	rates, err := decodeRates(term.Rates)
	if err != nil {
		return err
	}
	return (&Store{db: tx}).SetRateBudget(ctx, clientID, clientID, term.ID, uint64(current.Revision+1), rates)
}

func (s *Store) lifecycleOperation(ctx context.Context, operationID, kind string, request any, apply func(*gorm.DB) (TransitionResult, error)) (TransitionResult, error) {
	var result TransitionResult
	if !validID(operationID) {
		return result, ErrInvalid
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(append([]byte("client-lifecycle/v1/"+kind+":"), encoded...))
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, false); err != nil {
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
				return ErrConflict
			}
			if err := json.Unmarshal(op.Result, &result); err != nil {
				return err
			}
			result.Replayed = true
			return nil
		}
		result, err = apply(tx)
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
