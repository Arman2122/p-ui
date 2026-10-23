package openvpn

import (
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrNodeConfiguration = errors.New("invalid headless OpenVPN configuration")

func ParseNodeSettings(raw string) (Settings, error) { return parseSettings(raw) }

type NodeConfiguration struct {
	ID                  uint32       `json:"id"`
	InboundTag          string       `json:"inboundTag,omitempty"`
	Port                uint16       `json:"port"`
	Listen              netip.Addr   `json:"listen"`
	Proto               string       `json:"proto"`
	Subnet              netip.Prefix `json:"subnet"`
	MTU                 uint16       `json:"mtu"`
	DNS                 []netip.Addr `json:"dns,omitempty"`
	RedirectGateway     bool         `json:"redirectGateway,omitempty"`
	ServerCert          string       `json:"serverCert"`
	ServerKey           string       `json:"serverKey"`
	TLSCryptV2ServerKey string       `json:"tlsCryptV2ServerKey"`
	Clients             []NodeClient `json:"clients"`
}

type NodeClient struct {
	ClientID  string       `json:"clientId"`
	Username  string       `json:"username"`
	Password  string       `json:"password"`
	Addresses []netip.Addr `json:"addresses"`
}

func (c NodeConfiguration) Device() string      { return "phvo" + strconv.FormatUint(uint64(c.ID), 10) }
func (c NodeConfiguration) Address() netip.Addr { return c.Subnet.Addr().Next() }

func (c NodeConfiguration) Validate() error {
	if c.ID == 0 || c.ID > 999999999 || len(c.InboundTag) > 128 || strings.ContainsRune(c.InboundTag, 0) || c.Port < 1024 || !c.Listen.Is4() || c.Listen.IsMulticast() ||
		(c.Proto != ProtoUDP && c.Proto != ProtoTCP) || !c.Subnet.Addr().Is4() || !c.Subnet.Addr().IsPrivate() || c.Subnet != c.Subnet.Masked() ||
		c.Subnet.Bits() < 16 || c.Subnet.Bits() > 29 || c.MTU < 1280 || c.MTU > 1420 || len(c.DNS) > 8 || len(c.Clients) > 60000 {
		return ErrNodeConfiguration
	}
	if len(c.ServerCert)+len(c.ServerKey) > 32768 || len(c.TLSCryptV2ServerKey) > 1024 {
		return ErrNodeConfiguration
	}
	if _, err := tls.X509KeyPair([]byte(c.ServerCert), []byte(c.ServerKey)); err != nil {
		return ErrNodeConfiguration
	}
	key, rest := pem.Decode([]byte(c.TLSCryptV2ServerKey))
	if key == nil || key.Type != "OpenVPN tls-crypt-v2 server key" || len(key.Bytes) != 128 || len(strings.TrimSpace(string(rest))) != 0 {
		return ErrNodeConfiguration
	}
	for _, ip := range c.DNS {
		if !ip.IsValid() || ip.Is4In6() || ip.Zone() != "" || !ip.IsGlobalUnicast() {
			return ErrNodeConfiguration
		}
	}
	names, clients, addresses := map[string]bool{}, map[string]bool{}, map[netip.Addr]bool{}
	for _, client := range c.Clients {
		if !nodeUsername(client.Username) || names[client.Username] || client.ClientID == "" || clients[client.ClientID] ||
			len(client.Password) < 8 || len(client.Password) > 512 || strings.ContainsAny(client.Password, "\x00\r\n") || len(client.Addresses) == 0 || len(client.Addresses) > 32 {
			return ErrNodeConfiguration
		}
		names[client.Username], clients[client.ClientID] = true, true
		for _, ip := range client.Addresses {
			if !ip.Is4() || !c.Subnet.Contains(ip) || ip == c.Subnet.Addr() || ip == c.Address() || !c.Subnet.Contains(ip.Next()) || addresses[ip] {
				return ErrNodeConfiguration
			}
			addresses[ip] = true
		}
	}
	return nil
}

func nodeUsername(name string) bool {
	if len(name) < 3 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '@', r == '-':
		default:
			return false
		}
	}
	return name != "." && name != ".."
}

func (c NodeConfiguration) Netmask() string {
	mask := ^uint32(0) << (32 - c.Subnet.Bits())
	return fmt.Sprintf("%d.%d.%d.%d", byte(mask>>24), byte(mask>>16), byte(mask>>8), byte(mask))
}

func (c NodeConfiguration) Render(dir, executable, management string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	for _, path := range []string{dir, executable, management} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n\"\\") {
			return "", ErrNodeConfiguration
		}
	}
	var out strings.Builder
	proto := "udp4"
	if c.Proto == ProtoTCP {
		proto = "tcp4-server"
	}
	fmt.Fprintf(&out, "port %d\nlocal %s\nproto %s\ndev %s\ndev-type tun\ntopology subnet\nserver %s %s nopool\n", c.Port, c.Listen, proto, c.Device(), c.Subnet.Addr(), c.Netmask())
	out.WriteString("ifconfig-noexec\nroute-noexec\ndisable-dco\nverify-client-cert none\nusername-as-common-name\nduplicate-cn\n")
	fmt.Fprintf(&out, "tun-mtu %d\nca %q\ncert %q\nkey %q\ntls-crypt-v2 %q\ndh none\n", c.MTU, filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key"), filepath.Join(dir, "tc2.key"))
	fmt.Fprintf(&out, "management %q unix\nmanagement-client\nmanagement-client-auth\nmanagement-hold\n", management)
	fmt.Fprintf(&out, "script-security 2\nlearn-address \"%s openvpn-learn %s\"\n", executable, filepath.Join(dir, "allow"))
	out.WriteString("data-ciphers AES-256-GCM:CHACHA20-POLY1305\nauth SHA256\ntls-version-min 1.2\nkeepalive 10 60\npersist-key\npersist-tun\nmax-clients 60000\nmax-routes-per-client 1\nverb 2\nmute 20\n")
	if c.Proto == ProtoUDP {
		out.WriteString("explicit-exit-notify 1\n")
	}
	if c.RedirectGateway {
		out.WriteString("push \"redirect-gateway def1 bypass-dhcp\"\n")
	}
	for _, ip := range c.DNS {
		fmt.Fprintf(&out, "push \"dhcp-option DNS %s\"\n", ip)
	}
	return out.String(), nil
}
