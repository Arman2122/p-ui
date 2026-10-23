package xray

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
)

type TransportResolver func(core.Kind, string, string) core.Transports

type bindSocket struct {
	address core.BindAddress
	bits    uint8
	tag     string
}

type bindPair struct {
	left, right core.BindAddress
	port        int
	bits        uint8
}

type BindConflict struct {
	pair       bindPair
	tagA, tagB string
}

func (c BindConflict) String() string {
	network := "tcp"
	switch c.pair.bits {
	case 2:
		network = "udp"
	case 3:
		network = "tcp/udp"
	}
	return fmt.Sprintf("inbounds %q and %q both bind port %d (%s) on %q and %q",
		c.tagA, c.tagB, c.pair.port, network, c.pair.left.Listen, c.pair.right.Listen)
}

// BindConflicts excuses only socket pairs already served by the running core.
// Counts matter: a third listener cannot reuse one established pair's exemption.
func BindConflicts(desired, running *Config, resolve TransportResolver) []BindConflict {
	excused := map[bindPair]int{}
	for _, conflict := range rawBindConflicts(running, resolve) {
		excused[conflict.pair]++
	}
	var conflicts []BindConflict
	for _, conflict := range rawBindConflicts(desired, resolve) {
		if excused[conflict.pair] > 0 {
			excused[conflict.pair]--
		} else {
			conflicts = append(conflicts, conflict)
		}
	}
	return conflicts
}

func rawBindConflicts(config *Config, resolve TransportResolver) []BindConflict {
	if config == nil {
		return nil
	}
	byPort := map[int][]InboundConfig{}
	for _, inbound := range config.InboundConfigs {
		if inbound.Port > 0 {
			byPort[inbound.Port] = append(byPort[inbound.Port], inbound)
		}
	}
	var conflicts []BindConflict
	for port, inbounds := range byPort {
		if len(inbounds) < 2 {
			continue
		}
		var sockets []bindSocket
		for _, inbound := range inbounds {
			socket := generatedBindSocket(inbound, resolve)
			if socket.bits != 0 {
				sockets = append(sockets, socket)
			}
		}
		for i, left := range sockets {
			for _, right := range sockets[i+1:] {
				shared := left.bits & right.bits
				if shared == 0 || !core.BindListenOverlaps(left.address, right.address) {
					continue
				}
				a, b := left.address, right.address
				if cmp.Compare(a.Listen, b.Listen) > 0 || (a.Listen == b.Listen && a.V6Only && !b.V6Only) {
					a, b = b, a
				}
				conflicts = append(conflicts, BindConflict{bindPair{a, b, port, shared}, left.tag, right.tag})
			}
		}
	}
	slices.SortFunc(conflicts, func(a, b BindConflict) int {
		return cmp.Or(cmp.Compare(a.pair.port, b.pair.port), cmp.Compare(a.tagA, b.tagA), cmp.Compare(a.tagB, b.tagB))
	})
	return conflicts
}

func generatedBindSocket(inbound InboundConfig, resolve TransportResolver) bindSocket {
	out := bindSocket{tag: inbound.Tag}
	_ = json.Unmarshal(inbound.Listen, &out.address.Listen)
	if strings.HasPrefix(out.address.Listen, "/") || strings.HasPrefix(out.address.Listen, "@") {
		return out
	}
	if out.address.Listen == "" {
		out.address.Listen = "0.0.0.0"
	} else if ip := net.ParseIP(out.address.Listen); ip != nil {
		out.address.Listen = ip.String()
	}
	var stream struct {
		Sockopt struct {
			V6Only bool `json:"v6only"`
		} `json:"sockopt"`
	}
	_ = json.Unmarshal(inbound.StreamSettings, &stream)
	out.address.V6Only = stream.Sockopt.V6Only
	kind := core.Kind(strings.ToLower(inbound.Protocol))
	switch kind {
	case "socks":
		kind = "mixed"
	case "dokodemo-door":
		kind = "tunnel"
	}
	transports := resolve(kind, string(inbound.Settings), string(inbound.StreamSettings))
	if !transports.None {
		if transports.TCP {
			out.bits |= 1
		}
		if transports.UDP {
			out.bits |= 2
		}
		if out.bits == 0 {
			out.bits = 1
		}
	}
	return out
}

// CheckBindings leaves a working process untouched when a new generated listener collides.
// With no running predecessor, the core remains the final authority at startup.
func (m *Manager) CheckBindings(desired *Config, resolve TransportResolver) error {
	process := m.Current()
	var running *Config
	if process != nil && process.IsRunning() {
		running = process.GetConfig()
	}
	conflicts := BindConflicts(desired, running, resolve)
	if len(conflicts) == 0 {
		return nil
	}
	if running == nil {
		logger.Warning("xray config may not start:", conflicts[0].String())
		return nil
	}
	err := fmt.Errorf("xray config refused: %s", conflicts[0])
	m.SetHeldBack(process, err.Error())
	return err
}
