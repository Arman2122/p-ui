package sub

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-json"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/util/common"
	"github.com/Arman2122/p-ui/internal/util/link"
	"github.com/Arman2122/p-ui/internal/util/sharelink"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/xray"
)

// SubService provides business logic for generating subscription links and managing subscription data.
type SubService struct {
	address        string
	remarkTemplate string
	datepicker     string
	// subscriptionBody is true only when rendering the actual subscription
	// content a client app imports (raw /sub fetch, /json, /clash). The remark
	// template's per-client info is emitted there (on the first link); every
	// other context — the sub info page, the panel's link/QR displays — renders
	// the name-only template, like Remnawave.
	subscriptionBody bool
	// usageShown tracks, per client email, whether the info part of the template
	// has already been emitted this request, so it appears on the first body
	// link only. Per-request state; reset in PrepareForRequest.
	usageShown             map[string]bool
	showIdentityOnAllLinks bool
	inboundService         service.InboundService
	settingService         service.SettingService
	// nodesByID is populated per request from the Node table so
	// resolveInboundAddress can return the node's address for any
	// inbound whose NodeID is set. Keeps the per-link host derivation
	// O(1) instead of O(N) DB hits.
	nodesByID map[int]*model.Node
	// statsByEmail maps a client email to its traffic row across ALL inbounds
	// loaded for the request. client_traffics.email is globally unique, so this
	// lets statsForClient resolve usage for a client even on an inbound that
	// doesn't own its row (multi-inbound subscriptions). Filled in
	// getInboundsBySubId; reset per request in PrepareForRequest.
	statsByEmail map[string]xray.ClientTraffic
	// clientsByInbound caches clients resolved for this request keyed by
	// inbound id then email, so the per-protocol link generators look a client
	// up without re-parsing the inbound's settings JSON per link.
	// fullyPrimedInbounds marks inbounds whose complete client list is cached
	// (a miss there is authoritative). Reset per request in PrepareForRequest.
	clientsByInbound    map[int]map[string]model.Client
	fullyPrimedInbounds map[int]bool
	// settingsByInbound caches each inbound's settings decoded once per request
	// with the clients array left out; generators read only inbound-level
	// fields (encryption, method, version, …) from it.
	settingsByInbound map[int]map[string]any
}

// NewSubService creates a new subscription service with the given configuration.
func NewSubService(remarkTemplate string) *SubService {
	return &SubService{
		remarkTemplate: remarkTemplate,
	}
}

// ForRequest returns a shallow copy with request-scoped state populated.
// Subscription controllers share one base SubService, so request-specific
// fields such as address and nodesByID must live on a per-request copy.
func (s *SubService) ForRequest(host string) *SubService {
	req := *s
	req.PrepareForRequest(host)
	return &req
}

// PrepareForRequest sets per-request state (host + nodes map) on this
// SubService instance. HTTP handlers should call ForRequest instead so the
// controller's shared base service is never mutated by concurrent requests.
func (s *SubService) PrepareForRequest(host string) {
	if !isRoutableHost(host) {
		if d := s.configuredPublicHost(); d != "" {
			host = d
		} else if isLoopbackHost(host) {
			host = "localhost"
		}
	}
	s.address = host
	s.usageShown = map[string]bool{}
	s.statsByEmail = map[string]xray.ClientTraffic{}
	s.clientsByInbound = map[int]map[string]model.Client{}
	s.fullyPrimedInbounds = map[int]bool{}
	s.settingsByInbound = map[int]map[string]any{}
	s.loadNodes()
	s.loadRemarkSettings()
}

// primeLinkClients caches clients (first occurrence per email, matching the
// old settings-JSON iteration order) so clientForLink resolves them without a
// parse. complete marks the inbound's whole client list as cached.
func (s *SubService) primeLinkClients(inboundId int, clients []model.Client, complete bool) {
	if inboundId <= 0 {
		return
	}
	if s.clientsByInbound == nil {
		s.clientsByInbound = map[int]map[string]model.Client{}
	}
	m := s.clientsByInbound[inboundId]
	if m == nil {
		m = make(map[string]model.Client, len(clients))
		s.clientsByInbound[inboundId] = m
	}
	for _, c := range clients {
		if _, exists := m[c.Email]; !exists {
			m[c.Email] = c
		}
	}
	if complete {
		if s.fullyPrimedInbounds == nil {
			s.fullyPrimedInbounds = map[int]bool{}
		}
		s.fullyPrimedInbounds[inboundId] = true
	}
}

// Resolve current credentials once per request, keeping inbound-specific peer identities.
func (s *SubService) clientForLink(inbound *model.Inbound, email string) (model.Client, bool) {
	if m, ok := s.clientsByInbound[inbound.Id]; ok {
		if c, hit := m[email]; hit {
			return c, true
		}
		if s.fullyPrimedInbounds[inbound.Id] {
			return model.Client{}, false
		}
	}
	clients, err := s.currentLinkClients(inbound)
	if err != nil {
		return model.Client{}, false
	}
	s.primeLinkClients(inbound.Id, clients, true)
	for i := range clients {
		if clients[i].Email == email {
			return clients[i], true
		}
	}
	return model.Client{}, false
}

// linkSettings returns the inbound's settings decoded once per request with
// the clients array left out — the link generators read only inbound-level
// fields from it and resolve clients via clientForLink. The shallow
// RawMessage pass skips materializing a huge clients array entirely.
func (s *SubService) linkSettings(inbound *model.Inbound) map[string]any {
	if inbound.Id > 0 {
		if cached, ok := s.settingsByInbound[inbound.Id]; ok {
			return cached
		}
	}
	shallow := map[string]json.RawMessage{}
	_ = json.Unmarshal([]byte(inbound.Settings), &shallow)
	out := make(map[string]any, len(shallow))
	for key, raw := range shallow {
		if key == "clients" {
			continue
		}
		var value any
		_ = json.Unmarshal(raw, &value)
		out[key] = value
	}
	if inbound.Id > 0 {
		if s.settingsByInbound == nil {
			s.settingsByInbound = map[int]map[string]any{}
		}
		s.settingsByInbound[inbound.Id] = out
	}
	return out
}

// loadRemarkSettings populates the per-request remark formatting state so
// every subscription format — raw, JSON, Clash — renders remarks the same way
// (the date formatter reads datepicker). Loading it only in getSubs left
// JSON/Clash with the zero value.
func (s *SubService) loadRemarkSettings() {
	var err error
	s.datepicker, err = s.settingService.GetDatepicker()
	if err != nil {
		s.datepicker = "gregorian"
	}
	s.showIdentityOnAllLinks, err = s.settingService.GetSubShowIdentityOnAllLinks()
	if err != nil {
		s.showIdentityOnAllLinks = false
	}
}

func (s *SubService) configuredPublicHost() string {
	if d, err := s.settingService.GetSubDomain(); err == nil && d != "" {
		return d
	}
	if d, err := s.settingService.GetWebDomain(); err == nil && d != "" {
		return d
	}
	return ""
}

func isRoutableHost(host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return !ip.IsLoopback() && !ip.IsUnspecified()
	}
	return true
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// listenIsInternalOnly reports whether a bind address is reachable only from
// the same host — a loopback IP or a unix-domain socket. Such an inbound can't
// be dialed directly by a remote client, so when it is the child side of a
// fallback its share link must be projected through the master. A public or
// wildcard listen (""/0.0.0.0/::) is reachable on its own port and advertises
// itself.
func listenIsInternalOnly(listen string) bool {
	if listen == "" {
		return false
	}
	if listen[0] == '@' || listen[0] == '/' {
		return true
	}
	return isLoopbackHost(listen)
}

// matchingClients returns the inbound's clients whose SubID equals subId,
// resolved from the normalized clients/client_inbounds tables (both filter
// columns indexed) instead of parsing the settings JSON — at large client
// counts that parse made every subscription fetch cost seconds. The
// case-insensitive email dedupe stays as cheap insurance even though
// clients.email is unique, preserving the #5134 guarantee that duplicate
// settings entries never fan out into duplicate profiles. Resolved clients
// are primed into the per-request cache so the link generators don't parse
// settings either.
func (s *SubService) matchingClients(inbound *model.Inbound, subId string) []model.Client {
	clients, err := s.inboundService.GetClientsBySubId(inbound.Id, subId)
	if err != nil {
		logger.Error("SubService - GetClientsBySubId: Unable to get clients from inbound")
		return nil
	}
	var out []model.Client
	seen := make(map[string]struct{}, len(clients))
	for _, client := range clients {
		key := strings.ToLower(client.Email)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, client)
	}
	out, err = s.inboundTunnelIdentities(inbound, out)
	if err != nil {
		logger.Warningf("subscription: inbound %d peer identities: %v", inbound.Id, err)
		return nil
	}
	s.primeLinkClients(inbound.Id, out, false)
	return out
}

