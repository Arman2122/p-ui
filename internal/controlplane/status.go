package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type HostStatus struct {
	ControlPlaneID              string
	ControlAddress              string
	EnrollmentURL               string
	RootSHA256                  string
	CertificateExpiresUnixMilli int64
	RecoveryRequired            bool
	RecoveryReason              string
}

func (h *Host) Status(ctx context.Context) (HostStatus, error) {
	digest := sha256.Sum256(h.issuer.Certificate.Raw)
	status := HostStatus{ControlPlaneID: h.material.ControlPlaneID, ControlAddress: h.config.ControlAddress, EnrollmentURL: h.config.EnrollmentURL, RootSHA256: hex.EncodeToString(digest[:])}
	certificate := h.certificate.Load()
	if certificate != nil && certificate.Leaf != nil {
		status.CertificateExpiresUnixMilli = certificate.Leaf.NotAfter.UnixMilli()
	}
	var fence model.FleetFence
	if err := h.store.db.WithContext(ctx).First(&fence, 1).Error; err != nil {
		return status, err
	}
	status.RecoveryRequired, status.RecoveryReason = fence.RecoveryRequired, fence.Reason
	return status, nil
}

func (s *Store) Nodes(ctx context.Context, after string, limit int) ([]model.FleetNode, string, error) {
	if after != "" && !validID(after) || limit < 1 || limit > 200 {
		return nil, "", ErrInvalid
	}
	var nodes []model.FleetNode
	query := s.db.WithContext(ctx).Order("id").Limit(limit + 1)
	if after != "" {
		query = query.Where("id > ?", after)
	}
	if err := query.Find(&nodes).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(nodes) > limit {
		nodes = nodes[:limit]
		next = nodes[len(nodes)-1].ID
	}
	return nodes, next, nil
}
