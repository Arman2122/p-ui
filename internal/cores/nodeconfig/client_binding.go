package nodeconfig

import (
	"net/netip"

	"github.com/Arman2122/p-ui/internal/core"
)

func ValidateClientBinding(kind core.Kind, subnet netip.Prefix, binding NativeClientBinding) error {
	switch kind {
	case "openvpn":
		if binding.Subnet.IsValid() || binding.MaxSessions != 0 {
			return ErrInvalid
		}
		if len(binding.Addresses) == 0 {
			return ErrClientBinding
		}
	case "ocserv":
		pool := binding.Subnet
		if len(binding.Addresses) != 0 || binding.MaxSessions > 32 || pool.IsValid() &&
			(pool != pool.Masked() || !pool.Addr().Is4() || pool.Bits() < 24 || pool.Bits() > 29 || !subnet.Contains(pool.Addr()) || pool.Bits() < subnet.Bits()) {
			return ErrInvalid
		}
		if !pool.IsValid() || binding.MaxSessions == 0 {
			return ErrClientBinding
		}
	}
	return nil
}
