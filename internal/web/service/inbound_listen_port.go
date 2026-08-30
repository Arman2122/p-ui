package service

import (
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
)

/*
validateInboundBind refuses a listen/port pair nothing can bind.

Both halves were unchecked. Listen carried no validate: tag and no custom
validator, and Port was tagged gte=0 — so `"port": 0` was stored and the core
bound whatever ephemeral port the OS handed it, changing on every restart, while
the inbound list, every share link and the port-conflict check all read 0.

xray-core refuses both, but only for an inbound that is enabled, local and
xray-served: validateInboundForXray returns nil for the rest, and no other core
looks at the bind at all. So the rule lives here, in the same terms xray uses —
an address needs a port, and a socket path does not. F009 / F039.
*/
func validateInboundBind(inbound *model.Inbound) error {
	if inbound == nil || inbound.NodeID != nil {
		return nil
	}
	listen := strings.TrimSpace(inbound.Listen)
	if isSocketListen(listen) {
		return nil
	}
	// "localhost" is the one name xray resolves for a bind; everything else it
	// refuses, and a typo'd host silently became one on a disabled inbound.
	if listen != "" && listen != "localhost" {
		if _, err := netip.ParseAddr(listen); err != nil {
			return common.NewError("listen must be an IP address, or a socket path starting with / or @:", inbound.Listen)
		}
	}
	if inbound.Port < 1 || inbound.Port > 65535 {
		return common.NewError("port must be between 1 and 65535, not", inbound.Port)
	}
	return nil
}

// A unix socket, absolute or abstract — the one bind that takes no port, which
// is what the listen field's own hint tells the operator to write.
func isSocketListen(listen string) bool {
	return strings.HasPrefix(listen, "/") || strings.HasPrefix(listen, "@")
}

/*
validateUpdatedClients asks the core about the clients an update brings in.

Only the ones it introduces or changes: a client that arrives byte-identical to
the stored one is not an edit, and refusing it would strand an inbound whose
clients an older build let in. A protocol change skips the comparison entirely
— under a new kind, every stored client is a new client.
*/
func (s *InboundService) validateUpdatedClients(inbound, oldInbound *model.Inbound) error {
	clients, err := s.GetClients(inbound)
	if err != nil {
		return err
	}
	unchanged := make(map[string]struct{})
	if inbound.Protocol == oldInbound.Protocol {
		stored, sErr := s.GetClients(oldInbound)
		if sErr != nil {
			return sErr
		}
		for i := range stored {
			unchanged[clientCredentialFingerprint(&stored[i])] = struct{}{}
		}
	}
	for i := range clients {
		if _, ok := unchanged[clientCredentialFingerprint(&clients[i])]; ok {
			continue
		}
		if err := validateClientForKind(inbound.Protocol, inbound.Settings, &clients[i]); err != nil {
			return err
		}
	}
	return nil
}

// The client's identity and every credential a core can be asked about, in a
// stable order — two clients with the same fingerprint are the same account.
func clientCredentialFingerprint(c *model.Client) string {
	values := clientCredentialValues(c)
	names := slices.Sorted(maps.Keys(values))
	parts := make([]string, 0, len(names)*2+1)
	parts = append(parts, c.Email)
	for _, name := range names {
		parts = append(parts, name, values[name])
	}
	return strings.Join(parts, "\x00")
}