// GetSubs retrieves subscription links for a given subscription ID and host.
func (s *SubService) GetSubs(subId string, host string) ([]string, []string, int64, xray.ClientTraffic, error) {
	result, emails, _, lastOnline, traffic, err := s.ForRequest(host).getSubs(subId)
	return result, emails, lastOnline, traffic, err
}

// fileShapedInboundId is the inbound id when its core renders a config file
// (the sub server serves it under /file/<id>), and 0 for a link-only kind.
func fileShapedInboundId(inbound *model.Inbound) int {
	if cores.SharesFile(core.Kind(inbound.Protocol)) {
		return inbound.Id
	}
	return 0
}

func (s *SubService) getSubs(subId string) ([]string, []string, []int, int64, xray.ClientTraffic, error) {
	var result []string
	var emails []string
	var fileIds []int
	var traffic xray.ClientTraffic
	var hasEnabledClient bool
	inbounds, err := s.getInboundsBySubId(subId)
	if err != nil {
		return nil, nil, nil, 0, traffic, err
	}
	externalLinks, err := s.getClientExternalLinksBySubId(subId)
	if err != nil {
		return nil, nil, nil, 0, traffic, err
	}

	if len(inbounds) == 0 && len(externalLinks) == 0 {
		return nil, nil, nil, 0, traffic, nil
	}

	seenEmails := make(map[string]struct{})
	for _, inbound := range inbounds {
		clients := s.matchingClients(inbound, subId)
		if len(clients) == 0 {
			continue
		}
		s.projectThroughFallbackMaster(inbound)
		// Host overrides apply AFTER fallback projection so a host's
		// address/TLS wins over the projected master stream.
		hostEps := s.hostEndpoints(inbound, "raw")
		for _, client := range clients {
			if client.Enable {
				hasEnabledClient = true
			}
			var link string
			if hostEps != nil {
				link = s.linkFromHosts(inbound, client, hostEps)
			} else {
				link = s.GetLink(inbound, client.Email)
			}
			// A kind with no shareable link renders "" -- http/mixed/tunnel, and a
			// WireGuard client holding no private key. seenEmails stays
			// unconditional so the traffic header still counts them. F311.
			if link != "" {
				result = append(result, link)
				emails = append(emails, client.Email)
				fileIds = append(fileIds, fileShapedInboundId(inbound))
			}
			seenEmails[client.Email] = struct{}{}
		}
	}
	for _, ext := range externalLinks {
		if ext.Enable {
			hasEnabledClient = true
		}
		seenEmails[ext.Email] = struct{}{}
		if !ext.Active {
			if result == nil {
				result = []string{}
			}
			continue
		}
		for _, el := range expandEntry(ext) {
			if link := applyRemarkToLink(el.Link, el.Name); link != "" {
				result = append(result, link)
				emails = append(emails, ext.Email)
				fileIds = append(fileIds, 0)
				seenEmails[ext.Email] = struct{}{}
			}
		}
	}

	uniqueEmails := make([]string, 0, len(seenEmails))
	for e := range seenEmails {
		uniqueEmails = append(uniqueEmails, e)
	}
	traffic, lastOnline := s.AggregateTrafficByEmails(uniqueEmails)
	traffic.Enable = hasEnabledClient
	return result, emails, fileIds, lastOnline, traffic, nil
}

// inboundLinks builds the share links for every distinct client of one inbound
// the same way getSubs does — managed Host endpoints win over the plain link so
// {{HOST}} and per-host variants render — but across all clients rather than a
// single subId. Dedups duplicate client JSON entries by email (#5134). Backs the
// panel's "Export all inbound links" so it matches the client/QR pages.
/*
linkForEmail renders one client's raw link, through the inbound's hosts when it
has any.

The panel's per-client link endpoint used to call GetLink directly, so it handed
out the origin address on exactly the deployments hosts exist for. Every step
below mirrors inboundLinks, which is the renderer a subscriber gets; the two
must not disagree about the same client. Anything it cannot resolve falls back
to the pre-host behaviour rather than to no link at all.
*/
func (s *SubService) linkForEmail(inbound *model.Inbound, email string) string {
	clients, err := s.currentLinkClients(inbound)
	if err == nil {
		s.primeLinkClients(inbound.Id, clients, true)
	}
	s.projectThroughFallbackMaster(inbound)
	if hostEps := s.hostEndpoints(inbound, "raw"); len(hostEps) > 0 {
		for _, client := range clients {
			if strings.EqualFold(client.Email, email) {
				return s.linkFromHosts(inbound, client, hostEps)
			}
		}
	}
	return s.GetLink(inbound, email)
}

func (s *SubService) inboundLinks(inbound *model.Inbound) []string {
	clients, err := s.currentLinkClients(inbound)
	if err != nil {
		return nil
	}
	s.primeLinkClients(inbound.Id, clients, true)
	s.projectThroughFallbackMaster(inbound)
	hostEps := s.hostEndpoints(inbound, "raw")
	var out []string
	seen := make(map[string]struct{}, len(clients))
	for _, client := range clients {
		key := strings.ToLower(client.Email)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		var link string
		if len(hostEps) > 0 {
			link = s.linkFromHosts(inbound, client, hostEps)
		} else {
			link = s.GetLink(inbound, client.Email)
		}
		out = append(out, splitLinkLines(link)...)
	}
	return out
}

// AggregateTrafficByEmails resolves traffic for every email in one
// query and folds the rows into a single ClientTraffic + lastOnline.
// xray.ClientTraffic.Email is globally unique, so a multi-inbound
// client's single row is attached to exactly one inbound — iterating
// per-inbound ClientStats would miss it on the others. Used by GetSubs,
// SubClashService.GetClash, and SubJsonService.GetJson to keep the
// sub-info header consistent across all three formats.
func (s *SubService) AggregateTrafficByEmails(emails []string) (xray.ClientTraffic, int64) {
	var agg xray.ClientTraffic
	var lastOnline int64
	if len(emails) == 0 {
		return agg, 0
	}
	db := database.GetDB()
	var rows []xray.ClientTraffic
	if err := db.
		Model(&xray.ClientTraffic{}).
		Where("email IN ?", emails).
		Find(&rows).Error; err != nil {
		logger.Warning("SubService - AggregateTrafficByEmails: load by email:", err)
		return agg, 0
	}

	// total/expiry are configured limits owned by the clients table, not the
	// runtime traffic rows. In a multi-node setup the node snapshot can reset
	// client_traffics.total/expiry_time to 0, so fall back to the clients
	// table to keep the Subscription-Userinfo header in sync with the UI (#4645).
	limits := make(map[string][2]int64, len(emails))
	var records []model.ClientRecord
	if err := db.Model(&model.ClientRecord{}).Where("email IN ?", emails).Find(&records).Error; err != nil {
		logger.Warning("SubService - AggregateTrafficByEmails: load client limits:", err)
	} else {
		for _, r := range records {
			limits[r.Email] = [2]int64{r.TotalGB, r.ExpiryTime}
		}
	}

	now := time.Now().UnixMilli()
	first := true
	for _, ct := range rows {
		if ct.LastOnline > lastOnline {
			lastOnline = ct.LastOnline
		}
		total, expiry := ct.Total, ct.ExpiryTime
		if lim, ok := limits[ct.Email]; ok {
			if total == 0 {
				total = lim[0]
			}
			if expiry == 0 {
				expiry = lim[1]
			}
		}
		if first {
			agg.Up = ct.Up
			agg.Down = ct.Down
			agg.Total = total
			agg.ExpiryTime = subscriptionExpiryFromClient(now, expiry)
			first = false
			continue
		}
		agg.Up += ct.Up
		agg.Down += ct.Down
		if agg.Total == 0 || total == 0 {
			agg.Total = 0
		} else {
			agg.Total += total
		}
		normalized := subscriptionExpiryFromClient(now, expiry)
		// Zero is "never expires" in Subscription-Userinfo, so differing expiries
		// must fold to the nearest one, not to nothing. F131.
		if agg.ExpiryTime != 0 && (normalized == 0 || normalized < agg.ExpiryTime) {
			agg.ExpiryTime = normalized
		}
	}
	return agg, lastOnline
}

