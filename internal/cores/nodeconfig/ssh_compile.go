package nodeconfig

import (
	"crypto/tls"
	"encoding/json"
	"net/netip"
	"slices"
	"sort"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores/internal/ssh"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func CompileSSH(input Input) (Result, error) {
	r, err := newRenderer(input)
	if err != nil {
		return Result{}, err
	}
	var config ssh.NodeConfiguration
	var identities []string
	identitySelected := false
	inbounds := slices.Clone(input.Inbounds)
	sort.Slice(inbounds, func(i, j int) bool { return inbounds[i].TargetID < inbounds[j].TargetID })
	for _, inbound := range inbounds {
		if inbound.Instance.Kind != ssh.Kind {
			return Result{}, issue(inbound, "", "kind", ErrUnsupported)
		}
		if !inbound.Instance.Enable {
			r.result.Omissions = append(r.result.Omissions, Omission{SourceID: inbound.Instance.ID, Reason: "inbound-disabled"})
			continue
		}
		if !identitySelected {
			config.HostKeyPEM, err = r.sshHostKeys()
			if err != nil {
				return Result{}, err
			}
			identitySelected = true
		}
		if inbound.Instance.Port < 1024 || inbound.Instance.Port > 65535 || !unusedObject(inbound.Instance.StreamSettings) || !unusedObject(inbound.Instance.Sniffing) || inbound.Native.Subnet.IsValid() || len(inbound.Native.Addresses) != 0 || inbound.Native.MTU != 0 {
			return Result{}, issue(inbound, "", "listener/network", ErrUnsupported)
		}
		listen, err := literalListen(inbound.Instance.Listen)
		if err != nil {
			return Result{}, issue(inbound, "", "listen", err)
		}
		beforeAssets, beforeBridges := len(r.result.Assets), len(r.result.Bridges)
		settings, err := r.sshSettings(inbound)
		if err != nil {
			return Result{}, err
		}
		clients, err := r.activeClients(inbound)
		if err != nil {
			return Result{}, err
		}
		instance := inbound.Instance
		instance.ID, instance.Listen = int(inbound.TargetID), listen.String()
		instance.StreamSettings, instance.Sniffing = "", ""
		if inbound.TargetTag != "" {
			instance.Tag = inbound.TargetTag
		}
		encoded, err := json.Marshal(settings)
		if err != nil {
			return Result{}, issue(inbound, "", "settings", ErrInvalid)
		}
		instance.Settings = string(encoded)
		for _, client := range clients {
			if len(client.Native.Addresses) != 0 || client.Native.Subnet.IsValid() || client.Native.MaxSessions != 0 {
				return Result{}, issue(inbound, client.ClientID, "native binding", ErrInvalid)
			}
			username, err := credentialText(client, core.CredUsername, true)
			if err != nil {
				return Result{}, issue(inbound, client.ClientID, "username", err)
			}
			password, err := credentialText(client, core.CredPassword, true)
			if err != nil {
				return Result{}, issue(inbound, client.ClientID, "password", err)
			}
			instance.Users = append(instance.Users, core.User{Email: client.ClientID, Enable: true, Credentials: map[string]any{core.CredUsername: username, core.CredPassword: password}})
		}
		if ssh.ValidateNodeInbounds([]core.Instance{instance}) != nil {
			return Result{}, issue(inbound, "", "SSH configuration", ErrInvalid)
		}
		if len(clients) == 0 {
			r.result.Assets, r.result.Bridges = r.result.Assets[:beforeAssets], r.result.Bridges[:beforeBridges]
			r.result.Omissions = append(r.result.Omissions, Omission{SourceID: inbound.Instance.ID, Reason: "no-enabled-clients"})
			continue
		}
		for _, client := range clients {
			identities = append(identities, client.ClientID)
		}
		config.Inbounds = append(config.Inbounds, instance)
		r.result.Listeners = append(r.result.Listeners, ListenerClaim{RuntimeID: "ssh", SourceID: inbound.Instance.ID, TargetID: inbound.TargetID, Protocol: "tcp", Address: listen, Port: uint16(inbound.Instance.Port), DualStack: listen.IsUnspecified(), Role: "client"})
	}
	if len(config.Inbounds) == 0 {
		r.result.Assets = nil
		return finishResult(r.result)
	}
	data, err := json.Marshal(config)
	if err != nil || ssh.ValidateNodeInbounds(config.Inbounds) != nil {
		return Result{}, ErrInvalid
	}
	r.result.Runtimes = []RuntimeDefinition{{ID: "ssh", Core: "ssh", Configuration: data, ClientIDs: identities, FileAssets: r.fileAssets("ssh")}}
	return finishResult(r.result)
}

