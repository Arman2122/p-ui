package singbox

import (
	"github.com/Arman2122/p-ui/internal/core"
)

func (c *Core) SourceClientCredentials(kind core.Kind) []string {
	return c.ClientCredentials(kind)
}

func (c *Core) MintSourceClientCredentials(kind core.Kind, settings string, have map[string]string) (core.SourceCredentialResult, error) {
	if len(c.SourceClientCredentials(kind)) == 0 {
		return core.SourceCredentialResult{}, core.ErrKindHasNoUsers
	}
	minted, err := c.MintClientCredentials(kind, settings, have)
	if err != nil {
		return core.SourceCredentialResult{}, err
	}
	return core.SourceCredentialAdditions(have, minted)
}

func (c *Core) ValidateSourceClientCredentials(kind core.Kind, settings, email string, have map[string]string) error {
	if len(c.SourceClientCredentials(kind)) == 0 {
		return core.ErrKindHasNoUsers
	}
	return c.ValidateClient(kind, settings, email, have)
}
