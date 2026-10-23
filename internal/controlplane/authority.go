package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

var ErrAuthority = errors.New("invalid or mismatched control-plane authority")

func (s *Store) BindAuthority(ctx context.Context, plane string, ca *x509.Certificate) error {
	if !validID(plane) || ca == nil || !ca.IsCA {
		return ErrAuthority
	}
	digest := sha256.Sum256(ca.RawSubjectPublicKeyInfo)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var fence model.FleetFence
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&fence, 1).Error; err != nil {
			return err
		}
		var existing model.FleetAuthority
		err := tx.First(&existing, 1).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var nodes int64
			if err := tx.Model(&model.FleetNode{}).Count(&nodes).Error; err != nil {
				return err
			}
			if nodes != 0 {
				return ErrAuthority
			}
			return tx.Create(&model.FleetAuthority{ID: 1, ControlPlaneID: plane, KeyDigest: digest[:]}).Error
		}
		if err != nil {
			return err
		}
		if existing.ControlPlaneID != plane || !bytes.Equal(existing.KeyDigest, digest[:]) {
			return ErrAuthority
		}
		return nil
	})
}
