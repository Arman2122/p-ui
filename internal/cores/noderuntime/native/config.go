package nativeruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strconv"

	"github.com/google/uuid"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/awg"
	ocserv "github.com/Arman2122/p-ui/internal/cores/internal/ocserv"
	openvpn "github.com/Arman2122/p-ui/internal/cores/internal/openvpn"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/native"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

type Configuration struct {
	Network     *native.NetworkBinding      `json:"network,omitempty"`
	WireGuard   []WireGuard                 `json:"wireguard"`
	AmneziaWG   []AmneziaWG                 `json:"amneziawg,omitempty"`
	OpenVPN     []openvpn.NodeConfiguration `json:"openvpn,omitempty"`
	OpenConnect []ocserv.NodeConfiguration  `json:"openconnect,omitempty"`
}

type (
	NetworkBinding    = native.NetworkBinding
	OpenVPN           = openvpn.NodeConfiguration
	OpenVPNClient     = openvpn.NodeClient
	OpenConnect       = ocserv.NodeConfiguration
	OpenConnectClient = ocserv.NodeClient
)

type AmneziaWG struct {
	WireGuard
	Parameters awg.Params `json:"parameters"`
}

type WireGuard struct {
	ID         uint32         `json:"id"`
	Port       uint16         `json:"port"`
	PrivateKey string         `json:"privateKey"`
	Addresses  []netip.Prefix `json:"addresses"`
	MTU        uint16         `json:"mtu"`
	Clients    []Peer         `json:"clients"`
	prefix     string
	padding    int
}

type Peer struct {
	ClientID     string       `json:"clientId"`
	PublicKey    string       `json:"publicKey"`
	PreSharedKey string       `json:"preSharedKey,omitempty"`
	Addresses    []netip.Addr `json:"addresses"`
	KeepAlive    uint16       `json:"keepAlive,omitempty"`
}

type compiled struct {
	config      Configuration
	instances   []engine.Instance
	amnezia     []engine.Instance
	assignments []native.Assignment
	selectors   map[string][]native.Selector
}

func canonicalID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validKey(value string) bool {
	key, err := wgtypes.ParseKey(value)
	return err == nil && key != (wgtypes.Key{}) && key.String() == value
}

func privateAddress(ip netip.Addr) bool {
	return ip.IsValid() && !ip.Is4In6() && ip.Zone() == "" && ip.IsPrivate()
}

func compile(runtime *configuration.Runtime) (compiled, error) {
	var out compiled
	if runtime == nil {
		return out, nil
	}
	grants := make(map[string]governor.Grant, len(runtime.Grants))
	grantIDs := make(map[string]bool, len(runtime.Grants))
	var clients []string
	for _, grant := range runtime.Grants {
		if governor.Validate(grant) != nil || grants[grant.ClientID].ID != "" || grantIDs[grant.ID] {
			return out, configuration.ErrConfiguration
		}
		grants[grant.ClientID], grantIDs[grant.ID] = grant, true
		clients = append(clients, grant.ClientID)
	}
	if admission.Validate(runtime.Admissions) != nil {
		return out, configuration.ErrConfiguration
	}
	known := make(map[string]bool, len(clients))
	for _, client := range clients {
		known[client] = true
	}
	for _, descriptor := range runtime.Admissions {
		if !known[descriptor.ClientID] {
			clients = append(clients, descriptor.ClientID)
			known[descriptor.ClientID] = true
		}
	}
	out, err := compileConfiguration(runtime.Configuration, clients)
	if err != nil {
		return out, err
	}
	if err := validateNativeAdmissions(out.config, runtime.Admissions, grants); err != nil {
		return out, err
	}
	for client, selected := range out.selectors {
		if grant, funded := grants[client]; funded {
			out.assignments = append(out.assignments, native.Assignment{Grant: grant, Selectors: selected})
		}
	}
	sort.Slice(out.assignments, func(i, j int) bool { return out.assignments[i].Grant.ID < out.assignments[j].Grant.ID })
	if err := native.ValidateAssignments(out.assignments); err != nil {
		return out, err
	}
	return out, nil
}

func ValidateConfiguration(data []byte, clientIDs []string) error {
	_, err := compileConfiguration(data, clientIDs)
	return err
}

