package xray

import (
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

func (c *Core) SourceClientCredentials(kind core.Kind) []string {
	switch kind {
	case "http", "mixed":
		return []string{core.CredUsername, core.CredPassword}
	case "vmess", "vless":
		return []string{core.CredUUID}
	case "trojan", "shadowsocks", "hysteria":
		return c.ClientCredentials(kind)
	default:
		return nil
	}
}

func (c *Core) MintSourceClientCredentials(kind core.Kind, settings string, have map[string]string) (core.SourceCredentialResult, error) {
	if len(c.SourceClientCredentials(kind)) == 0 {
		return core.SourceCredentialResult{}, core.ErrKindHasNoUsers
	}
	if kind == "http" || kind == "mixed" {
		minted := map[string]string{}
		if have[core.CredUsername] == "" {
			minted[core.CredUsername] = "u" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		if have[core.CredPassword] == "" {
			minted[core.CredPassword] = strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		return core.SourceCredentialAdditions(have, minted)
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
	switch kind {
	case "http", "mixed":
		username := have[core.CredUsername]
		if username == "" {
			username = email
		}
		if username == "" || len(username) > 256 || have[core.CredPassword] == "" {
			return errors.New("xray source account requires a username up to 256 bytes and a password")
		}
		return nil
	case "vmess", "vless":
		parsed, err := uuid.Parse(have[core.CredUUID])
		if err != nil || parsed == uuid.Nil || parsed.String() != have[core.CredUUID] {
			return errors.New("xray source client requires a canonical nonzero UUID")
		}
	case "shadowsocks":
		if !engine.ValidShadowsocksClientKey(engine.ShadowsocksMethodFromSettings(settings), have[core.CredPassword]) {
			return errors.New("shadowsocks source client key does not match the inbound method")
		}
		method := engine.ShadowsocksMethodFromSettings(settings)
		if engine.ShadowsocksKeyBytes(method) == 0 {
			if _, err := engine.ShadowsocksClientIdentity(method, have[core.CredPassword]); err != nil {
				return errors.New("unsupported shadowsocks source cipher")
			}
		}
	}
	return c.ValidateClient(kind, settings, email, have)
}
