package tgbot

import (
	"slices"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
offerableToTelegram reports whether the bot can hand a user this inbound.

Asked of the registry rather than kept as a list of exclusions, because a list
fails the wrong way round: a protocol missing from it is OFFERED, and the user
gets a button that produces a config the bot cannot build. AmneziaWG arrived and
was offered for exactly that reason.

What the bot can hand out is a URI. A client configured by a FILE -- every
WireGuard-family tunnel -- has none, and the rest are the local shapes that were
never a subscription in the first place.
*/
func offerableToTelegram(protocol model.Protocol) bool {
	if slices.Contains(cores.ClientCredentials(core.Kind(protocol)), core.CredAllowedIPs) {
		return false
	}
	switch protocol {
	case model.Tunnel, model.Mixed, model.HTTP, model.Tun:
		return false
	}
	return true
}
