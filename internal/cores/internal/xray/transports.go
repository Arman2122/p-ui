package xray

import (
	"encoding/json"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
Transports answers what one Xray inbound actually binds.

Three rules, and all three are Xray's own: its stream layer moves kcp and quic
to UDP while every other transport stays TCP; a shadowsocks or tunnel inbound
overrides that outright with a CSV network field; a mixed inbound adds UDP with
a boolean, because socks5 UDP associate shares the port it was negotiated on.

Parse failures fall back to TCP rather than to nothing: a port this cannot read
must still be treated as occupied, or conflict detection quietly stops
protecting the one inbound whose settings are malformed.
*/
func (c *Core) Transports(kind core.Kind, settings, streamSettings string) core.Transports {
	// Two of Xray's kinds dial their own UDP and read no stream settings at all,
	// so the transport peek below would answer TCP for them.
	switch kind {
	case "hysteria", "wireguard":
		return core.Transports{UDP: true}
	}

	out := core.Transports{}

	network := ""
	if streamSettings != "" {
		var ss map[string]any
		if json.Unmarshal([]byte(streamSettings), &ss) == nil {
			network, _ = ss["network"].(string)
		}
	}
	switch network {
	case "kcp", "quic":
		out.UDP = true
	default:
		out.TCP = true
	}

	if settings != "" {
		var st map[string]any
		if json.Unmarshal([]byte(settings), &st) == nil {
			switch kind {
			case "shadowsocks", "tunnel":
				key := "network"
				if kind == "tunnel" {
					key = "allowedNetwork"
				}
				if n, ok := st[key].(string); ok && n != "" {
					out = core.Transports{}
					for part := range strings.SplitSeq(n, ",") {
						switch strings.TrimSpace(part) {
						case "tcp":
							out.TCP = true
						case "udp":
							out.UDP = true
						}
					}
				}
			case "mixed":
				if udpOn, _ := st["udp"].(bool); udpOn {
					out.UDP = true
				}
			}
		}
	}

	if !out.TCP && !out.UDP {
		out.TCP = true
	}
	return out
}
