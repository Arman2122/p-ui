package nodeconfig

import (
	"encoding/json"
	"encoding/pem"
	"net/netip"
	"slices"
	"sort"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores/internal/ocserv"
	"github.com/Arman2122/p-ui/internal/cores/internal/openvpn"
	nativeruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/native"
)

func nativeVPNListen(inbound Inbound) (netip.Addr, error) {
	if len(inbound.Native.Addresses) != 0 {
		return netip.Addr{}, issue(inbound, "", "server addresses", ErrUnsupported)
	}
	if inbound.Instance.Listen == "" {
		return netip.IPv4Unspecified(), nil
	}
	address, err := literalListen(inbound.Instance.Listen)
	if err != nil || !address.Is4() {
		return netip.Addr{}, issue(inbound, "", "listen", ErrUnsupported)
	}
	return address, nil
}

func nativeDNS(values []string) ([]netip.Addr, error) {
	var addresses []netip.Addr
	for _, value := range values {
		address, err := netip.ParseAddr(value)
		if err != nil || !address.IsGlobalUnicast() {
			return nil, ErrInvalid
		}
		if !address.Is4() {
			return nil, ErrUnsupported
		}
		addresses = append(addresses, address)
	}
	return addresses, nil
}

func nativeSubnet(inbound Inbound, raw string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return netip.Prefix{}, issue(inbound, "", "subnet", ErrInvalid)
	}
	prefix = prefix.Masked()
	if inbound.Native.Subnet.IsValid() {
		prefix = inbound.Native.Subnet
		if prefix != prefix.Masked() {
			return netip.Prefix{}, issue(inbound, "", "subnet", ErrInvalid)
		}
	}
	if !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() {
		return netip.Prefix{}, issue(inbound, "", "subnet", ErrUnsupported)
	}
	return prefix, nil
}

func (r *renderer) nativeIdentity(inbound Inbound, certificate, privateKey string) (string, string, error) {
	cert, err := r.asset(inbound, "native", "serverCert", AssetCertificate, AssetEmbedded)
	if err != nil || !sameMaterial(certificate, cert.Data) {
		return "", "", issue(inbound, "", "serverCert", ErrDependency)
	}
	key, err := r.asset(inbound, "native", "serverKey", AssetPrivateKey, AssetEmbedded)
	if err != nil || !sameMaterial(privateKey, key.Data) {
		return "", "", issue(inbound, "", "serverKey", ErrDependency)
	}
	return string(cert.Data), string(key.Data), nil
}

func (r *renderer) nativeOpenVPN(inbound Inbound, clients []Client) (nativeruntime.OpenVPN, error) {
	var result nativeruntime.OpenVPN
	var shape struct {
		openvpn.Settings
		Clients []json.RawMessage `json:"clients"`
	}
	if strictSettings(inbound.Instance.Settings, &shape) != nil || allowedAssetFields(inbound, "serverCert", "serverKey", "tlsCryptV2ServerKey") != nil {
		return result, issue(inbound, "", "settings", ErrInvalid)
	}
	settings, err := openvpn.ParseNodeSettings(inbound.Instance.Settings)
	if err != nil {
		return result, issue(inbound, "", "settings", ErrInvalid)
	}
	listen, err := nativeVPNListen(inbound)
	if err != nil {
		return result, err
	}
	subnet, err := nativeSubnet(inbound, settings.Subnet)
	if err != nil {
		return result, err
	}
	dns, err := nativeDNS(settings.DNS)
	if err != nil {
		return result, issue(inbound, "", "dns", err)
	}
	certificate, privateKey, err := r.nativeIdentity(inbound, settings.ServerCert, settings.ServerKey)
	if err != nil {
		return result, err
	}
	key, err := r.asset(inbound, "native", "tlsCryptV2ServerKey", AssetTLSCryptV2, AssetEmbedded)
	if err != nil || !sameMaterial(settings.TLSCryptV2ServerKey, key.Data) {
		return result, issue(inbound, "", "tlsCryptV2ServerKey", ErrDependency)
	}
	mtu := inbound.Native.MTU
	if mtu == 0 {
		mtu = 1400
	}
	result = nativeruntime.OpenVPN{ID: inbound.TargetID, InboundTag: inbound.TargetTag, Port: uint16(inbound.Instance.Port), Listen: listen, Proto: settings.Proto, Subnet: subnet, MTU: mtu, DNS: dns, RedirectGateway: settings.RedirectGateway, ServerCert: certificate, ServerKey: privateKey, TLSCryptV2ServerKey: string(key.Data)}
	for _, client := range clients {
		if err := ValidateClientBinding(inbound.Instance.Kind, subnet, client.Native); err != nil {
			return result, issue(inbound, client.ClientID, "session addresses", err)
		}
		username, err := credentialText(client, core.CredUsername, true)
		if err != nil {
			return result, issue(inbound, client.ClientID, "username", err)
		}
		password, err := credentialText(client, core.CredPassword, true)
		if err != nil {
			return result, issue(inbound, client.ClientID, "password", err)
		}
		wrapped, err := credentialText(client, core.CredTLSCryptV2, true)
		if err != nil || len(wrapped) > 32768 {
			return result, issue(inbound, client.ClientID, "tlsCryptV2", ErrDependency)
		}
		block, rest := pem.Decode([]byte(wrapped))
		if block == nil || block.Type != "OpenVPN tls-crypt-v2 client key" || len(block.Bytes) == 0 || strings.TrimSpace(string(rest)) != "" {
			return result, issue(inbound, client.ClientID, "tlsCryptV2", ErrInvalid)
		}
		addresses := slices.Clone(client.Native.Addresses)
		sort.Slice(addresses, func(i, j int) bool { return addresses[i].Less(addresses[j]) })
		result.Clients = append(result.Clients, nativeruntime.OpenVPNClient{ClientID: client.ClientID, Username: username, Password: password, Addresses: addresses})
	}
	if err := result.Validate(); err != nil {
		return result, issue(inbound, "", "OpenVPN configuration", ErrInvalid)
	}
	return result, nil
}

