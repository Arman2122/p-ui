package xray

import (
	"encoding/json"
	"errors"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
ValidateInstance refuses an inbound configuration Xray would serve into a loop.

One rule so far, and it is the panel's own default that trips it. A tunnel
(dokodemo) inbound resolves its destination like this, measured against the
pinned core (proxy/dokodemo/dokodemo.go): with followRedirect off it takes
rewriteAddress, and where that is nil it substitutes 127.0.0.1 -- then takes
rewritePort, and where that is 0 it substitutes THE CONNECTION'S OWN LOCAL PORT.

So an inbound with neither dials itself. Every accepted connection opens another,
from one external probe, until the file descriptors of the xray process serving
every other inbound on the box are gone. createDefaultTunnelInboundSettings
shipped exactly that shape.

Deliberately narrow: a non-empty portMap can override the port per connection,
and a rewritePort alone points at some other local service, so both are left
alone. This refuses the combination that can only ever be the loop. F316.
*/
func (c *Core) ValidateInstance(kind core.Kind, settings string) error {
	if kind != "tunnel" {
		return nil
	}
	var parsed struct {
		RewriteAddress string            `json:"rewriteAddress"`
		RewritePort    int               `json:"rewritePort"`
		PortMap        map[string]string `json:"portMap"`
		FollowRedirect bool              `json:"followRedirect"`
	}
	if settings == "" || json.Unmarshal([]byte(settings), &parsed) != nil {
		// Unreadable settings are not this rule's business; the config builder
		// refuses them with its own message.
		return nil
	}
	if parsed.FollowRedirect || parsed.RewriteAddress != "" ||
		parsed.RewritePort != 0 || len(parsed.PortMap) > 0 {
		return nil
	}
	return errors.New("a tunnel inbound with no destination forwards to itself: " +
		"set a rewrite address or port, add a port map, or turn on follow redirect. " +
		"Left as it is, every connection it accepts opens another one to itself until " +
		"the core runs out of file descriptors")
}
