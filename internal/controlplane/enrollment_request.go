package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type EnrollmentRequest struct {
	Version       uint32     `json:"version"`
	RequestID     string     `json:"requestId"`
	Name          string     `json:"name"`
	Configuration HostConfig `json:"configuration"`
	Enrollment    Enrollment `json:"enrollment"`
}

func readEnrollmentRequest(input io.Reader) (EnrollmentRequest, error) {
	var request EnrollmentRequest
	data, err := io.ReadAll(io.LimitReader(input, (64<<10)+1))
	if err != nil {
		return request, err
	}
	if len(data) > 64<<10 {
		return request, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, ErrInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return request, ErrInvalid
	}
	return request, nil
}

func (s *Store) createEnrollmentRequest(ctx context.Context, request EnrollmentRequest) (Enrollment, error) {
	enrollment := request.Enrollment
	secret, err := base64.RawURLEncoding.DecodeString(enrollment.Ticket)
	if request.Version != 1 || !validID(request.RequestID) || !validID(enrollment.NodeID) || !validID(enrollment.InstallationID) ||
		request.Name == "" || request.Name != strings.TrimSpace(request.Name) || len(request.Name) > 128 ||
		err != nil || len(secret) != 32 || base64.RawURLEncoding.EncodeToString(secret) != enrollment.Ticket ||
		enrollment.ExpiresUnixMilli <= time.Now().UnixMilli() || enrollment.ExpiresUnixMilli > time.Now().Add(10*time.Minute).UnixMilli() {
		return Enrollment{}, ErrEnrollment
	}
	hash := sha256.Sum256([]byte(enrollment.Ticket))
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node := model.FleetNode{ID: enrollment.NodeID, InstallationID: enrollment.InstallationID, Name: request.Name}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&node).Error; err != nil {
			return err
		}
		var existing model.FleetNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, "id = ?", node.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnrollment
			}
			return err
		}
		if existing.InstallationID != node.InstallationID || existing.Name != node.Name {
			return ErrEnrollment
		}
		row := model.FleetEnrollment{ID: request.RequestID, NodeID: node.ID, InstallationID: node.InstallationID, Digest: hash[:], ExpiresUnixMilli: enrollment.ExpiresUnixMilli}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		var saved model.FleetEnrollment
		if err := tx.First(&saved, "id = ?", request.RequestID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnrollment
			}
			return err
		}
		if saved.NodeID != row.NodeID || saved.InstallationID != row.InstallationID || saved.ExpiresUnixMilli != row.ExpiresUnixMilli || !bytes.Equal(saved.Digest, hash[:]) {
			return ErrEnrollment
		}
		return nil
	})
	if err != nil {
		return Enrollment{}, err
	}
	return enrollment, nil
}

func (h *Host) enrollRequest(ctx context.Context, request EnrollmentRequest) (credentials.EnrollmentPackage, error) {
	if request.Configuration != h.config {
		return credentials.EnrollmentPackage{}, ErrEnrollment
	}
	result, err := h.store.createEnrollmentRequest(ctx, request)
	if err != nil {
		return credentials.EnrollmentPackage{}, err
	}
	return credentials.EnrollmentPackage{Configuration: credentials.Config{
		Identity:       identity.Principal{NodeID: result.NodeID, InstallationID: result.InstallationID, ControlPlaneID: h.material.ControlPlaneID},
		ControlAddress: h.config.ControlAddress, ServerName: h.config.ServerName, EnrollmentURL: h.config.EnrollmentURL, TrustBundlePEM: bytes.Clone(h.material.CA),
	}, Ticket: result.Ticket, ExpiresUnixMilli: result.ExpiresUnixMilli}, nil
}