func (r *renderer) nativeOpenConnect(inbound Inbound, clients []Client) (nativeruntime.OpenConnect, error) {
	var result nativeruntime.OpenConnect
	var shape struct {
		ocserv.Settings
		Clients []json.RawMessage `json:"clients"`
	}
	if strictSettings(inbound.Instance.Settings, &shape) != nil || allowedAssetFields(inbound, "serverCert", "serverKey") != nil {
		return result, issue(inbound, "", "settings", ErrInvalid)
	}
	settings, err := ocserv.ParseNodeSettings(inbound.Instance.Settings)
	if err != nil {
		return result, issue(inbound, "", "settings", ErrInvalid)
	}
	listen, err := nativeVPNListen(inbound)
	if err != nil {
		return result, err
	}
	subnet, err := nativeSubnet(inbound, settings.Subnet)
	if err != nil {
		return result, err
	}
	dns, err := nativeDNS(settings.DNS)
	if err != nil {
		return result, issue(inbound, "", "dns", err)
	}
	certificate, privateKey, err := r.nativeIdentity(inbound, settings.ServerCert, settings.ServerKey)
	if err != nil {
		return result, err
	}
	mtu := inbound.Native.MTU
	if mtu == 0 {
		mtu = 1400
	}
	result = nativeruntime.OpenConnect{ID: inbound.TargetID, InboundTag: inbound.TargetTag, Port: uint16(inbound.Instance.Port), Listen: listen, MTU: mtu, DNS: dns, RedirectGateway: settings.RedirectGateway, ServerCert: certificate, ServerKey: privateKey}
	if settings.Camouflage {
		result.CamouflageSecret = settings.CamouflageSecret
	}
	if err := result.ValidateServer(); err != nil {
		return result, issue(inbound, "", "OpenConnect configuration", ErrInvalid)
	}
	for _, client := range clients {
		pool := client.Native.Subnet
		if err := ValidateClientBinding(inbound.Instance.Kind, subnet, client.Native); err != nil {
			return result, issue(inbound, client.ClientID, "session pool", err)
		}
		username, err := credentialText(client, core.CredUsername, true)
		if err != nil {
			return result, issue(inbound, client.ClientID, "username", err)
		}
		password, err := credentialText(client, core.CredPassword, true)
		if err != nil {
			return result, issue(inbound, client.ClientID, "password", err)
		}
		result.Clients = append(result.Clients, nativeruntime.OpenConnectClient{ClientID: client.ClientID, Username: username, Password: password, Subnet: pool, MaxSessions: client.Native.MaxSessions})
	}
	if len(clients) != 0 {
		if err := result.Validate(); err != nil {
			return result, issue(inbound, "", "OpenConnect clients", ErrInvalid)
		}
	}
	return result, nil
}
