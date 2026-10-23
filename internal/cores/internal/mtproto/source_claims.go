package mtproto

import (
	"encoding/base64"
	"encoding/hex"

	"github.com/Arman2122/p-ui/internal/core"
)

func (c *Core) SourceClientCredentialClaims(kind core.Kind, settings, email string, have map[string]string) ([]core.SourceCredentialClaim, error) {
	if err := c.ValidateSourceClientCredentials(kind, settings, email, have); err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(have[core.CredSecret])
	if err != nil {
		key, _ = base64.RawURLEncoding.DecodeString(have[core.CredSecret])
	}
	return []core.SourceCredentialClaim{core.NewSourceCredentialClaim("mtproto-key-v1", key[1:17])}, nil
}
