package identity

import (
	"context"
	"crypto/tls"
	"time"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

func FromContext(ctx context.Context, plane string) (Principal, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return Principal{}, ErrIdentity
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || !info.State.HandshakeComplete || info.State.Version < tls.VersionTLS13 || len(info.State.VerifiedChains) == 0 || len(info.State.PeerCertificates) == 0 {
		return Principal{}, ErrIdentity
	}
	leaf := info.State.PeerCertificates[0]
	if len(info.State.VerifiedChains[0]) == 0 || !leaf.Equal(info.State.VerifiedChains[0][0]) {
		return Principal{}, ErrIdentity
	}
	return principalFromCertificate(leaf, plane, time.Now())
}

func (p Principal) Require(nodeID, installationID string) error {
	if p.NodeID != nodeID || p.InstallationID != installationID {
		return ErrIdentity
	}
	return nil
}