func subscriptionExpiryFromClient(nowMs, expiryTime int64) int64 {
	if expiryTime > 0 {
		return expiryTime
	}
	if expiryTime < 0 {
		return nowMs + (-expiryTime)
	}
	return 0
}

func (s *SubService) getInboundsBySubId(subId string) ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	// The protocol set comes from the registry: a literal here silently drops a
	// core added later, and the symptom is an empty subscription page for an
	// inbound that exists, has clients and is serving them.
	err := db.Model(model.Inbound{}).Where(`id in (
		SELECT DISTINCT inbounds.id
		FROM inbounds
		JOIN client_inbounds ON client_inbounds.inbound_id = inbounds.id
		JOIN clients ON clients.id = client_inbounds.client_id
		WHERE
			inbounds.protocol in ?
			AND clients.sub_id = ? AND inbounds.enable = ?
	)`, subscribableProtocols(), subId, true).Order("sub_sort_index ASC").Order("id ASC").Find(&inbounds).Error
	if err != nil {
		return nil, err
	}
	s.indexStatsBySubId(subId)
	return inbounds, nil
}

// indexStatsBySubId loads the traffic rows for just this subscriber's clients
// into statsByEmail so statsForClient can resolve a client's usage on any of
// its inbounds. It replaces preloading every matched inbound's ClientStats,
// which read the entire client_traffics table on every subscription fetch of
// a large inbound; statsForClient's per-email DB fallback covers any miss.
func (s *SubService) indexStatsBySubId(subId string) {
	if s.statsByEmail == nil {
		s.statsByEmail = map[string]xray.ClientTraffic{}
	}
	db := database.GetDB()
	var emails []string
	if err := db.Model(&model.ClientRecord{}).Where("sub_id = ?", subId).Pluck("email", &emails).Error; err != nil {
		logger.Error("SubService - indexStatsBySubId: load emails:", err)
		return
	}
	const chunk = 400
	for lo := 0; lo < len(emails); lo += chunk {
		hi := min(lo+chunk, len(emails))
		var rows []xray.ClientTraffic
		if err := db.Where("email IN ?", emails[lo:hi]).Find(&rows).Error; err != nil {
			logger.Error("SubService - indexStatsBySubId: load traffics:", err)
			return
		}
		for _, st := range rows {
			s.statsByEmail[st.Email] = st
		}
	}
}

// projectThroughFallbackMaster mutates the inbound in place so its
// Listen/Port/StreamSettings reflect the externally reachable master
// when applicable. Covers both fallback mechanisms:
//   - panel-tracked: an inbound_fallbacks row where child_id = inbound.Id
//   - legacy unix-socket: inbound.Listen begins with "@" and some VLESS/
//     Trojan inbound's settings.fallbacks references that listen address
//
// Returns true when a projection happened; sub services call this before
// generating links so a child VLESS-WS bound to 127.0.0.1 emits the
// master's :443 + TLS state instead of its own loopback endpoint.
//
// Projection only applies to a child that is not directly reachable on its
// own listen (loopback or a unix-domain socket). An inbound on a public or
// wildcard listen is reachable on its own port, so it advertises its own
// port + security even when a stale fallback rule still names it as a child —
// otherwise its share link would leak the master's port and Reality/TLS
// settings (#4987).
func (s *SubService) projectThroughFallbackMaster(inbound *model.Inbound) bool {
	if inbound == nil {
		return false
	}
	if !listenIsInternalOnly(inbound.Listen) || webRelayFronted(inbound.Settings) {
		return false
	}
	db := database.GetDB()
	var master *model.Inbound

	var rule model.InboundFallback
	if err := db.Where("child_id = ?", inbound.Id).
		Order("sort_order ASC, id ASC").
		First(&rule).Error; err == nil {
		var m model.Inbound
		if err := db.Where("id = ?", rule.MasterId).First(&m).Error; err == nil {
			master = &m
		}
	}

	if master == nil && len(inbound.Listen) > 0 && inbound.Listen[0] == '@' {
		// jsonb, not SQLite's JSON_TYPE/json_each/json_extract: this is a
		// PostgreSQL-only codebase and the SQLite form errored on every call,
		// silently, because the error was compared to nil and dropped. F127.
		var m model.Inbound
		err := db.Model(model.Inbound{}).
			Where("jsonb_typeof(settings::jsonb -> 'fallbacks') = 'array'").
			Where("EXISTS (SELECT 1 FROM jsonb_array_elements(settings::jsonb -> 'fallbacks') AS f(value) WHERE f.value ->> 'dest' = ?)", inbound.Listen).
			First(&m).Error
		switch {
		case err == nil:
			master = &m
		case !errors.Is(err, gorm.ErrRecordNotFound):
			// Not finding a master is the ordinary case; anything else is the
			// class of failure that hid this bug for as long as it existed.
			logger.Warning("SubService - fallback master lookup for", inbound.Listen, ":", err)
		}
	}

	if master == nil {
		return false
	}
	inbound.StreamSettings = mergeStreamFromMaster(inbound.StreamSettings, master.StreamSettings)
	inbound.Listen = master.Listen
	inbound.Port = master.Port
	return true
}

// mergeStreamFromMaster copies the master's security + tlsSettings +
// realitySettings + externalProxy onto the child's stream so the child's
// link advertises the master's TLS / Reality state. Transport (network
// + ws/grpc/etc. settings) stays the child's.
func mergeStreamFromMaster(childStream, masterStream string) string {
	var stream map[string]any
	_ = json.Unmarshal([]byte(childStream), &stream)
	if stream == nil {
		stream = map[string]any{}
	}
	var mst map[string]any
	_ = json.Unmarshal([]byte(masterStream), &mst)
	if mst == nil {
		return childStream
	}
	stream["security"] = mst["security"]
	if v, ok := mst["tlsSettings"]; ok {
		stream["tlsSettings"] = v
	} else {
		delete(stream, "tlsSettings")
	}
	if v, ok := mst["realitySettings"]; ok {
		stream["realitySettings"] = v
	} else {
		delete(stream, "realitySettings")
	}
	if v, ok := mst["externalProxy"]; ok {
		stream["externalProxy"] = v
	}
	out, err := json.MarshalIndent(stream, "", "  ")
	if err != nil {
		return childStream
	}
	return string(out)
}

// GetLink dispatches to the protocol-specific generator for one (inbound, client)
// pair. Returns "" when the inbound's protocol doesn't produce a subscription URL
// (socks, http, mixed, wireguard, dokodemo, tunnel). The returned string may
// contain multiple `\n`-separated URLs when the inbound has externalProxy set.
func (s *SubService) GetLink(inbound *model.Inbound, email string) string {
	switch inbound.Protocol {
	case "vmess":
		return s.genVmessLink(inbound, email)
	case "vless":
		return s.genVlessLink(inbound, email)
	case "trojan":
		return s.genTrojanLink(inbound, email)
	case "shadowsocks":
		return s.genShadowsocksLink(inbound, email)
	case "hysteria":
		return s.genHysteriaLink(inbound, email)
	case "mtproto":
		return s.genMtprotoLink(inbound, email)
	}
	if carriesWireguardClient(inbound.Protocol) {
		return s.genWireguardLink(inbound, email)
	}
	return s.shareURI(inbound, email)
}

