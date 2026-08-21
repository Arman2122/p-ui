package service

import (
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
)

type SubLinkProvider interface {
	SubLinksForSubId(host, subId string) ([]string, error)
	LinksForClient(host string, inbound *model.Inbound, email string) []string
	LinksForInbounds(host string, inbounds []*model.Inbound) []string
	// ConfigFileForSubId renders the file a client imports — the .conf for a
	// WireGuard kind — through the core that owns the kind, which is the only
	// renderer that knows what its parameters are.
	ConfigFileForSubId(host, subId string, inboundId int) (filename, body string, err error)
}

var registeredSubLinkProvider SubLinkProvider

func RegisterSubLinkProvider(p SubLinkProvider) {
	registeredSubLinkProvider = p
}

func (s *InboundService) GetSubLinks(host, subId string) ([]string, error) {
	if registeredSubLinkProvider == nil {
		return nil, common.NewError("sub link provider not registered")
	}
	return registeredSubLinkProvider.SubLinksForSubId(host, subId)
}

/*
GetClientConfigFile serves the Clients page the same bytes the subscription
serves.

The panel used to hand out a fourth hand-rolled .conf, built in the browser from
the handful of WireGuard fields InboundOption carries — which is why an
AmneziaWG client downloaded from the Clients page described a plain WireGuard
tunnel and could not complete a handshake against its own server. The renderer
that knows what an inbound's obfuscation is belongs to the core that owns the
kind, and everything else asks it.
*/
func (s *InboundService) GetClientConfigFile(host, subId string, inboundId int) (ClientConfigFile, error) {
	if registeredSubLinkProvider == nil {
		return ClientConfigFile{}, common.NewError("sub link provider not registered")
	}
	filename, body, err := registeredSubLinkProvider.ConfigFileForSubId(host, subId, inboundId)
	if err != nil {
		return ClientConfigFile{}, err
	}
	return ClientConfigFile{Filename: filename, Body: body}, nil
}

// ClientConfigFile is one importable client config: what to call the file, and
// what is in it.
type ClientConfigFile struct {
	Filename string `json:"filename" example:"peer-alice.conf"`
	Body     string `json:"body" example:"[Interface]\nPrivateKey = ..."`
}

func (s *InboundService) GetAllInboundLinks(host string, userId int) ([]string, error) {
	if registeredSubLinkProvider == nil {
		return nil, common.NewError("sub link provider not registered")
	}
	inbounds, err := s.GetInbounds(userId)
	if err != nil {
		return nil, err
	}
	return registeredSubLinkProvider.LinksForInbounds(host, inbounds), nil
}

func (s *InboundService) GetAllClientLinks(host string, email string) ([]string, error) {
	if email == "" {
		return nil, common.NewError("client email is required")
	}
	if registeredSubLinkProvider == nil {
		return nil, common.NewError("sub link provider not registered")
	}
	rec, err := s.clientService.GetRecordByEmail(nil, email)
	if err != nil {
		return nil, err
	}
	inboundIds, err := s.clientService.GetInboundIdsForRecord(rec.Id)
	if err != nil {
		return nil, err
	}
	var links []string
	for _, ibId := range inboundIds {
		inbound, getErr := s.GetInbound(ibId)
		if getErr != nil {
			return nil, getErr
		}
		links = append(links, registeredSubLinkProvider.LinksForClient(host, inbound, email)...)
	}
	return links, nil
}
