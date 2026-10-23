package core

import (
	"crypto/sha256"
	"encoding/binary"
)

// NewSourceCredentialClaim receives a validated canonical lookup key from its
// adapter. Length framing keeps scheme and variable-length identities distinct.
func NewSourceCredentialClaim(scheme string, identity []byte) SourceCredentialClaim {
	hash := sha256.New()
	_, _ = hash.Write([]byte("penhoon/source-auth-claim/v1\x00"))
	_, _ = hash.Write(binary.BigEndian.AppendUint32(nil, uint32(len(scheme))))
	_, _ = hash.Write([]byte(scheme))
	_, _ = hash.Write(binary.BigEndian.AppendUint32(nil, uint32(len(identity))))
	_, _ = hash.Write(identity)
	claim := SourceCredentialClaim{Scheme: scheme}
	copy(claim.Digest[:], hash.Sum(nil))
	return claim
}
