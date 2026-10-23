package controlplane

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type timeService struct {
	nodev1.UnimplementedTimeServiceServer
	store *Store
	plane string
}

func (s *timeService) Synchronize(ctx context.Context, request *nodev1.TimeRequest) (*nodev1.TimeResponse, error) {
	principal, err := identity.FromContext(ctx, s.plane)
	if err != nil || request == nil || principal.Require(request.NodeId, request.InstallationId) != nil || !validID(request.Nonce) {
		return nil, status.Error(codes.PermissionDenied, "time identity rejected")
	}
	err = s.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthority(tx, true); err != nil {
			return err
		}
		return lockAuthenticatedNode(tx, principal)
	})
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, "time identity rejected")
	}
	return &nodev1.TimeResponse{NodeId: principal.NodeID, InstallationId: principal.InstallationID, Nonce: request.Nonce, ServerUnixMilli: time.Now().UnixMilli()}, nil
}
