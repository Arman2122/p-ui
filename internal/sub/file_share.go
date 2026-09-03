package sub

import (
	"errors"
	"strconv"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
File-shaped delivery. A WireGuard client is configured by a .conf and an OpenVPN
client by an .ovpn; neither fits the raw subscription, whose lines are URIs. The
subscription token already authenticates the client for links, so it authorises
the file too: same secret, same audience, one more representation.
*/

// ErrNoFileShare is a subscription/inbound pair with nothing file-shaped to
// hand out — an unknown pair, or a kind that is delivered by URI alone.
var ErrNoFileShare = errors.New("sub: nothing file-shaped to serve for this subscription")

// FileShare renders the config file for one inbound of one subscription.
func (s *SubService) FileShare(subId string, inboundId int, host string) (core.Share, error) {
	inbounds, err := s.getInboundsBySubId(subId)
	if err != nil {
		return core.Share{}, err
	}
	for _, inbound := range inbounds {
		if inbound.Id != inboundId {
			continue
		}
		for _, match := range s.matchingClients(inbound, subId) {
			if !match.Enable {
				continue
			}
			// The match names the client; the credentials are hydrated the same
			// way the link path hydrates them, or the render has no key to use.
			client, ok := s.clientForLink(inbound, match.Email)
			if !ok {
				continue
			}
			// A host is the way in the operator chose for this inbound; the file
			// points there like every link does, not at the inbound's own address.
			inst, address := instanceForShare(inbound), s.resolveInboundAddress(inbound)
			if eps := s.hostEndpoints(inbound, "file"); len(eps) > 0 {
				if dest, _ := eps[0]["dest"].(string); dest != "" {
					address = dest
				}
				if port, _ := eps[0]["port"].(float64); port > 0 {
					inst.Port = int(port)
				}
			}
			share, renders, err := cores.ClientShare(inst, userForShare(client), address)
			if err != nil {
				return core.Share{}, err
			}
			if !renders || share.Kind != "file" {
				return core.Share{}, ErrNoFileShare
			}
			return share, nil
		}
	}
	return core.Share{}, ErrNoFileShare
}

// instanceForShare projects the row into the contract's shape. Only what
// RenderClient reads: the tag for the comment line, the port for the endpoint,
// and the settings the server half is derived from.
func instanceForShare(inbound *model.Inbound) core.Instance {
	return core.Instance{
		ID:       inbound.Id,
		Kind:     core.Kind(inbound.Protocol),
		Tag:      inbound.Tag,
		Port:     inbound.Port,
		Settings: inbound.Settings,
	}
}

// userForShare carries the client's credentials under their vocabulary names,
// plus keepAlive, which rides beside them in the settings JSON.
func userForShare(client model.Client) core.User {
	credentials := map[string]any{}
	for name, value := range map[string]string{
		core.CredUUID: client.ID, core.CredPassword: client.Password, core.CredUsername: client.Username,
		core.CredAuth: client.Auth, core.CredSecurity: client.Security, core.CredSecret: client.Secret, core.CredAdTag: client.AdTag,
	} {
		if value != "" {
			credentials[name] = value
		}
	}
	if client.PrivateKey != "" {
		credentials[core.CredPrivateKey] = client.PrivateKey
	}
	if client.PublicKey != "" {
		credentials[core.CredPublicKey] = client.PublicKey
	}
	if psk := client.PSK(); psk != "" {
		credentials[core.CredPreSharedKey] = psk
	}
	if len(client.AllowedIPs) > 0 {
		list := make([]any, 0, len(client.AllowedIPs))
		for _, prefix := range client.AllowedIPs {
			list = append(list, prefix)
		}
		credentials[core.CredAllowedIPs] = list
	}
	if client.KeepAlive > 0 {
		credentials["keepAlive"] = float64(client.KeepAlive)
	}
	return core.User{Email: client.Email, Enable: client.Enable, Credentials: credentials}
}

// fileShareInboundId parses the route's inbound id; 0 is never a valid row id.
func fileShareInboundId(raw string) int {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}
