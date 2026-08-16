package wireguard

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

/*
RenderClient is the first LinkRenderer in the tree: a WireGuard client is
configured by a FILE, and no URI scheme carries one — the wireguard:// link the
subscription emits is a lossy transport the apps then rebuild a .conf from.
This is the .conf itself, and its field set deliberately mirrors the panel
frontend's buildWireguardClientConfig so the two surfaces hand out the same
tunnel.
*/
func (c *Core) RenderClient(inst core.Instance, user core.User, host string) (core.Share, error) {
	privateKey := core.CredString(user.Credentials, core.CredPrivateKey)
	if privateKey == "" {
		// A client without its private key has no config to hand out: the panel
		// never learns a key generated client-side, and must not invent one.
		return core.Share{}, fmt.Errorf("wgkernel: client %q carries no private key to render", user.Email)
	}

	var settings struct {
		SecretKey string  `json:"secretKey"`
		DNS       string  `json:"dns"`
		MTU       float64 `json:"mtu"`
	}
	if inst.Settings != "" {
		// Unreadable settings render a config without the server half, which
		// cannot connect; refusing names the reason instead.
		if err := json.Unmarshal([]byte(inst.Settings), &settings); err != nil {
			return core.Share{}, fmt.Errorf("wgkernel: inbound %d settings: %w", inst.ID, err)
		}
	}

	address := strings.Join(credStrings(user.Credentials, core.CredAllowedIPs), ", ")
	if address == "" {
		address = "10.0.0.2/32"
	}
	dns := settings.DNS
	if dns == "" {
		dns = "1.1.1.1, 1.0.0.1"
	}

	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", privateKey)
	fmt.Fprintf(&b, "Address = %s\n", address)
	fmt.Fprintf(&b, "DNS = %s\n", dns)
	if settings.MTU > 0 {
		fmt.Fprintf(&b, "MTU = %d\n", int(settings.MTU))
	}
	b.WriteString("\n")
	if remark := strings.TrimSpace(strings.Join([]string{inst.Tag, user.Email}, " - ")); remark != " - " {
		fmt.Fprintf(&b, "# %s\n", remark)
	}
	b.WriteString("[Peer]\n")
	if settings.SecretKey != "" {
		if pub, err := wgutil.PublicKeyFromPrivate(settings.SecretKey); err == nil {
			fmt.Fprintf(&b, "PublicKey = %s\n", pub)
		}
	}
	if psk := core.CredString(user.Credentials, core.CredPreSharedKey); psk != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", psk)
	}
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	fmt.Fprintf(&b, "Endpoint = %s:%d\n", host, inst.Port)
	if keepAlive, ok := user.Credentials["keepAlive"].(float64); ok && keepAlive > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", int(keepAlive))
	}

	return core.Share{
		Kind:     "file",
		Filename: shareFilename(user.Email),
		Body:     b.String(),
	}, nil
}

// shareFilename names the download after the client, reduced to characters a
// filesystem and a Content-Disposition header both take. wg-quick also reads
// the interface name from the filename, so it must stay a plausible one.
func shareFilename(email string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '-' || r == '_':
			return r
		}
		return '-'
	}, email)
	mapped = strings.Trim(mapped, "-.")
	if mapped == "" {
		mapped = "client"
	}
	// IFNAMSIZ bounds a wg-quick interface name at 15 characters.
	if len(mapped) > 15 {
		mapped = mapped[:15]
	}
	return mapped + ".conf"
}
