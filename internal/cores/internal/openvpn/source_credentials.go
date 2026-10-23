package openvpn

import (
	"encoding/pem"
	"errors"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
)

func mintLoginCredentials(have map[string]string) map[string]string {
	minted := map[string]string{}
	if have[core.CredUsername] == "" {
		minted[core.CredUsername] = "u" + randomToken(7)
	}
	if len(have[core.CredPassword]) < 8 {
		minted[core.CredPassword] = randomToken(16)
	}
	return minted
}

func (c *Core) SourceClientCredentials(kind core.Kind) []string {
	return c.ClientCredentials(kind)
}

func (c *Core) MintSourceClientCredentials(kind core.Kind, _ string, have map[string]string) (core.SourceCredentialResult, error) {
	if kind != Kind {
		return core.SourceCredentialResult{}, core.ErrKindHasNoUsers
	}
	result, err := core.SourceCredentialAdditions(have, mintLoginCredentials(have))
	if err != nil {
		return core.SourceCredentialResult{}, err
	}
	if have[core.CredTLSCryptV2] == "" {
		result.NodeDerived = []string{core.CredTLSCryptV2}
	}
	return result, nil
}

func (c *Core) ValidateSourceClientCredentials(kind core.Kind, settings, email string, have map[string]string) error {
	if kind != Kind {
		return core.ErrKindHasNoUsers
	}
	if len(have[core.CredPassword]) > 512 || strings.ContainsAny(have[core.CredPassword], "\r\n\x00") {
		return errors.New("openvpn source password must be at most 512 bytes without line breaks or NUL")
	}
	if wrapped := have[core.CredTLSCryptV2]; wrapped != "" {
		block, rest := pem.Decode([]byte(wrapped))
		if len(wrapped) > 32768 || block == nil || block.Type != "OpenVPN tls-crypt-v2 client key" || len(block.Bytes) == 0 || strings.TrimSpace(string(rest)) != "" {
			return errors.New("openvpn source wrapped key has invalid PEM format")
		}
	}
	return c.ValidateClient(kind, settings, email, have)
}