/*
shareURI asks the core that renders this kind's file whether it also has a URI
the list can carry; the registry answers, so no kind is named here.

One link per Host row, like every other generator: a core is handed the host's
address and port and renders the connection, and the remark is stamped here.
Without the fan-out an SSH client behind a CDN was handed the origin address
its Host row exists to replace, and without the remark the link was named
after the inbound's tag rather than the person holding it.
*/
func (s *SubService) shareURI(inbound *model.Inbound, email string) string {
	client, ok := s.clientForLink(inbound, email)
	if !ok {
		return ""
	}
	inst, user := instanceForShare(inbound), userForShare(client)
	render := func(at core.Instance, address string) string {
		share, renders, err := cores.ClientShare(at, user, address)
		if err != nil || !renders {
			return ""
		}
		// The subscription names the link after the remark; a name the core
		// gave it would otherwise sit in front as a second fragment.
		uri, _, _ := strings.Cut(share.URI, "#")
		return uri
	}
	endpoints := externalProxyEndpoints(unmarshalStreamSettings(inbound.StreamSettings))
	if len(endpoints) == 0 {
		uri := render(inst, s.resolveInboundAddress(inbound))
		if uri == "" {
			return ""
		}
		return buildLinkWithParams(uri, nil, s.genRemark(inbound, email, "", ""))
	}
	links := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		dest, _ := ep["dest"].(string)
		port, _ := ep["port"].(float64)
		hosted := inst
		hosted.Port = int(port)
		if uri := render(hosted, dest); uri != "" {
			links = append(links, buildLinkWithParams(uri, nil, s.endpointRemark(inbound, email, ep, "")))
		}
	}
	return strings.Join(links, "\n")
}

// genWireguardLink builds a per-client wireguard:// share link mirroring the
// frontend genWireguardLink: the client's private key is the userinfo, the
// server public key (derived from the inbound secretKey) and the client's
// tunnel address ride in the query. Returns "" when the client has no key.
func (s *SubService) genWireguardLink(inbound *model.Inbound, email string) string {
	if !carriesWireguardClient(inbound.Protocol) {
		return ""
	}
	settings := s.linkSettings(inbound)
	secretKey, _ := settings["secretKey"].(string)

	resolved, ok := s.clientForLink(inbound, email)
	if !ok || resolved.PrivateKey == "" {
		return ""
	}
	client := &resolved

	link := fmt.Sprintf("wireguard://%s@%s", encodeUserinfo(client.PrivateKey), joinHostPort(s.resolveInboundAddress(inbound), inbound.Port))
	params := make(map[string]string)
	if secretKey != "" {
		if pub, err := wgutil.PublicKeyFromPrivate(secretKey); err == nil {
			params["publickey"] = pub
		}
	}
	if joined := strings.Join(client.AllowedIPs, ","); joined != "" {
		params["address"] = joined
	}
	// The client must be told the same MTU the device runs at, or it sends
	// oversized packets in the other direction. S4 is prepended to every data
	// packet, so a device carrying one needs less than the module's own default
	// and a client left to guess picks the default. F103.
	configuredMTU, _ := settings["mtu"].(float64)
	awgParams := awgParamsOf(inbound)
	if configuredMTU > 0 || awgParams.S4 > 0 {
		params["mtu"] = strconv.Itoa(awgParams.DeviceMTU(int(configuredMTU)))
	}
	if dns, ok := settings["dns"].(string); ok && dns != "" {
		params["dns"] = dns
	}
	if psk := client.PSK(); psk != "" {
		params["presharedkey"] = psk
	}
	if client.KeepAlive > 0 {
		params["keepalive"] = strconv.Itoa(client.KeepAlive)
	}
	// Without these an AmneziaWG inbound's link describes a plain WireGuard
	// tunnel, and the config the subscription page derives from it cannot
	// connect to the server that issued it.
	awgLinkParams(inbound, params)

	// One link per Host row, like every L4 generator. Without this a Host
	// configured for a WireGuard inbound was silently ignored.
	if endpoints := externalProxyEndpoints(unmarshalStreamSettings(inbound.StreamSettings)); len(endpoints) > 0 {
		links := make([]string, 0, len(endpoints))
		for _, ep := range endpoints {
			dest, _ := ep["dest"].(string)
			port, _ := ep["port"].(float64)
			hosted := fmt.Sprintf("wireguard://%s@%s", encodeUserinfo(client.PrivateKey), joinHostPort(dest, int(port)))
			links = append(links, buildLinkWithParams(hosted, cloneStringMap(params), s.endpointRemark(inbound, email, ep, "")))
		}
		return strings.Join(links, "\n")
	}

	return buildLinkWithParams(link, params, s.genRemark(inbound, email, "", ""))
}

// externalProxyEndpoints returns the address/port pairs the inbound's Host rows
// declare. wireguard:// and tg://proxy ignored them entirely. F129 / F143.
func externalProxyEndpoints(stream map[string]any) []map[string]any {
	raw, _ := stream["externalProxy"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		ep, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if dest, _ := ep["dest"].(string); dest == "" {
			continue
		}
		if _, ok := ep["port"].(float64); !ok {
			continue
		}
		out = append(out, ep)
	}
	return out
}

// Telegram links omit fragments because lenient clients append them to the
// final query value. Host and WEB delivery policy stay outside the core encoder.
func (s *SubService) genMtprotoLink(inbound *model.Inbound, email string) string {
	if inbound.Protocol != model.MTProto {
		return ""
	}
	resolved, ok := s.clientForLink(inbound, email)
	if !ok || resolved.Secret == "" {
		return ""
	}
	var links []string
	// One link per Host row, same reason as the WireGuard generator above.
	if endpoints := externalProxyEndpoints(unmarshalStreamSettings(inbound.StreamSettings)); len(endpoints) > 0 {
		for _, ep := range endpoints {
			dest, _ := ep["dest"].(string)
			port, _ := ep["port"].(float64)
			links = append(links, sharelink.MTProto(dest, int(port), resolved.Secret))
		}
	} else {
		links = append(links, sharelink.MTProto(s.resolveInboundAddress(inbound), inbound.Port, resolved.Secret))
	}
	if web := mtprotoWebLink(inbound, resolved.Secret); web != "" {
		links = append(links, web)
	}
	return strings.Join(links, "\n")
}

// webRelayFronted is an inbound running the WEB relay. Its links are its own: the
// WEB one carries no port, and a fallback master cannot front the FakeTLS one.
func webRelayFronted(settings string) bool {
	var s struct {
		WebEnable bool `json:"webEnable"`
	}
	return json.Unmarshal([]byte(settings), &s) == nil && s.WebEnable
}

// mtprotoWebLink is the WEB proxy deep link for an inbound running the relay:
// tg://webproxy, the hostname alone since the client fixes https and 443, and
// the same key as dd, the only form a WEB client holds. Confirmed against a
// real Telegram Desktop on 2026-09-07.
func mtprotoWebLink(inbound *model.Inbound, secret string) string {
	var settings struct {
		WebEnable bool   `json:"webEnable"`
		WebHost   string `json:"webHost"`
	}
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil || !settings.WebEnable || settings.WebHost == "" {
		return ""
	}
	dd := model.MtprotoDDSecret(secret)
	if dd == "" {
		return ""
	}
	return buildLinkWithParams("tg://webproxy", map[string]string{"server": settings.WebHost, "secret": dd}, "")
}

func (s *SubService) genVmessLink(inbound *model.Inbound, email string) string {
	if inbound.Protocol != model.VMESS {
		return ""
	}
	template, _, ok := s.xrayShareTemplate(inbound, email)
	if !ok {
		return ""
	}
	stream := unmarshalStreamSettings(inbound.StreamSettings)
	proxies, _ := stream["externalProxy"].([]any)
	if len(proxies) > 0 {
		return s.buildVmessExternalProxyLinks(proxies, template.Object, inbound, email, template.Network)
	}
	return template.Render(s.resolveInboundAddress(inbound), inbound.Port, s.genRemark(inbound, email, "", template.Network))
}

func normalizeVmessSecurity(security string) string {
	return sharelink.NormalizeVmessSecurity(security)
}

// tlsFlowAllowed reports whether a client's XTLS Vision flow belongs in
// generated links/configs. Asked by Trojan too, which is why it is no longer
// named for VLESS: the rule answers false there, and the core refuses one.
func tlsFlowAllowed(protocol model.Protocol, network, security string, settings map[string]any) bool {
	return core.Can(core.CapTLSFlow, core.Facts{
		Protocol: string(protocol),
		Stream:   map[string]string{"network": network, "security": security},
		Settings: core.Scalars(settings),
	})
}

