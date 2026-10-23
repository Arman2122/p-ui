package singbox

import (
	"crypto/sha256"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
)

func (c *Core) SourceClientCredentialClaims(kind core.Kind, settings, email string, have map[string]string) ([]core.SourceCredentialClaim, error) {
	if err := c.ValidateSourceClientCredentials(kind, settings, email, have); err != nil {
		return nil, err
	}
	if kind == KindTUIC {
		id, _ := uuid.Parse(have[core.CredUUID])
		return []core.SourceCredentialClaim{core.NewSourceCredentialClaim("singbox-tuic-uuid-v1", id[:])}, nil
	}
	key := sha256.Sum256([]byte(have[core.CredPassword]))
	return []core.SourceCredentialClaim{core.NewSourceCredentialClaim("singbox-anytls-sha256-v1", key[:])}, nil
}
