package mtproto

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"

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
	decoded, err := hex.DecodeString(have[core.CredSecret])
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(have[core.CredSecret])
	}
	if err != nil || len(decoded) < 18 || decoded[0] != 0xee || bytes.Equal(decoded[1:17], make([]byte, 16)) {
		return errors.New("mtproto source client requires a valid FakeTLS secret")
	}
	return c.ValidateClient(kind, settings, email, have)
}