func (s *SubService) genVlessLink(inbound *model.Inbound, email string) string {
	if inbound.Protocol != model.VLESS {
		return ""
	}
	template, _, ok := s.xrayShareTemplate(inbound, email)
	if !ok {
		return ""
	}
	stream := unmarshalStreamSettings(inbound.StreamSettings)
	proxies, _ := stream["externalProxy"].([]any)
	if len(proxies) > 0 {
		return s.buildExternalProxyURLLinks(proxies, template.Params, template.Security,
			func(ep map[string]any, dest string, port int) string {
				return fmt.Sprintf("vless://%s@%s", applyVlessRoute(template.UserInfo, hostVlessRoute(ep)), joinHostPort(dest, port))
			},
			func(ep map[string]any) string { return s.endpointRemark(inbound, email, ep, template.Network) })
	}
	return template.Render(s.resolveInboundAddress(inbound), inbound.Port, s.genRemark(inbound, email, "", template.Network))
}

func (s *SubService) genTrojanLink(inbound *model.Inbound, email string) string {
	if inbound.Protocol != model.Trojan {
		return ""
	}
	template, _, ok := s.xrayShareTemplate(inbound, email)
	if !ok {
		return ""
	}
	stream := unmarshalStreamSettings(inbound.StreamSettings)
	proxies, _ := stream["externalProxy"].([]any)
	if len(proxies) > 0 {
		return s.buildExternalProxyURLLinks(proxies, template.Params, template.Security,
			func(_ map[string]any, dest string, port int) string {
				return fmt.Sprintf("trojan://%s@%s", template.UserInfo, joinHostPort(dest, port))
			},
			func(ep map[string]any) string { return s.endpointRemark(inbound, email, ep, template.Network) })
	}
	return template.Render(s.resolveInboundAddress(inbound), inbound.Port, s.genRemark(inbound, email, "", template.Network))
}

func encodeUserinfo(s string) string {
	return sharelink.EncodeUserinfo(s)
}

func joinHostPort(host string, port int) string {
	return sharelink.JoinHostPort(host, port)
}

func (s *SubService) genShadowsocksLink(inbound *model.Inbound, email string) string {
	if inbound.Protocol != model.Shadowsocks {
		return ""
	}
	template, _, ok := s.xrayShareTemplate(inbound, email)
	if !ok {
		return ""
	}
	stream := unmarshalStreamSettings(inbound.StreamSettings)
	proxies, _ := stream["externalProxy"].([]any)
	if len(proxies) > 0 {
		template.Params["security"] = template.Security
		return s.buildExternalProxyURLLinks(proxies, template.Params, template.Security,
			func(_ map[string]any, dest string, port int) string {
				return fmt.Sprintf("ss://%s@%s", template.UserInfo, joinHostPort(dest, port))
			},
			func(ep map[string]any) string { return s.endpointRemark(inbound, email, ep, template.Network) })
	}
	return template.Render(s.resolveInboundAddress(inbound), inbound.Port, s.genRemark(inbound, email, "", template.Network))
}

func (s *SubService) genHysteriaLink(inbound *model.Inbound, email string) string {
	if inbound.Protocol != model.Hysteria {
		return ""
	}
	template, _, ok := s.xrayShareTemplate(inbound, email)
	if !ok {
		return ""
	}
	stream := unmarshalStreamSettings(inbound.StreamSettings)
	proxies, _ := stream["externalProxy"].([]any)
	if len(proxies) > 0 {
		delete(template.Params, "mport")
		var links []string
		for _, raw := range proxies {
			ep, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			dest, _ := ep["dest"].(string)
			port, ok := ep["port"].(float64)
			if dest == "" || !ok {
				continue
			}
			params := cloneStringMap(template.Params)
			applyExternalProxyHysteriaParams(ep, params)
			endpointStream := endpointFinalMaskStream(stream, ep)
			if hop := hysteriaHopPorts(endpointStream); hop != "" {
				params["mport"] = hop
			}
			applySalamanderParams(endpointStream, params)
			link := fmt.Sprintf("%s://%s@%s", template.Scheme, template.UserInfo, joinHostPort(dest, int(port)))
			links = append(links, buildLinkWithParams(link, params, s.endpointRemark(inbound, email, ep, "quic")))
		}
		return strings.Join(links, "\n")
	}
	return template.Render(s.resolveInboundAddress(inbound), inbound.Port, s.genRemark(inbound, email, "", "quic"))
}

// endpointFinalMaskStream returns the stream one endpoint reads its masks from:
// the inbound's, with that endpoint's own Final Mask merged in. The Hosts page
// offers the field precisely so a host can carry its own.
func endpointFinalMaskStream(stream, ep map[string]any) map[string]any {
	fm, _ := ep["finalMask"].(string)
	if fm == "" {
		return stream
	}
	var masks map[string]any
	if json.Unmarshal([]byte(fm), &masks) != nil || len(masks) == 0 {
		return stream
	}
	return map[string]any{"finalmask": mergeFinalMask(stream["finalmask"], masks)}
}

func salamanderPassword(stream map[string]any) string {
	return sharelink.SalamanderPassword(stream)
}

func applySalamanderParams(stream map[string]any, params map[string]string) {
	sharelink.ApplySalamanderParams(stream, params)
}

func hysteriaHopPorts(stream map[string]any) string {
	return sharelink.HysteriaHopPorts(stream)
}

// loadNodes refreshes nodesByID from the DB. Called once per request so
// the per-inbound resolveInboundAddress lookups are pure map reads.
// We filter to address != ” so a half-configured node row doesn't
// accidentally produce a useless host like "https://:2053".
func (s *SubService) loadNodes() {
	db := database.GetDB()
	var nodes []*model.Node
	if err := db.Model(&model.Node{}).Where("address != ''").Find(&nodes).Error; err != nil {
		logger.Warning("subscription: load nodes failed:", err)
		s.nodesByID = nil
		return
	}
	m := make(map[int]*model.Node, len(nodes))
	for _, n := range nodes {
		m[n.Id] = n
	}
	s.nodesByID = m
}

// resolveInboundAddress picks the host an external client should connect to,
// honoring the inbound's share address strategy the same way the panel's
// share/QR link builder does (#5208):
//   - "listen": an explicit, client-reachable bind Listen wins, backed by the
//     node's address for node-managed inbounds;
//   - "custom": the inbound's ShareAddr wins, then node, then listen;
//   - "node" (default, and any unknown value): the node's address for
//     node-managed inbounds, then a routable Listen — the pre-strategy order.
//
// Every chain ends at the admin's configured public host (Sub/Web domain) and
// then the subscriber's request host (s.address). Preferring the configured
// host over the request host for this last resort keeps a wildcard local inbound
// from advertising a bogus client IP that leaked into the request Host header
// behind NAT/proxy/CDN (#5425). A loopback/wildcard bind or a unix-domain-socket
// listen is a server-side detail and is never advertised; External Proxy still
// overrides everything upstream of this call.
func (s *SubService) resolveInboundAddress(inbound *model.Inbound) string {
	var nodeAddr string
	if inbound.NodeID != nil && s.nodesByID != nil {
		if n, ok := s.nodesByID[*inbound.NodeID]; ok {
			nodeAddr = n.Address
		}
	}
	var listenAddr string
	if listen := inbound.Listen; listen != "" && listen[0] != '@' && listen[0] != '/' && isRoutableHost(listen) {
		listenAddr = listen
	}

	candidates := []string{nodeAddr, listenAddr}
	switch inbound.ShareAddrStrategy {
	case "listen":
		candidates = []string{listenAddr, nodeAddr}
	case "custom":
		candidates = []string{strings.TrimSpace(inbound.ShareAddr), nodeAddr, listenAddr}
	}
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	if d := s.configuredPublicHost(); d != "" {
		return d
	}
	return s.address
}

func findClientIndex(clients []model.Client, email string) int {
	for i, client := range clients {
		if client.Email == email {
			return i
		}
	}
	return -1
}

func unmarshalStreamSettings(streamSettings string) map[string]any {
	return sharelink.UnmarshalStreamSettings(streamSettings)
}

func applyPathAndHostParams(settings map[string]any, params map[string]string) {
	sharelink.ApplyPathAndHostParams(settings, params)
}

func verifyPeerCertByNameValue(tlsClientSettings any) (string, bool) {
	return sharelink.VerifyPeerCertByNameValue(tlsClientSettings)
}

func pinnedSha256List(tlsClientSettings any) ([]string, bool) {
	return sharelink.PinnedSha256List(tlsClientSettings)
}

