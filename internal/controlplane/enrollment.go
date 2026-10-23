package controlplane

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

var ErrEnrollment = errors.New("node enrollment rejected")

type Enrollment struct {
	NodeID           string
	InstallationID   string
	Ticket           string
	ExpiresUnixMilli int64
}

type Issuer struct {
	Certificate *x509.Certificate
	Signer      crypto.Signer
	TrustBundle []byte
}

func (s *Store) CreateEnrollment(ctx context.Context, name string) (Enrollment, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		return Enrollment{}, ErrInvalid
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return Enrollment{}, err
	}
	ticket := base64.RawURLEncoding.EncodeToString(secret[:])
	hash := sha256.Sum256([]byte(ticket))
	enrollment := Enrollment{
		NodeID: uuid.NewString(), InstallationID: uuid.NewString(), Ticket: ticket,
		ExpiresUnixMilli: time.Now().Add(10 * time.Minute).UnixMilli(),
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node := model.FleetNode{ID: enrollment.NodeID, InstallationID: enrollment.InstallationID, Name: name}
		if err := tx.Create(&node).Error; err != nil {
			return err
		}
		return tx.Create(&model.FleetEnrollment{
			ID: uuid.NewString(), NodeID: node.ID, InstallationID: node.InstallationID,
			Digest: hash[:], ExpiresUnixMilli: enrollment.ExpiresUnixMilli,
		}).Error
	})
	if err != nil {
		return Enrollment{}, err
	}
	return enrollment, nil
}

func (s *Store) Redeem(ctx context.Context, plane string, issuer *Issuer, request *nodev1.EnrollRequest) (*nodev1.EnrollResponse, error) {
	if issuer == nil || request == nil || !validID(plane) || !validID(request.RequestId) || !validID(request.InstallationId) ||
		len(request.Ticket) != 43 || len(request.CertificateRequestPem) > 8192 {
		return nil, ErrEnrollment
	}
	decoded, err := base64.RawURLEncoding.DecodeString(request.Ticket)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != request.Ticket {
		return nil, ErrEnrollment
	}
	hash := sha256.Sum256([]byte(request.Ticket))
	var certificate []byte
	var nodeID string
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var enrollment model.FleetEnrollment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&enrollment, "digest = ?", hash[:]).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnrollment
			}
			return err
		}
		if enrollment.InstallationID != request.InstallationId {
			return ErrEnrollment
		}
		keyDigest, err := identity.RequestKeyDigest(request.CertificateRequestPem)
		if err != nil {
			return ErrEnrollment
		}
		nodeID = enrollment.NodeID
		var node model.FleetNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", nodeID).Error; err != nil {
			return err
		}
		if node.InstallationID != request.InstallationId {
			return ErrEnrollment
		}
		if enrollment.RequestID != nil {
			if !node.Enabled || *enrollment.RequestID != request.RequestId || !bytes.Equal(enrollment.KeyDigest, keyDigest) {
				return ErrEnrollment
			}
			certificate = bytes.Clone(enrollment.Certificate)
			return nil
		}
		if time.Now().UnixMilli() >= enrollment.ExpiresUnixMilli {
			return ErrEnrollment
		}
		if node.Enabled {
			return ErrEnrollment
		}
		certificate, err = identity.Issue(issuer.Certificate, issuer.Signer, request.CertificateRequestPem,
			identity.Principal{ControlPlaneID: plane, NodeID: nodeID, InstallationID: request.InstallationId}, time.Now())
		if err != nil {
			return err
		}
		if err := tx.Model(&enrollment).Updates(map[string]any{"request_id": request.RequestId, "key_digest": keyDigest, "certificate": certificate}).Error; err != nil {
			return err
		}
		return tx.Model(&node).Update("enabled", true).Error
	})
	if err != nil {
		return nil, err
	}
	return &nodev1.EnrollResponse{
		NodeId: nodeID, ControlPlaneId: plane, ProtocolVersion: compatibility.Protocol,
		CertificateChainPem: certificate, TrustBundlePem: bytes.Clone(issuer.TrustBundle),
	}, nil
}
