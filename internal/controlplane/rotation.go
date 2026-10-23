package controlplane

import (
	"bytes"
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

var ErrRotationBusy = errors.New("node certificate was recently renewed")

type identityService struct {
	nodev1.UnimplementedIdentityServiceServer
	store  *Store
	plane  string
	issuer *Issuer
}

func validIssuer(issuer *Issuer) bool {
	return issuer != nil && issuer.Certificate != nil && issuer.Signer != nil && len(issuer.TrustBundle) != 0
}

func (s *identityService) Renew(ctx context.Context, request *nodev1.RenewRequest) (*nodev1.EnrollResponse, error) {
	p, err := identity.FromContext(ctx, s.plane)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "node certificate required")
	}
	response, err := s.store.Renew(ctx, p, s.issuer, request)
	switch {
	case err == nil:
		return response, nil
	case errors.Is(err, ErrRotationBusy):
		return nil, status.Error(codes.ResourceExhausted, "certificate recently renewed")
	case errors.Is(err, ErrEnrollment), errors.Is(err, ErrScope), errors.Is(err, identity.ErrCSR):
		return nil, status.Error(codes.PermissionDenied, "certificate renewal rejected")
	default:
		return nil, status.Error(codes.Unavailable, "certificate renewal unavailable")
	}
}

func (s *Store) Renew(ctx context.Context, p identity.Principal, issuer *Issuer, request *nodev1.RenewRequest) (*nodev1.EnrollResponse, error) {
	if !validIssuer(issuer) || request == nil || !validID(request.RequestId) {
		return nil, ErrEnrollment
	}
	if _, err := p.URI(); err != nil {
		return nil, ErrEnrollment
	}
	digest, err := identity.RequestKeyDigest(request.CertificateRequestPem)
	if err != nil {
		return nil, err
	}
	var certificate []byte
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockNode(tx, p.NodeID, p.InstallationID); err != nil {
			return err
		}
		if err := verifiedPresentedSerial(tx, p); err != nil {
			return err
		}
		var current model.FleetCredential
		result := tx.Find(&current, "node_id = ?", p.NodeID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 0 && current.InstallationID == p.InstallationID {
			if current.RequestID == request.RequestId {
				if !bytes.Equal(current.KeyDigest, digest) {
					return ErrEnrollment
				}
				serial, err := storedCertificateSerial(current.Certificate)
				if err != nil || p.Serial != "" && p.Serial != serial && p.Serial != current.PreviousSerial {
					return ErrScope
				}
				certificate = bytes.Clone(current.Certificate)
				return nil
			}
		}
		if err := authorizeRegisteredPeer(tx, p); err != nil {
			return err
		}
		if result.RowsAffected != 0 && current.InstallationID == p.InstallationID && time.Now().UnixMilli()-current.IssuedUnixMilli < time.Minute.Milliseconds() {
			return ErrRotationBusy
		}
		certificate, err = identity.Issue(issuer.Certificate, issuer.Signer, request.CertificateRequestPem, p, time.Now())
		if err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.FleetCredential{
			NodeID: p.NodeID, InstallationID: p.InstallationID, RequestID: request.RequestId,
			KeyDigest: digest, Certificate: certificate, IssuedUnixMilli: time.Now().UnixMilli(), PreviousSerial: p.Serial,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &nodev1.EnrollResponse{
		NodeId: p.NodeID, ControlPlaneId: p.ControlPlaneID, ProtocolVersion: compatibility.Protocol,
		CertificateChainPem: certificate, TrustBundlePem: bytes.Clone(issuer.TrustBundle),
	}, nil
}