func (r *renderer) sshHostKeys() (string, error) {
	binding := r.input.SSHHostKeys
	if binding.GenerateLocally {
		if binding.AssetID != "" {
			return "", ErrInvalid
		}
		r.result.Assets = append(r.result.Assets, AssetClaim{RuntimeID: "ssh", Field: "hostKeyPEM", Kind: AssetSSHHostKeys, Delivery: AssetGenerated})
		return "", nil
	}
	asset, err := r.asset(Inbound{Assets: map[string]string{"hostKeyPEM": binding.AssetID}}, "ssh", "hostKeyPEM", AssetSSHHostKeys, AssetEmbedded)
	if err != nil || len(asset.Data) > 32<<10 || ssh.ValidateNodeHostKeys(string(asset.Data)) != nil {
		return "", ErrDependency
	}
	return string(asset.Data), nil
}

func (r *renderer) sshSettings(inbound Inbound) (ssh.Settings, error) {
	var shape struct {
		ssh.Settings
		Clients []json.RawMessage `json:"clients"`
	}
	if strictSettings(inbound.Instance.Settings, &shape) != nil || allowedAssetFields(inbound, "certFile", "keyFile") != nil {
		return ssh.Settings{}, issue(inbound, "", "settings", ErrInvalid)
	}
	settings := shape.Settings
	if settings.Transport == ssh.TransportWSS {
		certificate, err := r.asset(inbound, "ssh", "certFile", AssetCertificate, AssetFile)
		if err != nil {
			return settings, err
		}
		key, err := r.asset(inbound, "ssh", "keyFile", AssetPrivateKey, AssetFile)
		if err != nil {
			return settings, err
		}
		if len(certificate.Data)+len(key.Data) > 32768 {
			return settings, issue(inbound, "", "TLS assets", ErrInvalid)
		}
		if _, err := tls.X509KeyPair(certificate.Data, key.Data); err != nil {
			return settings, issue(inbound, "", "TLS assets", ErrInvalid)
		}
		settings.CertFile = configuration.AssetReference(certificate.ID, fileAsset(certificate).SHA256)
		settings.KeyFile = configuration.AssetReference(key.ID, fileAsset(key).SHA256)
	} else {
		if len(inbound.Assets) != 0 {
			return settings, issue(inbound, "", "TLS assets", ErrInvalid)
		}
		settings.CertFile, settings.KeyFile = "", ""
	}
	switch inbound.Egress.Mode {
	case "", EgressDirect:
		if settings.RouteThroughXray || inbound.Egress.BridgeID != "" {
			return settings, issue(inbound, "", "egress", ErrDependency)
		}
		settings.RouteXrayPort = 0
	case EgressXray:
		bridge, ok := r.bridges[inbound.Egress.BridgeID]
		if !ok || bridge.Core != "xray" || bridge.Endpoint.Addr() != netip.MustParseAddr("127.0.0.1") || settings.UDPGW && !bridge.UDP {
			return settings, issue(inbound, "", "egress bridge", ErrDependency)
		}
		settings.RouteThroughXray, settings.RouteXrayPort = true, int(bridge.Endpoint.Port())
		r.result.Bridges = append(r.result.Bridges, BridgeClaim{ID: bridge.ID, RuntimeID: bridge.RuntimeID, ConsumerRuntimeID: "ssh", SourceID: inbound.Instance.ID, TargetID: inbound.TargetID, Endpoint: bridge.Endpoint, UDP: settings.UDPGW})
	default:
		return settings, issue(inbound, "", "egress", ErrUnsupported)
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return settings, issue(inbound, "", "settings", ErrInvalid)
	}
	parsed, err := ssh.ParseNodeSettings(string(data))
	if err != nil {
		return settings, issue(inbound, "", "settings", ErrInvalid)
	}
	return parsed, nil
}
