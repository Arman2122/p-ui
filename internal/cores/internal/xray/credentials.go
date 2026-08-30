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

func (c *Core) ValidateClient(kind core.Kind, settings, email string, have map[string]string) error {
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
		// Asked of the one table, so the inbound form and this refusal cannot
		// come to disagree about which ciphers can carry a client.
		if !core.Can(core.CapSSMultiUser, core.FactsFromJSON(string(kind), settings, "")) {
			return errors.New("shadowsocks method " + engine.ShadowsocksMethodFromSettings(settings) +
				" is single-user and cannot carry clients; use 2022-blake3-aes-128-gcm or 2022-blake3-aes-256-gcm")
		}
		// A password, like every other kind's credential. Nothing upstream mints
		// one for a legacy cipher -- normalizeShadowsocksClientKeys returns on its
		// second line for those -- so an empty one reached xray, which refuses the
		// user and with it the whole config. Emptiness only: a wrong-SIZE 2022 key
		// is repaired before this runs, and refusing it here would strand a client
		// an older build let in. F328.
		if have[core.CredPassword] == "" {
			return errors.New("shadowsocks client requires a password")
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
		/*
			A kind that names no credentials has no clients of its own.

			ClientCredentials returns nil for http, mixed, tunnel and tun, and
			MintClientCredentials has no arm for them either -- the rendered
			inbound has no user list to put a client in. This arm only asked for a
			non-empty uuid, so one was accepted, stored, listed and provisioned
			nowhere: a client that can never connect, on an inbound that was never
			going to serve it. The panel's own pickers already exclude these kinds,
			so the path here is the API and the Telegram bot. F313.
		*/
		if len(c.ClientCredentials(kind)) == 0 {
			return errors.New(string(kind) + " inbounds serve no clients of their own")
		}
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
