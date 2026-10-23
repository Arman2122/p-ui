package controlplane

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type creditService struct {
	nodev1.UnimplementedCreditServiceServer
	store     *Store
	plane     string
	authorize CreditDeliveryValidator
}

func creditRPCError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalid):
		return status.Error(codes.InvalidArgument, "invalid credit update")
	case errors.Is(err, ErrScope):
		return status.Error(codes.PermissionDenied, "credit scope rejected")
	case errors.Is(err, ErrRecovery), errors.Is(err, ErrRevision):
		return status.Error(codes.FailedPrecondition, "credit update requires reconciliation")
	default:
		return status.Error(codes.Unavailable, "credit update unavailable")
	}
}

func (s *creditService) Fetch(ctx context.Context, request *nodev1.CreditDeliveryRequest) (*nodev1.CreditUpdateBatch, error) {
	p, err := identity.FromContext(ctx, s.plane)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "node certificate required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := s.store.FetchCredit(ctx, p, request, s.authorize)
	return result, creditRPCError(err)
}

func (s *creditService) Acknowledge(ctx context.Context, request *nodev1.CreditUpdateBatch) (*nodev1.CreditUpdateReceipt, error) {
	p, err := identity.FromContext(ctx, s.plane)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "node certificate required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := s.store.AcknowledgeCredit(ctx, p, request)
	return result, creditRPCError(err)
}
