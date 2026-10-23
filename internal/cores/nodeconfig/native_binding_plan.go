package nodeconfig

import (
	"net/netip"

	"github.com/Arman2122/p-ui/internal/core"
	awgcore "github.com/Arman2122/p-ui/internal/cores/internal/amneziawg"
	"github.com/Arman2122/p-ui/internal/cores/internal/ocserv"
	"github.com/Arman2122/p-ui/internal/cores/internal/openvpn"
	wgcore "github.com/Arman2122/p-ui/internal/cores/internal/wireguard"
)

func NativeExplicitAddresses(inbound Inbound) ([]netip.Addr, error) {
	if inbound.Instance.Kind != wgcore.Kind && inbound.Instance.Kind != awgcore.Kind {
		return nil, nil
	}
	var settings wireGuardSettings
	if strictSettings(inbound.Instance.Settings, &settings) != nil {
		return nil, ErrInvalid
	}
	var addresses []netip.Addr
	if len(inbound.Native.Addresses) != 0 {
		for _, prefix := range inbound.Native.Addresses {
			if !prefix.IsValid() {
				return nil, ErrInvalid
			}
			addresses = append(addresses, prefix.Addr())
		}
	} else {
		for _, raw := range settings.Address {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil {
				return nil, ErrInvalid
			}
			addresses = append(addresses, prefix.Addr())
		}
	}
	for _, client := range inbound.Clients {
		if len(client.Native.Addresses) != 0 {
			addresses = append(addresses, client.Native.Addresses...)
			continue
		}
		values, err := credentialAddresses(client.User.Credentials[core.CredAllowedIPs])
		if err != nil {
			return nil, err
		}
		addresses = append(addresses, values...)
	}
	return addresses, nil
}

type NativeAddressRequest struct {
	Pool        netip.Prefix
	PrefixBits  int
	MaxSessions uint16
	Reserved    []netip.Addr
}

func NativeAddressRequestFor(inbound Inbound) (*NativeAddressRequest, error) {
	var raw string
	request := new(NativeAddressRequest)
	switch inbound.Instance.Kind {
	case openvpn.ID:
		settings, err := openvpn.ParseNodeSettings(inbound.Instance.Settings)
		if err != nil {
			return nil, ErrInvalid
		}
		raw, request.PrefixBits = settings.Subnet, 32
	case ocserv.ID:
		settings, err := ocserv.ParseNodeSettings(inbound.Instance.Settings)
		if err != nil {
			return nil, ErrInvalid
		}
		raw, request.PrefixBits, request.MaxSessions = settings.Subnet, 29, 1
	default:
		return nil, nil
	}
	pool, err := nativeSubnet(inbound, raw)
	if err != nil {
		return nil, err
	}
	if pool.Bits() < 16 || pool.Bits() > 29 {
		return nil, ErrUnsupported
	}
	request.Pool = pool
	if request.PrefixBits == 32 {
		request.Reserved = []netip.Addr{pool.Addr(), pool.Addr().Next()}
	}
	return request, nil
}
