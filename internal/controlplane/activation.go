package controlplane

import (
	"context"
	"errors"
	"math"
	"time"

	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type ActivationHandler func(context.Context, *gorm.DB, identity.Principal, uint64, *nodev1.ActivationRequest) (*nodev1.ActivationResult, error)

func (s *Service) activate(ctx context.Context, principal identity.Principal, generation uint64, request *nodev1.ActivationRequest) *nodev1.ActivationResult {
	result := new(nodev1.ActivationResult)
	if request == nil {
		result.ErrorCode = "INVALID_REQUEST"
		return result
	}
	if !validID(request.RequestId) || !validID(request.ClientId) || !validID(request.PlacementId) || !validID(request.InboundInstanceId) || !runtimeName.MatchString(request.RuntimeId) {
		result.ErrorCode = "INVALID_REQUEST"
		return result
	}
	result.RequestId, result.ClientId = request.RequestId, request.ClientId
	now := time.Now()
	if request.CredentialGeneration == 0 || request.CredentialGeneration > math.MaxInt64 || request.DeploymentRevision == 0 || request.DeploymentRevision > math.MaxInt64 || request.PlacementRevision == 0 || request.PlacementRevision > math.MaxInt64 ||
		request.DeadlineUnixMilli <= now.UnixMilli() || request.DeadlineUnixMilli > now.Add(15*time.Second).UnixMilli() {
		result.ErrorCode = "INVALID_REQUEST"
		return result
	}
	if s.activation == nil {
		result.ErrorCode = "ACTIVATION_UNAVAILABLE"
		return result
	}
	select {
	case s.activations <- struct{}{}:
		defer func() { <-s.activations }()
	default:
		result.ErrorCode = "ACTIVATION_BUSY"
		return result
	}
	deadline := min(request.DeadlineUnixMilli, now.Add(5*time.Second).UnixMilli())
	ctx, cancel := context.WithDeadline(ctx, time.UnixMilli(deadline))
	defer cancel()
	if err := s.store.CheckSession(ctx, principal, generation, false); err != nil {
		result.ErrorCode = "NOT_AUTHORIZED"
		return result
	}
	approved, err := s.activation(ctx, s.store.db, principal, generation, request)
	if err != nil {
		switch {
		case errors.Is(err, ErrScope), errors.Is(err, ErrRevision), errors.Is(err, ErrInvalid):
			result.ErrorCode = "NOT_AUTHORIZED"
		case errors.Is(err, ErrBudget):
			result.ErrorCode = "BUDGET_UNAVAILABLE"
		case errors.Is(err, ErrDrain):
			result.ErrorCode = "TRANSITION_PENDING"
		default:
			result.ErrorCode = "ACTIVATION_UNAVAILABLE"
		}
		return result
	}
	if approved == nil || approved.RequestId != request.RequestId || approved.ClientId != request.ClientId || !validID(approved.TermId) ||
		approved.ErrorCode != "" || approved.ExpiresUnixMilli < 0 || approved.ExpiresUnixMilli != 0 && approved.ExpiresUnixMilli <= time.Now().UnixMilli() {
		result.ErrorCode = "ACTIVATION_UNAVAILABLE"
		return result
	}
	// Term acknowledgment carries no forwarding credit; only a committed deployment does.
	s.wake(principal.NodeID)
	return approved
}
