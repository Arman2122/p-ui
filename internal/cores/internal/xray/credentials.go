package xray

import (
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

/*
The credential authority for Xray's kinds: what a client needs, what can be
minted for it, and which field names it in a rendered config. The refusal
strings are the API's exact historical wording — a message an operator has
automated against must not be reworded by a refactor.
*/

func (c *Core) MintClientCredentials(kind core.Kind, settings string, have map[string]string) (map[string]string, error) {
	minted := map[string]string{}
	switch kind {
	case "vmess", "vless":
		if have[core.CredUUID] == "" {
			minted[core.CredUUID] = uuid.NewString()
		}
	case "trojan":
		if have[core.CredPassword] == "" {
			minted[core.CredPassword] = strings.ReplaceAll(uuid.NewString(), "-", "")
		}
	case "shadowsocks":
		// Replaced when unusable, not only when absent: a key of the wrong size
		// for the inbound's method makes xray reject the user outright.
		method := engine.ShadowsocksMethodFromSettings(settings)
		if !engine.ValidShadowsocksClientKey(method, have[core.CredPassword]) {
			minted[core.CredPassword] = engine.RandomShadowsocksClientKey(method)
		}
	case "hysteria":
		if have[core.CredAuth] == "" {
			minted[core.CredAuth] = strings.ReplaceAll(uuid.NewString(), "-", "")
		}
	}
	return minted, nil
}

func (c *Core) ValidateClient(kind core.Kind, _, email string, have map[string]string) error {
	switch kind {
	case "trojan":
		if have[core.CredPassword] == "" {
			return errors.New("empty client ID")
		}
	case "shadowsocks":
		// The email is what identifies an SS client in the rendered config, so
		// a client without one cannot be told apart from its siblings.
		if email == "" {
			return errors.New("empty client ID")
		}
	case "hysteria":
		if have[core.CredAuth] == "" {
			return errors.New("empty client ID")
		}
	case "wireguard":
		if have[core.CredPublicKey] == "" {
			return errors.New("wireguard client requires a key")
		}
	default:
		if have[core.CredUUID] == "" {
			return errors.New("empty client ID")
		}
	}
	return nil
}

func (c *Core) ClientIdentity(kind core.Kind) string {
	switch kind {
	case "trojan":
		return core.CredPassword
	case "shadowsocks", "wireguard":
		return "email"
	case "hysteria":
		return core.CredAuth
	default:
		return core.CredUUID
	}
}