func compileConfiguration(data []byte, clientIDs []string) (compiled, error) {
	var out compiled
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 32<<20 || decoder.Decode(&out.config) != nil || decoder.Decode(new(any)) != io.EOF || len(out.config.WireGuard)+len(out.config.AmneziaWG)+len(out.config.OpenVPN)+len(out.config.OpenConnect) > 4096 {
		return out, configuration.ErrConfiguration
	}
	if out.config.Network == nil {
		binding := native.DefaultNetworkBinding()
		out.config.Network = &binding
	}
	if err := out.config.Network.Validate(); err != nil {
		return out, err
	}
	clients := make(map[string]bool, len(clientIDs))
	for _, client := range clientIDs {
		if !canonicalID(client) || clients[client] {
			return out, configuration.ErrConfiguration
		}
		clients[client] = true
	}
	ids, ports, addresses := make(map[string]bool), make(map[uint16]bool), make(map[netip.Addr]bool)
	addresses[out.config.Network.PeerIPv4], addresses[out.config.Network.GatewayIPv4] = true, true
	selectors := make(map[string][]native.Selector)
	var inbounds []WireGuard
	for _, inbound := range out.config.WireGuard {
		inbound.prefix = "phvw"
		inbounds = append(inbounds, inbound)
	}
	for _, inbound := range out.config.AmneziaWG {
		if err := inbound.Parameters.Validate(); err != nil {
			return out, err
		}
		if inbound.Parameters.S4 > engine.DefaultMTU-engine.MinMTU || inbound.MTU != 0 && int(inbound.MTU)+int(inbound.Parameters.S4) > engine.DefaultMTU {
			return out, configuration.ErrConfiguration
		}
		inbound.prefix, inbound.padding = "phva", int(inbound.Parameters.S4)
		inbounds = append(inbounds, inbound.WireGuard)
	}
	for _, inbound := range inbounds {
		device := inbound.prefix + strconv.FormatUint(uint64(inbound.ID), 10)
		if inbound.ID == 0 || inbound.ID > 999999999 || ids[device] || inbound.Port == 0 || ports[inbound.Port] || !validKey(inbound.PrivateKey) ||
			len(inbound.Addresses) == 0 || len(inbound.Addresses) > 2 || inbound.MTU != 0 && (inbound.MTU < 1280 || inbound.MTU > 1420) {
			return out, fmt.Errorf("%w: invalid WireGuard interface", configuration.ErrConfiguration)
		}
		ids[device], ports[inbound.Port] = true, true
		inst := engine.Instance{ID: int(inbound.ID), Tag: "native-" + device, Port: int(inbound.Port), PrivateKey: inbound.PrivateKey, MTU: int(inbound.MTU), TransportPadding: inbound.padding}
		families := make(map[int]bool)
		for _, prefix := range inbound.Addresses {
			ip := prefix.Addr()
			if !privateAddress(ip) || !prefix.IsSingleIP() || addresses[ip] || families[ip.BitLen()] {
				return out, fmt.Errorf("%w: WireGuard needs unique private host addresses", configuration.ErrConfiguration)
			}
			addresses[ip], families[ip.BitLen()] = true, true
			inst.Address = append(inst.Address, prefix.String())
		}
		keys, inboundClients := make(map[string]bool), make(map[string]bool)
		for _, peer := range inbound.Clients {
			if !canonicalID(peer.ClientID) || !clients[peer.ClientID] || inboundClients[peer.ClientID] || keys[peer.PublicKey] || !validKey(peer.PublicKey) ||
				peer.PreSharedKey != "" && !validKey(peer.PreSharedKey) || len(peer.Addresses) == 0 || len(peer.Addresses) > 2 {
				return out, fmt.Errorf("%w: invalid WireGuard client or missing grant", configuration.ErrConfiguration)
			}
			keys[peer.PublicKey], inboundClients[peer.ClientID] = true, true
			user := engine.Peer{Email: peer.ClientID, PublicKey: peer.PublicKey, PreSharedKey: peer.PreSharedKey, KeepAlive: int(peer.KeepAlive)}
			peerFamilies := make(map[int]bool)
			for _, ip := range peer.Addresses {
				if !privateAddress(ip) || addresses[ip] || !families[ip.BitLen()] || peerFamilies[ip.BitLen()] {
					return out, fmt.Errorf("%w: conflicting WireGuard client address", configuration.ErrConfiguration)
				}
				addresses[ip], peerFamilies[ip.BitLen()] = true, true
				user.AllowedIPs = append(user.AllowedIPs, netip.PrefixFrom(ip, ip.BitLen()).String())
				selectors[peer.ClientID] = append(selectors[peer.ClientID], native.Selector{Device: device, Address: ip})
			}
			inst.Peers = append(inst.Peers, user)
		}
		if inbound.prefix == "phva" {
			out.amnezia = append(out.amnezia, inst)
		} else {
			out.instances = append(out.instances, inst)
		}
	}
	for _, inbound := range out.config.OpenVPN {
		if err := inbound.Validate(); err != nil {
			return out, fmt.Errorf("%w: %w", configuration.ErrConfiguration, err)
		}
		if ids[inbound.Device()] || ports[inbound.Port] || addresses[inbound.Address()] {
			return out, configuration.ErrConfiguration
		}
		ids[inbound.Device()], ports[inbound.Port], addresses[inbound.Address()] = true, true, true
		for _, client := range inbound.Clients {
			if !canonicalID(client.ClientID) || !clients[client.ClientID] {
				return out, configuration.ErrConfiguration
			}
			for _, ip := range client.Addresses {
				if addresses[ip] {
					return out, configuration.ErrConfiguration
				}
				addresses[ip] = true
				selectors[client.ClientID] = append(selectors[client.ClientID], native.Selector{Device: inbound.Device(), Address: ip})
			}
		}
	}
	addressSlots := 0
	for _, inbound := range out.config.OpenConnect {
		if err := inbound.Validate(); err != nil {
			return out, fmt.Errorf("%w: %w", configuration.ErrConfiguration, err)
		}
		if ids[inbound.Prefix()] || ports[inbound.Port] {
			return out, configuration.ErrConfiguration
		}
		ids[inbound.Prefix()], ports[inbound.Port] = true, true
		for _, client := range inbound.Clients {
			if !canonicalID(client.ClientID) || !clients[client.ClientID] {
				return out, configuration.ErrConfiguration
			}
			addressSlots += 1 << (32 - client.Subnet.Bits())
			if addressSlots > 262144 {
				return out, configuration.ErrConfiguration
			}
			for ip := client.Subnet.Addr(); client.Subnet.Contains(ip); ip = ip.Next() {
				if addresses[ip] {
					return out, configuration.ErrConfiguration
				}
				addresses[ip] = true
			}
			if _, ok := selectors[client.ClientID]; !ok {
				selectors[client.ClientID] = nil
			}
		}
	}
	if len(selectors) > native.MaxClasses || len(selectors) != len(clients) {
		return out, fmt.Errorf("%w: unplaced native grant or client capacity exceeded", configuration.ErrConfiguration)
	}
	for _, selected := range selectors {
		if len(selected) > native.MaxSelectors {
			return out, configuration.ErrConfiguration
		}
	}
	out.selectors = selectors
	return out, nil
}
