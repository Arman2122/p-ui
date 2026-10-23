package wireguard

import (
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/core"
)

func (c *Core) SourceClientCredentialClaims(kind core.Kind, settings, email string, have map[string]string) ([]core.SourceCredentialClaim, error) {
	if err := c.ValidateSourceClientCredentials(kind, settings, email, have); err != nil {
		return nil, err
	}
	key, _ := wgtypes.ParseKey(have[core.CredPublicKey])
	return []core.SourceCredentialClaim{core.NewSourceCredentialClaim("wireguard-public-key-v1", key[:])}, nil
}
