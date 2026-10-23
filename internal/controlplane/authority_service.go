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

type authorityService struct {
	nodev1.UnimplementedAuthorityServiceServer
	store *Store
	plane string
}

func authorityRPCError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrScope):
		return status.Error(codes.PermissionDenied, "node authority scope rejected")
	case errors.Is(err, ErrInvalid):
		return status.Error(codes.InvalidArgument, "invalid authority observation")
	case errors.Is(err, ErrRecovery):
		return status.Error(codes.FailedPrecondition, "node journal recovery required")
	case errors.Is(err, ErrConflict), errors.Is(err, ErrSequence):
		return status.Error(codes.FailedPrecondition, "authority observation sequence rejected")
	default:
		return status.Error(codes.Unavailable, "authority observation unavailable")
	}
}

func (s *authorityService) RegisterJournals(ctx context.Context, request *nodev1.JournalRegistration) (*nodev1.JournalRegistrationReceipt, error) {
	p, err := identity.FromContext(ctx, s.plane)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "node certificate required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := s.store.RegisterJournals(ctx, p, request)
	return result, authorityRPCError(err)
}

func (s *authorityService) Report(ctx context.Context, request *nodev1.AuthorityReport) (*nodev1.AuthorityReceipt, error) {
	p, err := identity.FromContext(ctx, s.plane)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "node certificate required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := s.store.IngestAuthority(ctx, p, request)
	return result, authorityRPCError(err)
}
