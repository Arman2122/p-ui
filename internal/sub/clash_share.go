package sub

import (
	"net/url"
	"strings"

	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// buildProxyFromShare asks the registry for the core's own share link and
// renders mihomo's entry from it; rendered is false for a kind no core links.
func (s *SubClashService) buildProxyFromShare(subReq *SubService, inbound *model.Inbound, client model.Client, ep map[string]any) (map[string]any, bool) {
	share, renders, err := cores.ClientShare(instanceForShare(inbound), userForShare(client), inbound.Listen)
	if !renders {
		return nil, false
	}
	if err != nil || share.Kind != "link" {
		return nil, true
	}
	proxy := clashProxyFromShare(share.URI, subReq.endpointRemark(inbound, client.Email, ep, ""))
	if proxy == nil {
		return nil, true
	}
	if sni, ok := externalProxySNI(ep); ok && sni != "" {
		proxy["sni"] = sni
	}
	if insecure, ok := ep["allowInsecure"].(bool); ok && insecure {
		proxy["skip-cert-verify"] = true
	}
	return proxy, true
}

// clashProxyFromShare is the mihomo entry for a share link's scheme; a scheme
// mihomo has no proxy type for renders nothing.
func clashProxyFromShare(uri, name string) map[string]any {
	u, err := url.Parse(uri)
	if err != nil || u.User == nil {
		return nil
	}
	host, port := splitClashHostPort(u.Host)
	q := u.Query()
	proxy := map[string]any{"name": name, "server": host, "port": port, "udp": true}
	if sni := q.Get("sni"); sni != "" {
		proxy["sni"] = sni
	}
	switch u.Scheme {
	case "tuic":
		password, _ := u.User.Password()
		proxy["type"] = "tuic"
		proxy["uuid"] = u.User.Username()
		proxy["password"] = password
		if v := q.Get("congestion_control"); v != "" {
			proxy["congestion-controller"] = v
		}
		if v := q.Get("udp_relay_mode"); v != "" {
			proxy["udp-relay-mode"] = v
		}
		if v := q.Get("alpn"); v != "" {
			proxy["alpn"] = strings.Split(v, ",")
		}
	case "anytls":
		proxy["type"] = "anytls"
		proxy["password"] = u.User.Username()
		proxy["client-fingerprint"] = "chrome"
	default:
		return nil
	}
	return proxy
}
