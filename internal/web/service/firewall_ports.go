package service

import (
	"fmt"
	"sort"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
FirewallPort is one line of the report the installer's firewall step reads.

The shell is a consumer, so this shape is an interface and not a convenience:
one record per line, "<port>/<proto> <role>", stable field order, no header, no
colour, no localisation, and nothing at all when there is nothing to say. A
script does `while read spec role`; adding a column is a breaking change and
should be treated as one.

Roles are panel, subscription, inbound and inbound-disabled. The last exists
because a disabled inbound's port is still opened: an allowed port with no
listener answers nothing, while leaving it closed means the operator enables
that inbound next month and the firewall eats it silently, with nothing to
re-run the menu.
*/
type FirewallPort struct {
	Port  int
	Proto string
	Role  string
}

func (p FirewallPort) String() string { return fmt.Sprintf("%d/%s %s", p.Port, p.Proto, p.Role) }

/*
FirewallPorts is every port this host binds on the panel's behalf.

Node inbounds are excluded: they are served by the node's own process, not this
one, so opening their ports here protects nothing and advertises a service that
is not present. The menu says so in a sentence rather than this answering it —
a host serving another panel's inbounds is a case for the operator, not for
node-detection machinery.
*/
func (s *InboundService) FirewallPorts() ([]FirewallPort, error) {
	setting := &SettingService{}
	out := make([]FirewallPort, 0, 8)

	webPort, err := setting.GetPort()
	if err != nil {
		return nil, err
	}
	// What web.go actually binds, which is the env override when one is set.
	if envPort, configured, envErr := config.GetPortOverride(); configured && envErr == nil {
		webPort = envPort
	}
	out = append(out, FirewallPort{Port: webPort, Proto: "tcp", Role: "panel"})

	subEnable, err := setting.GetSubEnable()
	if err != nil {
		return nil, err
	}
	if subEnable {
		subPort, sErr := setting.GetSubPort()
		if sErr != nil {
			return nil, sErr
		}
		out = append(out, FirewallPort{Port: subPort, Proto: "tcp", Role: "subscription"})
	}

	var inbounds []*model.Inbound
	if err := database.GetDB().Where("node_id IS NULL").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	for _, inbound := range inbounds {
		role := "inbound"
		if !inbound.Enable {
			role = "inbound-disabled"
		}
		// The core's own answer, the same source the port-conflict check uses,
		// so the firewall and that check can never disagree about a protocol.
		bits := inboundTransports(inbound.Protocol, inbound.StreamSettings, inbound.Settings)
		if bits&transportTCP != 0 {
			out = append(out, FirewallPort{Port: inbound.Port, Proto: "tcp", Role: role})
		}
		if bits&transportUDP != 0 {
			out = append(out, FirewallPort{Port: inbound.Port, Proto: "udp", Role: role})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	return out, nil
}
