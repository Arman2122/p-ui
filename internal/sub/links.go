package sub

import (
	"strings"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/service"
)

type LinkProvider struct {
	settingService service.SettingService
}

func NewLinkProvider() *LinkProvider {
	return &LinkProvider{}
}

func (p *LinkProvider) build(host string) *SubService {
	remarkTemplate, _ := p.settingService.GetRemarkTemplate()
	svc := NewSubService(remarkTemplate)
	svc.PrepareForRequest(host)
	return svc
}

func (p *LinkProvider) SubLinksForSubId(host, subId string) ([]string, error) {
	svc := p.build(host)
	links, _, _, _, err := svc.GetSubs(subId, host)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(links))
	for _, l := range links {
		out = append(out, splitLinkLines(l)...)
	}
	return out, nil
}

func (p *LinkProvider) LinksForClient(host string, inbound *model.Inbound, email string) []string {
	svc := p.build(host)
	svc.projectThroughFallbackMaster(inbound)
	return splitLinkLines(svc.GetLink(inbound, email))
}

func (p *LinkProvider) LinksForInbounds(host string, inbounds []*model.Inbound) []string {
	svc := p.build(host)
	var out []string
	for _, inbound := range inbounds {
		out = append(out, svc.inboundLinks(inbound)...)
	}
	return out
}

func splitLinkLines(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ConfigFileForSubId is the panel's door onto FileShare, which is the same call
// the subscription's own file endpoint makes — so the Clients page and a
// subscriber downloading the same client get identical bytes.
func (p *LinkProvider) ConfigFileForSubId(host, subId string, inboundId int) (string, string, error) {
	share, err := p.build(host).FileShare(subId, inboundId, host)
	if err != nil {
		return "", "", err
	}
	return share.Filename, share.Body, nil
}
