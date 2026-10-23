package ssh

import "github.com/Arman2122/p-ui/internal/core"

func (c *Core) SourceClientCredentialClaims(kind core.Kind, settings, email string, have map[string]string) ([]core.SourceCredentialClaim, error) {
	if err := c.ValidateSourceClientCredentials(kind, settings, email, have); err != nil {
		return nil, err
	}
	return []core.SourceCredentialClaim{core.NewSourceCredentialClaim("ssh-username-v1", []byte(have[core.CredUsername]))}, nil
}