// subKey returns a stable per-client identity for deterministic derivations,
// preferring the subscription id and falling back to the (unique) email.
func subKey(c model.Client) string {
	if c.SubID != "" {
		return c.SubID
	}
	return c.Email
}

func deriveSpiderX(seed, clientKey string) string {
	return sharelink.DeriveSpiderX(seed, clientKey)
}

func deriveListIndex(field, clientKey string, n int) int {
	return sharelink.DeriveListIndex(field, clientKey, n)
}

func buildVmessLink(obj map[string]any) string {
	return sharelink.BuildVmessLink(obj)
}

func cloneVmessShareObj(baseObj map[string]any, newSecurity string) map[string]any {
	newObj := map[string]any{}
	for key, value := range baseObj {
		if newSecurity != "none" || (key != "alpn" && key != "sni" && key != "fp" && key != "pcs") {
			newObj[key] = value
		}
	}
	return newObj
}

func applyExternalProxyTLSObj(ep map[string]any, obj map[string]any, security string) {
	if security != "tls" {
		return
	}
	if externalProxySNIBlank(ep) {
		delete(obj, "sni")
	} else if sni, ok := externalProxySNI(ep); ok {
		obj["sni"] = sni
	}
	if fp, ok := ep["fingerprint"].(string); ok && fp != "" {
		obj["fp"] = fp
	}
	if alpn, ok := externalProxyALPN(ep["alpn"]); ok {
		obj["alpn"] = alpn
	}
	if pins, ok := externalProxyPins(ep["pinnedPeerCertSha256"]); ok {
		obj["pcs"] = joinAnyStrings(pins)
	}
	if vcn, ok := ep["verifyPeerCertByName"].(string); ok && vcn != "" {
		obj["vcn"] = vcn
	}
	if ech, ok := ep["echConfigList"].(string); ok && ech != "" {
		obj["ech"] = ech
	}
}

func applyExternalProxyTLSParams(ep map[string]any, params map[string]string, security string) {
	if security != "tls" {
		return
	}
	if externalProxySNIBlank(ep) {
		delete(params, "sni")
	} else if sni, ok := externalProxySNI(ep); ok {
		params["sni"] = sni
	}
	if fp, ok := ep["fingerprint"].(string); ok && fp != "" {
		params["fp"] = fp
	}
	if alpn, ok := externalProxyALPN(ep["alpn"]); ok {
		params["alpn"] = alpn
	}
	if pins, ok := externalProxyPins(ep["pinnedPeerCertSha256"]); ok {
		params["pcs"] = joinAnyStrings(pins)
	}
	if vcn, ok := ep["verifyPeerCertByName"].(string); ok && vcn != "" {
		params["vcn"] = vcn
	}
	if ech, ok := ep["echConfigList"].(string); ok && ech != "" {
		params["ech"] = ech
	}
}

// applyExternalProxyHysteriaParams overrides a Hysteria link from one Host row;
// the pin is hex `pinSHA256`, not the `pcs` URL-param protocols use. F139.
func applyExternalProxyHysteriaParams(ep map[string]any, params map[string]string) {
	if pins, ok := externalProxyPins(ep["pinnedPeerCertSha256"]); ok {
		params["pinSHA256"] = joinAnyStrings(pins)
	}
	if ai, ok := ep["allowInsecure"].(bool); ok && ai {
		params["insecure"] = "1"
	}
	if externalProxySNIBlank(ep) {
		delete(params, "sni")
	} else if sni, ok := ep["sni"].(string); ok && sni != "" {
		params["sni"] = sni
	}
	if fp, ok := ep["fingerprint"].(string); ok && fp != "" {
		params["fp"] = fp
	}
	if alpn := externalProxyAlpn(ep["alpn"]); alpn != "" {
		params["alpn"] = alpn
	}
}

// externalProxyAlpn flattens a host row's ALPN list into the comma-joined form
// a link carries.
func externalProxyAlpn(value any) string {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, ",")
}

// cloneStreamForExternalProxy returns a shallow clone of stream with
// tlsSettings (and its nested settings map) deep-copied. The external
// proxy loop mutates tlsSettings per iteration, so without isolating
// those maps each proxy's SNI/fingerprint/ALPN would leak into the next.
// cloneStreamForExternalProxy returns a stream one endpoint can be merged into
// without reaching the next one. The merge writes DEEP -- a host's path into
// wsSettings, its Host header into xhttpSettings, its SNI into realitySettings
// -- so anything short of a deep copy shares the very maps it mutates.
func cloneStreamForExternalProxy(stream map[string]any) map[string]any {
	if stream == nil {
		return nil
	}
	out, _ := deepCloneJSON(stream).(map[string]any)
	return out
}

// deepCloneJSON copies a decoded-JSON tree. Scalars are immutable and shared.
func deepCloneJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, inner := range v {
			out[key] = deepCloneJSON(inner)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, inner := range v {
			out[i] = deepCloneJSON(inner)
		}
		return out
	default:
		return value
	}
}

func applyExternalProxyTLSToStream(ep map[string]any, stream map[string]any, security string) {
	if security != "tls" {
		return
	}
	tlsSettings, _ := stream["tlsSettings"].(map[string]any)
	if tlsSettings == nil {
		tlsSettings = map[string]any{}
		stream["tlsSettings"] = tlsSettings
	}
	if externalProxySNIBlank(ep) {
		// Assigned, not deleted: json and clash copy this key unconditionally
		// and a missing one renders as null rather than as absent.
		tlsSettings["serverName"] = ""
	} else if sni, ok := externalProxySNI(ep); ok {
		tlsSettings["serverName"] = sni
	}
	if fp, ok := ep["fingerprint"].(string); ok && fp != "" {
		tlsSettings["fingerprint"] = fp
	}
	if alpn, ok := externalProxyALPNList(ep["alpn"]); ok {
		tlsSettings["alpn"] = alpn
	}
	// Top level, where xray's TLSConfig reads them: the renderer flattened the
	// inbound's own "settings" bag before this ran, so a key left there is dropped.
	if pins, ok := externalProxyPins(ep["pinnedPeerCertSha256"]); ok {
		joined := make([]string, 0, len(pins))
		for _, pin := range pins {
			if s, ok := pin.(string); ok {
				joined = append(joined, s)
			}
		}
		tlsSettings["pinnedPeerCertSha256"] = strings.Join(joined, ",")
		tlsSettings["pin-sha256"] = pins
	}
	if ech, ok := ep["echConfigList"].(string); ok && ech != "" {
		tlsSettings["echConfigList"] = ech
	}
	if vcn, ok := ep["verifyPeerCertByName"].(string); ok && vcn != "" {
		tlsSettings["verifyPeerCertByName"] = vcn
	}
	if ai, ok := ep["allowInsecure"].(bool); ok && ai {
		settings, _ := tlsSettings["settings"].(map[string]any)
		if settings == nil {
			settings = map[string]any{}
			tlsSettings["settings"] = settings
		}
		settings["allowInsecure"] = true
	}
}

// externalProxySNIBlank is separate from externalProxySNI on purpose: the
// hysteria applier reads ep["sni"] directly and would not be covered by it.
func externalProxySNIBlank(ep map[string]any) bool {
	b, _ := ep["sniBlank"].(bool)
	return b
}

func externalProxySNI(ep map[string]any) (string, bool) {
	if sni, ok := ep["sni"].(string); ok && sni != "" {
		return sni, true
	}
	return "", false
}

func externalProxyALPN(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, v != ""
	case []string:
		if len(v) == 0 {
			return "", false
		}
		return strings.Join(v, ","), true
	case []any:
		alpn := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				alpn = append(alpn, s)
			}
		}
		if len(alpn) == 0 {
			return "", false
		}
		return strings.Join(alpn, ","), true
	default:
		return "", false
	}
}

