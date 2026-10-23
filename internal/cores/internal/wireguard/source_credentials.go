package wireguard

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/core"
)

func (c *Core) SourceClientCredentials(kind core.Kind) []string {
	return c.ClientCredentials(kind)
}

func (c *Core) MintSourceClientCredentials(kind core.Kind, settings string, have map[string]string) (core.SourceCredentialResult, error) {
	if len(c.SourceClientCredentials(kind)) == 0 {
		return core.SourceCredentialResult{}, core.ErrKindHasNoUsers
	}
	minted, err := c.MintClientCredentials(kind, settings, have)
	if err != nil {
		return core.SourceCredentialResult{}, err
	}
	return core.SourceCredentialAdditions(have, minted)
}

func (c *Core) ValidateSourceClientCredentials(kind core.Kind, settings, email string, have map[string]string) error {
	if len(c.SourceClientCredentials(kind)) == 0 {
		return core.ErrKindHasNoUsers
	}
	for _, name := range []string{core.CredPublicKey, core.CredPrivateKey, core.CredPreSharedKey} {
		value := have[name]
		if value == "" && name != core.CredPublicKey {
			continue
		}
		key, err := wgtypes.ParseKey(value)
		if err != nil || key == (wgtypes.Key{}) || key.String() != value {
			return fmt.Errorf("wireguard source client requires a canonical nonzero %s", name)
		}
		if name == core.CredPrivateKey && key.PublicKey().String() != have[core.CredPublicKey] {
			return errors.New("wireguard source client private and public keys do not match")
		}
	}
	if raw := strings.TrimSpace(have[core.CredAllowedIPs]); raw != "" {
		if len(raw) > 4096 || strings.Count(raw, ",") >= 128 {
			return errors.New("wireguard source allowedIPs exceeds 128 prefixes or 4096 bytes")
		}
		for value := range strings.SplitSeq(raw, ",") {
			if _, err := netip.ParsePrefix(strings.TrimSpace(value)); err != nil {
				return errors.New("wireguard source allowedIPs contains an invalid prefix")
			}
		}
	}
	return c.ValidateClient(kind, settings, email, have)
}
