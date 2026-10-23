package sub

import (
	"strings"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
)

func (s *SubService) currentLinkClients(inbound *model.Inbound) ([]model.Client, error) {
	if inbound.Id > 0 && database.GetDB() != nil {
		clients, err := s.inboundService.ListClientsForInbound(inbound.Id)
		if err == nil && len(clients) > 0 {
			return s.inboundTunnelIdentities(inbound, clients)
		}
		if err != nil {
			logger.Debugf("subscription: refresh clients for inbound %d failed: %v", inbound.Id, err)
		}
	}
	return s.inboundService.GetClients(inbound)
}

// Shared lifecycle fields stay normalized; each tunnel keeps its own peer identity.
func (s *SubService) inboundTunnelIdentities(inbound *model.Inbound, clients []model.Client) ([]model.Client, error) {
	if !carriesWireguardClient(inbound.Protocol) {
		return clients, nil
	}
	embedded, err := s.inboundService.GetClients(inbound)
	if err != nil {
		return nil, err
	}
	peers := make(map[string]model.Client, len(embedded))
	for _, peer := range embedded {
		key := strings.ToLower(peer.Email)
		if _, found := peers[key]; key != "" && !found {
			peers[key] = peer
		}
	}
	out := make([]model.Client, 0, len(clients))
	for _, client := range clients {
		peer, found := peers[strings.ToLower(client.Email)]
		if !found {
			continue
		}
		client.PrivateKey = peer.PrivateKey
		client.PublicKey = peer.PublicKey
		client.PreSharedKey = peer.PreSharedKey
		client.AllowedIPs = append([]string(nil), peer.AllowedIPs...)
		client.KeepAlive = peer.KeepAlive
		out = append(out, client)
	}
	return out, nil
}