func externalProxyALPNList(value any) ([]any, bool) {
	switch v := value.(type) {
	case string:
		if v == "" {
			return nil, false
		}
		parts := strings.Split(v, ",")
		out := make([]any, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out, len(out) > 0
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if item != "" {
				out = append(out, item)
			}
		}
		return out, len(out) > 0
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

// externalProxyPins extracts an external-proxy entry's pinnedPeerCertSha256
// as a []any of non-empty strings, in the one form the core reads.
//
// Normalised HERE for the same reason pinnedSha256List does it: a pin crosses
// into a link at four call sites, and doing it at each of them is how this
// ended up hex for Hysteria and base64 everywhere else. Ledger L21.
func externalProxyPins(value any) ([]any, bool) {
	switch v := value.(type) {
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if item != "" {
				out = append(out, link.NormalizePinHex(item))
			}
		}
		return out, len(out) > 0
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, link.NormalizePinHex(s))
			}
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

func joinAnyStrings(items []any) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ",")
}

// buildVmessExternalProxyLinks is a thin adapter: it maps the legacy
// externalProxy entries to []ShareEndpoint and renders them through the unified
// endpoint path. Kept as a thin shim over the unified endpoint builder so
// genVmessLink keeps calling one helper (now threading transport through).
func (s *SubService) buildVmessExternalProxyLinks(externalProxies []any, baseObj map[string]any, inbound *model.Inbound, email string, transport string) string {
	eps := make([]ShareEndpoint, 0, len(externalProxies))
	for _, externalProxy := range externalProxies {
		ep, _ := externalProxy.(map[string]any)
		eps = append(eps, externalProxyToEndpoint(ep))
	}
	return s.buildEndpointVmessLinks(eps, baseObj, inbound, email, transport)
}

func buildLinkWithParams(link string, params map[string]string, fragment string) string {
	return sharelink.BuildLinkWithParams(link, params, fragment)
}

func buildLinkWithParamsAndSecurity(link string, params map[string]string, fragment, security string, omitTLSFields bool) string {
	return sharelink.BuildLinkWithParamsAndSecurity(link, params, fragment, security, omitTLSFields)
}

// buildExternalProxyURLLinks is a thin adapter: it maps the legacy externalProxy
// entries to []ShareEndpoint and renders them through the unified endpoint path.
// Kept so the genVless/genTrojan/genShadowsocks call sites are unchanged.
func (s *SubService) buildExternalProxyURLLinks(
	externalProxies []any,
	params map[string]string,
	baseSecurity string,
	makeLink func(ep map[string]any, dest string, port int) string,
	makeRemark func(ep map[string]any) string,
) string {
	eps := make([]ShareEndpoint, 0, len(externalProxies))
	for _, externalProxy := range externalProxies {
		ep, _ := externalProxy.(map[string]any)
		eps = append(eps, externalProxyToEndpoint(ep))
	}
	return s.buildEndpointLinks(eps, params, baseSecurity, func(e ShareEndpoint) string {
		return makeLink(e.ep, e.Address, e.Port)
	}, func(e ShareEndpoint) string {
		return makeRemark(e.ep)
	})
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	maps.Copy(cloned, source)
	return cloned
}

// genRemark builds the remark for a non-host link (raw default / legacy
// externalProxy / synthetic JSON-Clash entry). A set remark template drives it
// in both the body and display contexts (genTemplatedRemark renders the
// name-only part on displays); with no template it falls back to the inbound
// remark, extra and email joined by "-".
func (s *SubService) genRemark(inbound *model.Inbound, email string, extra string, transport string) string {
	if s.remarkTemplate != "" {
		return s.genTemplatedRemark(inbound, s.lookupClient(inbound, email), extra, transport)
	}
	return fallbackRemark(inbound.Remark, extra, email)
}

func fallbackRemark(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "-")
}

// findClientStats returns the inbound's traffic record for email, if present.
func (s *SubService) findClientStats(inbound *model.Inbound, email string) (xray.ClientTraffic, bool) {
	for _, clientStat := range inbound.ClientStats {
		if clientStat.Email == email {
			return clientStat, true
		}
	}
	return xray.ClientTraffic{}, false
}

// statsByEmailFromDB resolves a client's traffic row straight from the DB by its
// globally-unique email, caching the hit into statsByEmail for the rest of the
// request. It's the last-resort lookup behind statsForClient: the preloaded
// ClientStats and the statsByEmail index are both keyed by
// client_traffics.inbound_id, which is written once by AddClientStat and never
// updated. When an inbound is deleted and recreated it gets a new id, so the old
// row is orphaned from every loaded inbound and both in-memory paths miss —
// leaving {{TRAFFIC_USED}} stuck at 0 for pre-existing clients even though their
// usage is intact (#5567). Matching by email recovers it, the same way the
// sub-info header's AggregateTrafficByEmails already does.
func (s *SubService) statsByEmailFromDB(email string) (xray.ClientTraffic, bool) {
	db := database.GetDB()
	if db == nil {
		return xray.ClientTraffic{}, false
	}
	var row xray.ClientTraffic
	if err := db.Model(&xray.ClientTraffic{}).Where("email = ?", email).First(&row).Error; err != nil {
		return xray.ClientTraffic{}, false
	}
	if s.statsByEmail == nil {
		s.statsByEmail = map[string]xray.ClientTraffic{}
	}
	s.statsByEmail[email] = row
	return row, true
}

func searchKey(data any, key string) (any, bool) {
	return sharelink.SearchKey(data, key)
}

func buildXhttpExtra(xhttp map[string]any) map[string]any {
	return sharelink.BuildXhttpExtra(xhttp)
}

func nonZeroShareValue(v any) (any, bool) {
	return sharelink.NonZeroShareValue(v)
}

type kcpShareFields struct {
	headerType string
	seed       string
	mtu        int
	tti        int
}

func (f kcpShareFields) applyToParams(params map[string]string) {
	sharelink.KcpShareFields{HeaderType: f.headerType, Seed: f.seed, MTU: f.mtu, TTI: f.tti}.ApplyToParams(params)
}

func extractKcpShareFields(stream map[string]any) kcpShareFields {
	value := sharelink.ExtractKcpShareFields(stream)
	return kcpShareFields{headerType: value.HeaderType, seed: value.Seed, mtu: value.MTU, tti: value.TTI}
}

func readPositiveInt(value any) (int, bool) {
	return sharelink.ReadPositiveInt(value)
}

func setStringParam(params map[string]string, key, value string) {
	sharelink.SetStringParam(params, key, value)
}

func setIntParam(params map[string]string, key string, value int) {
	sharelink.SetIntParam(params, key, value)
}

func setStringField(obj map[string]any, key, value string) {
	sharelink.SetStringField(obj, key, value)
}

func setIntField(obj map[string]any, key string, value int) {
	sharelink.SetIntField(obj, key, value)
}

func marshalFinalMask(finalmask map[string]any) (string, bool) {
	return sharelink.MarshalFinalMask(finalmask)
}

func hasFinalMaskContent(value any) bool {
	return sharelink.HasFinalMaskContent(value)
}

func searchHost(headers any) string {
	return sharelink.SearchHost(headers)
}

// PageData is a view model for subpage.html
// PageData contains data for rendering the subscription information page.
type PageData struct {
	Host          string
	BasePath      string
	SId           string
	Enabled       bool
	IsOnline      bool
	Download      string
	Upload        string
	Total         string
	Used          string
	Remained      string
	Expire        int64
	LastOnline    int64
	Datepicker    string
	DownloadByte  int64
	UploadByte    int64
	TotalByte     int64
	SubUrl        string
	SubJsonUrl    string
	SubClashUrl   string
	SubTitle      string
	SubSupportUrl string
	Result        []string
	Emails        []string
	/*
		Ended[i] says the client behind Result[i] is switched off — out of
		traffic, past its expiry, or disabled by hand.

		Its link is still served: the body has to keep parsing for every app that
		fetches it, and an empty subscription reads as a broken URL rather than as
		an ended account. So the PAGE is where the truth goes, per entry, because
		the page is the one surface a person actually looks at. F249.
	*/
	Ended []bool
	// FileUrls[i] is where the sub server serves Result[i]'s config file, or "".
	FileUrls []string
}

