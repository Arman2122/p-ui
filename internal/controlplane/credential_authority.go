package controlplane

import (
	"crypto/x509"
	"encoding/pem"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/node/identity"
)

func storedCertificateSerial(encoded []byte) (string, error) {
	block, _ := pem.Decode(encoded)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", ErrEnrollment
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", ErrEnrollment
	}
	return certificate.SerialNumber.Text(16), nil
}

func verifiedPresentedSerial(tx *gorm.DB, principal identity.Principal) error {
	if principal.Serial == "" {
		return nil
	}
	verified, err := identity.FromContext(tx.Statement.Context, principal.ControlPlaneID)
	if err != nil || verified != principal {
		return ErrScope
	}
	return nil
}

func authorizeRegisteredPeer(tx *gorm.DB, principal identity.Principal) error {
	if principal.Serial == "" {
		return nil
	}
	if err := verifiedPresentedSerial(tx, principal); err != nil {
		return err
	}
	current, _, err := recoveryCredential(tx, principal.NodeID, principal.InstallationID)
	if err != nil {
		return err
	}
	if current.SerialNumber.Text(16) != principal.Serial {
		return ErrScope
	}
	return nil
}

func lockAuthenticatedNode(tx *gorm.DB, principal identity.Principal) error {
	if err := lockNode(tx, principal.NodeID, principal.InstallationID); err != nil {
		return err
	}
	return authorizeRegisteredPeer(tx, principal)
}
