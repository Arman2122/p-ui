package identity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func RecoveryDigest(plane string, challenge *nodev1.RecoveryChallenge, request *nodev1.RecoveryRequest) ([]byte, error) {
	if challenge == nil || request == nil || !validID(plane) || !validID(request.NodeId) || !validID(request.InstallationId) || !validID(request.RequestId) ||
		!validID(request.ChallengeId) || request.ChallengeId != challenge.ChallengeId || len(challenge.Nonce) != 32 {
		return nil, ErrIdentity
	}
	csr, err := RequestKeyDigest(request.CertificateRequestPem)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("penhoon-node-key-recovery/v1\x00" + plane + request.NodeId + request.InstallationId + request.ChallengeId + request.RequestId))
	_, _ = hash.Write(challenge.Nonce)
	_, _ = hash.Write(csr)
	return hash.Sum(nil), nil
}

func SignRecovery(key crypto.Signer, digest []byte) ([]byte, error) {
	if len(digest) != sha256.Size {
		return nil, ErrIdentity
	}
	switch key := key.(type) {
	case *ecdsa.PrivateKey:
		return ecdsa.SignASN1(rand.Reader, key, digest)
	case ed25519.PrivateKey:
		return ed25519.Sign(key, digest), nil
	case *rsa.PrivateKey:
		return rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256})
	default:
		return nil, ErrIdentity
	}
}

func VerifyRecovery(key crypto.PublicKey, digest, signature []byte) error {
	if len(digest) != sha256.Size || len(signature) > 1024 {
		return ErrIdentity
	}
	switch key := key.(type) {
	case *ecdsa.PublicKey:
		if ecdsa.VerifyASN1(key, digest, signature) {
			return nil
		}
	case ed25519.PublicKey:
		if ed25519.Verify(key, digest, signature) {
			return nil
		}
	case *rsa.PublicKey:
		if rsa.VerifyPSS(key, crypto.SHA256, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256}) == nil {
			return nil
		}
	}
	return ErrIdentity
}

func PublicKeyDigest(key crypto.PublicKey) ([]byte, error) {
	data, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	return digest[:], nil
}
