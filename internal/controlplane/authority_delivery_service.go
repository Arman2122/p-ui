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

type authorityDeliveryService struct {
	nodev1.UnimplementedAuthorityDeliveryServiceServer
	store     *Store
	plane     string
	authorize AuthorityDeliveryValidator
}

func authorityDeliveryRPCError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalid):
		return status.Error(codes.InvalidArgument, "invalid authority update")
	case errors.Is(err, ErrScope):
		return status.Error(codes.PermissionDenied, "authority scope rejected")
	case errors.Is(err, ErrRecovery), errors.Is(err, ErrRevision), errors.Is(err, ErrConflict):
		return status.Error(codes.FailedPrecondition, "authority update requires reconciliation")
	default:
		return status.Error(codes.Unavailable, "authority delivery unavailable")
	}
}

func (s *authorityDeliveryService) Fetch(ctx context.Context, request *nodev1.AuthorityDeliveryRequest) (*nodev1.AuthorityUpdateBatch, error) {
	p, err := identity.FromContext(ctx, s.plane)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "node certificate required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := s.store.FetchAuthorityUpdates(ctx, p, request, s.authorize)
	return result, authorityDeliveryRPCError(err)
}

func (s *authorityDeliveryService) Acknowledge(ctx context.Context, request *nodev1.AuthorityUpdateBatch) (*nodev1.AuthorityUpdateReceipt, error) {
	p, err := identity.FromContext(ctx, s.plane)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "node certificate required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := s.store.AcknowledgeAuthorityUpdates(ctx, p, request)
	return result, authorityDeliveryRPCError(err)
}
