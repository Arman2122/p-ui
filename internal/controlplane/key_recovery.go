package controlplane

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func recoveryCredential(tx *gorm.DB, node, installation string) (*x509.Certificate, []byte, error) {
	var current model.FleetCredential
	if err := tx.First(&current, "node_id = ?", node).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, err
		}
		var enrollment model.FleetEnrollment
		if err := tx.Where("node_id = ? AND installation_id = ? AND request_id IS NOT NULL", node, installation).Order("expires_unix_milli DESC").First(&enrollment).Error; err != nil {
			return nil, nil, ErrEnrollment
		}
		current.InstallationID, current.Certificate, current.KeyDigest = enrollment.InstallationID, enrollment.Certificate, enrollment.KeyDigest
	}
	if current.InstallationID != installation {
		return nil, nil, ErrEnrollment
	}
	block, _ := pem.Decode(current.Certificate)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, ErrEnrollment
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || certificate.IsCA {
		return nil, nil, ErrEnrollment
	}
	digest, err := identity.PublicKeyDigest(certificate.PublicKey)
	if err != nil || !bytes.Equal(digest, current.KeyDigest) {
		return nil, nil, ErrEnrollment
	}
	return certificate, digest, nil
}

func (s *Store) RecoveryChallenge(ctx context.Context, request *nodev1.RecoveryChallengeRequest) (*nodev1.RecoveryChallenge, error) {
	if request == nil || !validID(request.NodeId) || !validID(request.InstallationId) {
		return nil, ErrEnrollment
	}
	var result *nodev1.RecoveryChallenge
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockNode(tx, request.NodeId, request.InstallationId); err != nil {
			return err
		}
		_, digest, err := recoveryCredential(tx, request.NodeId, request.InstallationId)
		if err != nil {
			return err
		}
		var saved model.FleetRecoveryChallenge
		found := tx.First(&saved, "node_id = ?", request.NodeId)
		if found.Error != nil && !errors.Is(found.Error, gorm.ErrRecordNotFound) {
			return found.Error
		}
		if found.RowsAffected == 0 || saved.InstallationID != request.InstallationId || saved.ExpiresUnixMilli <= time.Now().UnixMilli() || saved.RequestID != nil {
			nonce := make([]byte, 32)
			if _, err := rand.Read(nonce); err != nil {
				return err
			}
			saved = model.FleetRecoveryChallenge{NodeID: request.NodeId, InstallationID: request.InstallationId, ChallengeID: uuid.NewString(), Nonce: nonce, ExpiresUnixMilli: time.Now().Add(5 * time.Minute).UnixMilli()}
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&saved).Error; err != nil {
				return err
			}
		}
		result = &nodev1.RecoveryChallenge{ChallengeId: saved.ChallengeID, Nonce: bytes.Clone(saved.Nonce), ExpiresUnixMilli: saved.ExpiresUnixMilli, CurrentKeyDigest: digest}
		return nil
	})
	return result, err
}

func (s *Store) RecoverKey(ctx context.Context, plane string, issuer *Issuer, request *nodev1.RecoveryRequest) (*nodev1.EnrollResponse, error) {
	if !validID(plane) || !validIssuer(issuer) || request == nil || !validID(request.NodeId) || !validID(request.InstallationId) || !validID(request.ChallengeId) || !validID(request.RequestId) || len(request.Proof) > 1024 {
		return nil, ErrEnrollment
	}
	requestDigest, err := identity.RequestKeyDigest(request.CertificateRequestPem)
	if err != nil {
		return nil, ErrEnrollment
	}
	proofDigest := sha256.Sum256(request.Proof)
	var certificate []byte
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockNode(tx, request.NodeId, request.InstallationId); err != nil {
			return err
		}
		var challenge model.FleetRecoveryChallenge
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&challenge, "node_id = ?", request.NodeId).Error; err != nil {
			return ErrEnrollment
		}
		if challenge.InstallationID != request.InstallationId || challenge.ChallengeID != request.ChallengeId {
			return ErrEnrollment
		}
		if challenge.RequestID != nil {
			if *challenge.RequestID != request.RequestId || !bytes.Equal(challenge.RequestDigest, requestDigest) || !bytes.Equal(challenge.ProofDigest, proofDigest[:]) {
				return ErrEnrollment
			}
			var active model.FleetCredential
			if err := tx.First(&active, "node_id = ?", request.NodeId).Error; err != nil {
				return err
			}
			if active.InstallationID != request.InstallationId || active.RequestID != request.RequestId || !bytes.Equal(active.KeyDigest, requestDigest) {
				return ErrEnrollment
			}
			certificate = bytes.Clone(challenge.Certificate)
			return nil
		}
		if challenge.ExpiresUnixMilli <= time.Now().UnixMilli() {
			return ErrEnrollment
		}
		current, currentDigest, err := recoveryCredential(tx, request.NodeId, request.InstallationId)
		if err != nil {
			return err
		}
		digest, err := identity.RecoveryDigest(plane, &nodev1.RecoveryChallenge{ChallengeId: challenge.ChallengeID, Nonce: challenge.Nonce}, request)
		if err != nil || identity.VerifyRecovery(current.PublicKey, digest, request.Proof) != nil {
			return ErrEnrollment
		}
		p := identity.Principal{ControlPlaneID: plane, NodeID: request.NodeId, InstallationID: request.InstallationId}
		issued := time.Now().UnixMilli()
		if bytes.Equal(currentDigest, requestDigest) && time.Now().Before(current.NotAfter) {
			certificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: current.Raw})
			issued = current.NotBefore.Add(5 * time.Minute).UnixMilli()
		} else {
			if current.NotBefore.Add(5 * time.Minute).After(time.Now().Add(-time.Minute)) {
				return ErrRotationBusy
			}
			certificate, err = identity.Issue(issuer.Certificate, issuer.Signer, request.CertificateRequestPem, p, time.Now())
			if err != nil {
				return err
			}
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.FleetCredential{NodeID: p.NodeID, InstallationID: p.InstallationID, RequestID: request.RequestId, KeyDigest: requestDigest, Certificate: certificate, IssuedUnixMilli: issued}).Error; err != nil {
			return err
		}
		return tx.Model(&challenge).Updates(map[string]any{"request_id": request.RequestId, "request_digest": requestDigest, "proof_digest": proofDigest[:], "certificate": certificate}).Error
	})
	if err != nil {
		return nil, err
	}
	return &nodev1.EnrollResponse{NodeId: request.NodeId, ControlPlaneId: plane, ProtocolVersion: compatibility.Protocol, CertificateChainPem: certificate, TrustBundlePem: bytes.Clone(issuer.TrustBundle)}, nil
}