// ResolveRequest extracts scheme and host info from request/headers consistently.
// ResolveRequest extracts scheme, host, and header information from an HTTP request.
func (s *SubService) ResolveRequest(c *gin.Context) (scheme string, host string, hostWithPort string, hostHeader string) {
	trusted := s.forwardedHeadersTrusted(c)
	if !trusted {
		warnSuppressedForwardedHeaders(c)
	}
	forwarded := func(name string) string {
		if !trusted {
			return ""
		}
		return c.GetHeader(name)
	}

	// scheme
	scheme = "http"
	if c.Request.TLS != nil || strings.EqualFold(forwarded("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}

	// base host (no port)
	if h, err := getHostFromXFH(forwarded("X-Forwarded-Host")); err == nil && h != "" {
		host = h
	}
	if host == "" {
		var err error
		host, _, err = net.SplitHostPort(c.Request.Host)
		if err != nil {
			host = c.Request.Host
		}
	}

	// host:port for URLs
	hostWithPort = forwarded("X-Forwarded-Host")
	if hostWithPort == "" {
		hostWithPort = c.Request.Host
	}
	if hostWithPort == "" {
		hostWithPort = host
	}

	// header display host
	hostHeader = forwarded("X-Forwarded-Host")
	if hostHeader == "" {
		hostHeader = host
	}
	return
}

// BuildURLs constructs absolute subscription and JSON subscription URLs for a given subscription ID.
// It prioritizes configured URIs, then individual settings, and finally falls back to request-derived components.
func (s *SubService) BuildURLs(subPath, subJsonPath, subClashPath, subId string) (subURL, subJsonURL, subClashURL string) {
	if subId == "" {
		return "", "", ""
	}

	configuredSubURI, _ := s.settingService.GetSubURI()
	configuredSubJsonURI, _ := s.settingService.GetSubJsonURI()
	configuredSubClashURI, _ := s.settingService.GetSubClashURI()

	// Same base as the panel's Client Information page; s.address is the
	// subscriber's host already normalized away from any loopback/bind IP.
	base := s.settingService.BuildSubURIBase(s.address)

	subURL = s.buildSingleURL(configuredSubURI, base, subPath, subId)

	// When subURI is explicitly configured (reverse-proxy setup), use its
	// scheme+host as the base for JSON and Clash URLs so they match the
	// reverse-proxy endpoint instead of the raw sub-server port. Fall back
	// to the request-derived base if subURI is empty or can't be parsed
	// into a scheme+host (e.g. a malformed value with no scheme).
	jsonClashBase := base
	if configuredSubURI != "" {
		if derived := s.extractBaseFromURI(configuredSubURI); derived != "" {
			jsonClashBase = derived
		}
	}

	subJsonURL = s.buildSingleURL(configuredSubJsonURI, jsonClashBase, subJsonPath, subId)
	subClashURL = s.buildSingleURL(configuredSubClashURI, jsonClashBase, subClashPath, subId)

	return subURL, subJsonURL, subClashURL
}

// extractBaseFromURI extracts scheme://host from a configured URI.
// e.g., "https://example.com/sub-xxx/" → "https://example.com".
// Returns "" when the URI is empty or lacks a scheme/host, so callers can
// fall back to the request-derived base instead of emitting a broken value.
func (s *SubService) extractBaseFromURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
}

// buildSingleURL constructs a single URL using configured URI or base components
func (s *SubService) buildSingleURL(configuredURI, base, basePath, subId string) string {
	if configuredURI != "" {
		return s.joinPathWithID(configuredURI, subId)
	}
	return s.joinPathWithID(base+basePath, subId)
}

// joinPathWithID safely joins a base path with a subscription ID
func (s *SubService) joinPathWithID(basePath, subId string) string {
	if strings.HasSuffix(basePath, "/") {
		return basePath + subId
	}
	return basePath + "/" + subId
}

// BuildPageData parses header and prepares the template view model.
// BuildPageData constructs page data for rendering the subscription information page.
func (s *SubService) BuildPageData(subId string, hostHeader string, traffic xray.ClientTraffic, lastOnline int64, subs []string, emails []string, fileInboundIds []int, subURL, subJsonURL, subClashURL string, basePath string, subTitle string, subSupportUrl string) PageData {
	download := common.FormatTraffic(traffic.Down)
	upload := common.FormatTraffic(traffic.Up)
	total := "∞"
	used := common.FormatTraffic(traffic.Up + traffic.Down)
	remained := ""
	if traffic.Total > 0 {
		total = common.FormatTraffic(traffic.Total)
		left := max(traffic.Total-(traffic.Up+traffic.Down), 0)
		remained = common.FormatTraffic(left)
	}

	datepicker := s.datepicker
	if datepicker == "" {
		datepicker = "gregorian"
	}

	ended := s.endedEmails(emails)
	pageLinks := make([]string, 0, len(subs))
	pageEmails := make([]string, 0, len(subs))
	pageEnded := make([]bool, 0, len(subs))
	pageFiles := make([]string, 0, len(subs))
	for i, sub := range subs {
		email := ""
		if i < len(emails) {
			email = emails[i]
		}
		fileURL := ""
		if i < len(fileInboundIds) && fileInboundIds[i] > 0 && subURL != "" {
			fileURL = strings.TrimRight(subURL, "/") + "/file/" + strconv.Itoa(fileInboundIds[i])
		}
		_, off := ended[email]
		for _, link := range splitLinkLines(sub) {
			pageLinks = append(pageLinks, link)
			pageEmails = append(pageEmails, email)
			pageEnded = append(pageEnded, off)
			pageFiles = append(pageFiles, fileURL)
		}
	}

	return PageData{
		Host:          hostHeader,
		BasePath:      basePath,
		SId:           subId,
		Enabled:       traffic.Enable,
		IsOnline:      subIsOnline(emails, s.inboundService.GetOnlineClients()),
		Download:      download,
		Upload:        upload,
		Total:         total,
		Used:          used,
		Remained:      remained,
		Expire:        traffic.ExpiryTime / 1000,
		LastOnline:    lastOnline,
		Datepicker:    datepicker,
		DownloadByte:  traffic.Down,
		UploadByte:    traffic.Up,
		TotalByte:     traffic.Total,
		SubUrl:        subURL,
		SubJsonUrl:    subJsonURL,
		SubClashUrl:   subClashURL,
		SubTitle:      subTitle,
		SubSupportUrl: subSupportUrl,
		Result:        pageLinks,
		Emails:        pageEmails,
		Ended:         pageEnded,
		FileUrls:      pageFiles,
	}
}

/*
endedEmails names the clients of this subscription that are switched off.

Read from the clients table rather than from the settings blob each link came
out of: `enable` there is the flag the traffic job clears when a client runs out
or expires, which is the state a subscriber is asking about. Page-path only, so
the body renderers pay nothing for it.
*/
func (s *SubService) endedEmails(emails []string) map[string]struct{} {
	out := map[string]struct{}{}
	if len(emails) == 0 {
		return out
	}
	db := database.GetDB()
	if db == nil {
		// The page renders without the "ended" tag rather than panicking. Only a
		// caller with no database reaches this -- BuildPageData is otherwise
		// reachable from a renderer test that never opened one.
		return out
	}
	var rows []model.ClientRecord
	if err := db.Model(&model.ClientRecord{}).
		Where("email IN ? AND enable = ?", emails, false).
		Find(&rows).Error; err != nil {
		logger.Warning("SubService - endedEmails:", err)
		return out
	}
	for _, row := range rows {
		out[row.Email] = struct{}{}
	}
	return out
}

func subIsOnline(subEmails, onlineEmails []string) bool {
	if len(subEmails) == 0 || len(onlineEmails) == 0 {
		return false
	}
	onlineSet := make(map[string]struct{}, len(onlineEmails))
	for _, email := range onlineEmails {
		onlineSet[email] = struct{}{}
	}
	for _, email := range subEmails {
		if _, online := onlineSet[email]; online {
			return true
		}
	}
	return false
}

func getHostFromXFH(s string) (string, error) {
	if strings.Contains(s, ":") {
		realHost, _, err := net.SplitHostPort(s)
		if err != nil {
			return "", err
		}
		return realHost, nil
	}
	return s, nil
}

func (s *SubService) xrayShareTemplate(inbound *model.Inbound, email string) (sharelink.XrayTemplate, model.Client, bool) {
	client, ok := s.clientForLink(inbound, email)
	if !ok {
		return sharelink.XrayTemplate{}, client, false
	}
	instance := instanceForShare(inbound)
	settings, _ := json.Marshal(s.linkSettings(inbound))
	instance.Settings = string(settings)
	template, err := sharelink.PrepareXray(instance, userForShare(client), subKey(client))
	return template, client, err == nil
}
