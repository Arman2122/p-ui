// Package service provides business logic services for the Penhoon UI web panel,
// including inbound/outbound management, user administration, settings, and Xray integration.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/mtproto"
	"github.com/Arman2122/p-ui/internal/util/common"
	"github.com/Arman2122/p-ui/internal/util/netsafe"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type InboundService struct {
	clientService   ClientService
	fallbackService FallbackService
}

func normalizeTrafficResetDay(day int) int {
	if day < 1 {
		return 1
	}
	return min(day, 31)
}

func normalizeInboundShareAddrStrategy(strategy string) string {
	strategy = strings.TrimSpace(strategy)
	switch strategy {
	case "listen", "custom":
		return strategy
	default:
		return "node"
	}
}

func normalizeInboundShareAddress(inbound *model.Inbound) {
	if inbound == nil {
		return
	}
	inbound.ShareAddrStrategy = normalizeInboundShareAddrStrategy(inbound.ShareAddrStrategy)
	if addr, err := normalizeInboundShareHost(inbound.ShareAddr); err == nil {
		inbound.ShareAddr = addr
	} else {
		inbound.ShareAddr = strings.TrimSpace(inbound.ShareAddr)
	}
}

func normalizeInboundShareAddressStrict(inbound *model.Inbound) error {
	if inbound == nil {
		return nil
	}
	inbound.ShareAddrStrategy = normalizeInboundShareAddrStrategy(inbound.ShareAddrStrategy)
	addr, err := normalizeInboundShareHost(inbound.ShareAddr)
	if err != nil {
		return common.NewError("shareAddr must be a host or IP without scheme or port")
	}
	inbound.ShareAddr = addr
	return nil
}

func normalizeInboundShareHost(raw string) (string, error) {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return "", nil
	}
	if strings.Contains(addr, "://") || strings.HasPrefix(addr, "//") || strings.ContainsAny(addr, "/?#@") {
		return "", fmt.Errorf("invalid share address %q", raw)
	}
	if strings.HasPrefix(addr, "[") {
		if !strings.HasSuffix(addr, "]") {
			return "", fmt.Errorf("invalid IPv6 host %q", raw)
		}
		ip := net.ParseIP(addr[1 : len(addr)-1])
		if ip == nil || ip.To4() != nil {
			return "", fmt.Errorf("invalid IPv6 host %q", raw)
		}
		return "[" + ip.String() + "]", nil
	}
	if strings.Contains(addr, ":") {
		if _, _, err := net.SplitHostPort(addr); err == nil {
			return "", fmt.Errorf("share address must not include port")
		}
		ip := net.ParseIP(addr)
		if ip == nil || ip.To4() != nil {
			return "", fmt.Errorf("invalid IPv6 host %q", raw)
		}
		return "[" + ip.String() + "]", nil
	}
	host, err := netsafe.NormalizeHost(addr)
	if err != nil {
		return "", err
	}
	return host, nil
}

func normalizeInboundShareAddressColumns(tx *gorm.DB) error {
	if tx == nil || !tx.Migrator().HasColumn(&model.Inbound{}, "share_addr_strategy") {
		return nil
	}

	strategyExpr := `CASE TRIM(COALESCE(share_addr_strategy, '')) WHEN 'listen' THEN 'listen' WHEN 'custom' THEN 'custom' ELSE 'node' END`
	if err := tx.Exec(`UPDATE inbounds SET share_addr_strategy = ` + strategyExpr + ` WHERE share_addr_strategy IS NULL OR share_addr_strategy <> ` + strategyExpr).Error; err != nil {
		return err
	}
	hasShareAddr := tx.Migrator().HasColumn(&model.Inbound{}, "share_addr")
	if hasShareAddr {
		if err := tx.Exec(`UPDATE inbounds SET share_addr = TRIM(share_addr) WHERE share_addr IS NOT NULL AND share_addr <> TRIM(share_addr)`).Error; err != nil {
			return err
		}
	}
	if !hasShareAddr {
		return nil
	}
	var rows []struct {
		Id                int
		ShareAddrStrategy string
		ShareAddr         string
	}
	if err := tx.Model(&model.Inbound{}).Select("id", "share_addr_strategy", "share_addr").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		strategy := normalizeInboundShareAddrStrategy(row.ShareAddrStrategy)
		addr, addrErr := normalizeInboundShareHost(row.ShareAddr)
		if addrErr != nil {
			strategy = "node"
			addr = ""
		}
		updates := map[string]any{}
		if strategy != row.ShareAddrStrategy {
			updates["share_addr_strategy"] = strategy
		}
		if addr != row.ShareAddr {
			updates["share_addr"] = addr
		}
		if len(updates) > 0 {
			if err := tx.Model(&model.Inbound{}).Where("id = ?", row.Id).Updates(updates).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// GetInbounds retrieves all inbounds for a specific user with client stats.
func (s *InboundService) GetInbounds(userId int) ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Preload("ClientStats").Where("user_id = ?", userId).Order("id ASC").Find(&inbounds).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	s.enrichClientStats(db, inbounds)
	s.annotateFallbackParents(db, inbounds)
	s.annotateLocalOriginGuid(inbounds)
	return inbounds, nil
}

// annotateLocalOriginGuid fills OriginNodeGuid for this panel's OWN inbounds
// (NodeID == nil) with the panel's stable GUID; inbounds synced from a node
// already carry the originating node's GUID. Read-time only (not persisted) so
// the per-inbound online view can scope by GUID uniformly across a chain of
// nodes (#4983).
func (s *InboundService) annotateLocalOriginGuid(inbounds []*model.Inbound) {
	if len(inbounds) == 0 {
		return
	}
	guid := s.panelGuid()
	if guid == "" {
		return
	}
	for _, ib := range inbounds {
		if ib.OriginNodeGuid == "" && ib.NodeID == nil {
			ib.OriginNodeGuid = guid
		}
	}
}

// GetInboundsSlim returns the same list of inbounds as GetInbounds but
// strips every per-client field other than email / enable / comment from
// settings.clients and skips UUID/SubId enrichment on ClientStats. The
// inbounds page only needs those three to roll up client counts and
// render badges, so this trims tens of bytes per client (UUID, password,
// flow, security, totalGB, expiryTime, limitIp, tgId, ...) which adds
// up fast on installs with thousands of clients.
//
// Full client data is still available through GET /panel/api/inbounds/get/:id
// for the edit/info/qr/export/clone flows that need it.
func (s *InboundService) GetInboundsSlim(userId int) ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Preload("ClientStats").Where("user_id = ?", userId).Order("id ASC").Find(&inbounds).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	s.annotateFallbackParents(db, inbounds)
	s.annotateLocalOriginGuid(inbounds)
	// Top up stats rows owned by sibling inbounds (multi-attached clients)
	// so the list's depleted/expiring badges see every client; the UUID/SubId
	// enrichment stays skipped. Must run before slimming strips the settings.
	s.backfillClientStats(db, inbounds)
	// Slim feeds the panel UI only (masters poll the full list), so the badge
	// math may see the cross-panel totals a master pushed.
	s.overlayInboundsClientStats(db, inbounds)
	for _, ib := range inbounds {
		ib.Settings = slimSettingsClients(ib.Settings)
	}
	return inbounds, nil
}

// slimSettingsClients rewrites the inbound settings JSON so settings.clients[]
// keeps only the fields the list view actually reads. Returns the input
// unchanged when the JSON can't be parsed or has no clients array.
func slimSettingsClients(settings string) string {
	if settings == "" {
		return settings
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(settings), &raw); err != nil {
		return settings
	}
	clients, ok := raw["clients"].([]any)
	if !ok || len(clients) == 0 {
		return settings
	}
	slim := make([]any, 0, len(clients))
	for _, entry := range clients {
		c, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		row := make(map[string]any, 3)
		if v, ok := c["email"]; ok {
			row["email"] = v
		}
		if v, ok := c["enable"]; ok {
			row["enable"] = v
		}
		if v, ok := c["comment"]; ok && v != "" {
			row["comment"] = v
		}
		slim = append(slim, row)
	}
	raw["clients"] = slim
	out, err := json.Marshal(raw)
	if err != nil {
		return settings
	}
	return string(out)
}

// annotateFallbackParents fills FallbackParent on each inbound that is
// the child side of a fallback rule. One DB round-trip serves the full
// list — the frontend needs this to rewrite the child's client-share
// link so it points at the master's reachable endpoint.
func (s *InboundService) annotateFallbackParents(db *gorm.DB, inbounds []*model.Inbound) {
	if len(inbounds) == 0 {
		return
	}
	childIds := make([]int, 0, len(inbounds))
	for _, ib := range inbounds {
		childIds = append(childIds, ib.Id)
	}
	var rows []model.InboundFallback
	if err := db.Where("child_id IN ?", childIds).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error; err != nil {
		return
	}
	first := make(map[int]model.InboundFallback, len(rows))
	for _, r := range rows {
		if _, ok := first[r.ChildId]; !ok {
			first[r.ChildId] = r
		}
	}
	for _, ib := range inbounds {
		if r, ok := first[ib.Id]; ok {
			ib.FallbackParent = &model.FallbackParentInfo{
				MasterId: r.MasterId,
				Path:     r.Path,
			}
		}
	}
}

type InboundOption struct {
	Id             int    `json:"id" example:"1"`
	Remark         string `json:"remark" example:"VLESS-443"`
	Tag            string `json:"tag" example:"in-443-tcp"`
	Protocol       string `json:"protocol" example:"vless"`
	Port           int    `json:"port" example:"443"`
	Enable         bool   `json:"enable" example:"true"`
	TlsFlowCapable bool   `json:"tlsFlowCapable" example:"true"`
	SsMethod       string `json:"ssMethod"`
	WgPublicKey    string `json:"wgPublicKey,omitempty"`
	WgMtu          int    `json:"wgMtu,omitempty"`
	WgDns          string `json:"wgDns,omitempty"`
	MtprotoDomain  string `json:"mtprotoDomain,omitempty"`
	// Hosting node; nil for this panel's own inbounds. Lets the clients
	// page map a node filter onto inbound IDs (#4997).
	NodeId *int `json:"nodeId,omitempty"`
	// Share-host resolution inputs, mirroring the subscription's
	// resolveInboundAddress so the clients page renders a node-managed WireGuard
	// Endpoint that points at the node, not the master panel. NodeAddress is the
	// hosting node's externally reachable address (empty for this panel's own
	// inbounds); Listen and ShareAddrStrategy/ShareAddr feed the same
	// node→listen→custom fallback the share/QR links already use.
	NodeAddress       string `json:"nodeAddress,omitempty"`
	Listen            string `json:"listen,omitempty"`
	ShareAddr         string `json:"shareAddr,omitempty"`
	ShareAddrStrategy string `json:"shareAddrStrategy,omitempty"`
}

func (s *InboundService) GetInboundOptions(userId int) ([]InboundOption, error) {
	db := database.GetDB()
	var rows []struct {
		Id                int    `gorm:"column:id"`
		Remark            string `gorm:"column:remark"`
		Tag               string `gorm:"column:tag"`
		Protocol          string `gorm:"column:protocol"`
		Port              int    `gorm:"column:port"`
		Enable            bool   `gorm:"column:enable"`
		StreamSettings    string `gorm:"column:stream_settings"`
		Settings          string `gorm:"column:settings"`
		Listen            string `gorm:"column:listen"`
		ShareAddr         string `gorm:"column:share_addr"`
		ShareAddrStrategy string `gorm:"column:share_addr_strategy"`
		NodeId            *int   `gorm:"column:node_id"`
		NodeAddress       string `gorm:"column:node_address"`
	}
	err := db.Table("inbounds").
		Select("inbounds.id, inbounds.remark, inbounds.tag, inbounds.protocol, inbounds.port, inbounds.enable, inbounds.stream_settings, inbounds.settings, inbounds.listen, inbounds.share_addr, inbounds.share_addr_strategy, inbounds.node_id, COALESCE(nodes.address, '') AS node_address").
		Joins("LEFT JOIN nodes ON nodes.id = inbounds.node_id").
		Where("inbounds.user_id = ?", userId).
		Order("inbounds.id ASC").
		Scan(&rows).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	out := make([]InboundOption, 0, len(rows))
	for _, r := range rows {
		wgPublicKey, wgMtu, wgDns := inboundWireguardHints(r.Protocol, r.Settings)
		shareAddrStrategy := r.ShareAddrStrategy
		if shareAddrStrategy == "node" {
			shareAddrStrategy = ""
		}
		out = append(out, InboundOption{
			Id:                r.Id,
			Remark:            r.Remark,
			Tag:               r.Tag,
			Protocol:          r.Protocol,
			Port:              r.Port,
			Enable:            r.Enable,
			TlsFlowCapable:    inboundCanEnableTlsFlow(r.Protocol, r.StreamSettings, r.Settings),
			SsMethod:          inboundShadowsocksMethod(r.Protocol, r.Settings),
			WgPublicKey:       wgPublicKey,
			WgMtu:             wgMtu,
			WgDns:             wgDns,
			MtprotoDomain:     inboundMtprotoDomain(r.Protocol, r.Settings),
			NodeId:            r.NodeId,
			NodeAddress:       r.NodeAddress,
			Listen:            r.Listen,
			ShareAddr:         r.ShareAddr,
			ShareAddrStrategy: shareAddrStrategy,
		})
	}
	return out, nil
}

func inboundWireguardHints(protocol string, settings string) (string, int, string) {
	if !clientCarriesWireguardKeys(model.Protocol(protocol)) || strings.TrimSpace(settings) == "" {
		return "", 0, ""
	}
	var parsed struct {
		PublicKey string `json:"publicKey"`
		PubKey    string `json:"pubKey"`
		SecretKey string `json:"secretKey"`
		MTU       int    `json:"mtu"`
		DNS       string `json:"dns"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return "", 0, ""
	}
	publicKey := parsed.PublicKey
	if publicKey == "" {
		publicKey = parsed.PubKey
	}
	if publicKey == "" && parsed.SecretKey != "" {
		if derived, err := wgutil.PublicKeyFromPrivate(parsed.SecretKey); err == nil {
			publicKey = derived
		}
	}
	return publicKey, parsed.MTU, parsed.DNS
}

// inboundMtprotoDomain returns the inbound-level FakeTLS default domain, used by
// the clients UI to seed a new mtproto client's secret with the right fronting
// hostname.
func inboundMtprotoDomain(protocol string, settings string) string {
	if protocol != string(model.MTProto) || strings.TrimSpace(settings) == "" {
		return ""
	}
	var parsed struct {
		FakeTLSDomain string `json:"fakeTlsDomain"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.FakeTLSDomain)
}

// GetAllInbounds retrieves all inbounds with client stats.
func (s *InboundService) GetAllInbounds() ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	// Ordered like GetInbounds: without it the generated config's inbound array
	// follows PostgreSQL's heap order, so an unrelated row update reshuffles an
	// otherwise identical config. F230.
	err := db.Model(model.Inbound{}).Preload("ClientStats").Order("id ASC").Find(&inbounds).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	s.enrichClientStats(db, inbounds)
	return inbounds, nil
}

func (s *InboundService) GetInboundsByTrafficReset(period string) ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Where("traffic_reset = ?", period).Find(&inbounds).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return inbounds, nil
}

func (s *InboundService) GetClients(inbound *model.Inbound) ([]model.Client, error) {
	return ParseInboundSettingsClients(inbound.Settings)
}

// GetClientsBySubId returns the inbound's clients with the given subscription
// id, resolved from the normalized clients tables (the same source the running
// Xray users are built from) instead of parsing the settings JSON blob.
func (s *InboundService) GetClientsBySubId(inboundId int, subId string) ([]model.Client, error) {
	return s.clientService.ListForInboundBySubId(nil, inboundId, subId)
}

func (s *InboundService) GetAllEmails() ([]string, error) {
	db := database.GetDB()
	var emails []string
	query := fmt.Sprintf(
		"SELECT DISTINCT %s %s",
		database.JSONFieldText("client.value", "email"),
		database.JSONClientsFromInbound(),
	)
	if err := db.Raw(query).Scan(&emails).Error; err != nil {
		return nil, err
	}
	return emails, nil
}

// getAllEmailSubIDs returns email→subId. An email seen with two different
// non-empty subIds is locked (mapped to "") so neither identity can claim it.
func (s *InboundService) getAllEmailSubIDs() (map[string]string, error) {
	db := database.GetDB()
	var rows []struct {
		Email string
		SubID string
	}
	query := fmt.Sprintf(
		"SELECT %s AS email, %s AS sub_id %s",
		database.JSONFieldText("client.value", "email"),
		database.JSONFieldText("client.value", "subId"),
		database.JSONClientsFromInbound(),
	)
	if err := db.Raw(query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]string, len(rows))
	for _, r := range rows {
		email := strings.ToLower(r.Email)
		if email == "" {
			continue
		}
		subID := r.SubID
		if existing, ok := result[email]; ok {
			if existing != subID {
				result[email] = ""
			}
			continue
		}
		result[email] = subID
	}
	return result, nil
}

// normalizeStreamSettings clears StreamSettings for protocols that don't use it.
// Which those are is CapStream's answer, not a second list here. Streams keyed on
// "method" — xray-core v26.7.11's preferred alias for "network" — are canonicalized
// to "network", which every panel reader keys on.
func (s *InboundService) normalizeStreamSettings(inbound *model.Inbound) {
	if !core.Can(core.CapStream, core.Facts{Protocol: string(inbound.Protocol)}) {
		inbound.StreamSettings = ""
		return
	}
	inbound.StreamSettings = canonicalizeStreamNetworkKey(inbound.StreamSettings)
}

// canonicalizeStreamNetworkKey rewrites a streamSettings JSON that names its
// transport under "method" to the panel-canonical "network" key. When both
// keys are present, "method" wins — matching xray-core's own precedence.
func canonicalizeStreamNetworkKey(streamSettings string) string {
	if streamSettings == "" {
		return streamSettings
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(streamSettings), &stream); err != nil {
		return streamSettings
	}
	method, ok := stream["method"].(string)
	if !ok || method == "" {
		return streamSettings
	}
	stream["network"] = method
	delete(stream, "method")
	out, err := json.MarshalIndent(stream, "", "  ")
	if err != nil {
		return streamSettings
	}
	return string(out)
}

// finalMaskRealityTcpMasks returns the stream's finalmask.tcp masks when the
// stream uses REALITY security, or nil otherwise. A non-empty result means
// this stream carries the finalmask+REALITY combination that panics
// Xray-core (see https://github.com/XTLS/Xray-core/issues/6453): finalmask
// wraps the connection before REALITY's handshake ever sees it, and
// reality.Server() does an unchecked type assertion assuming a raw
// *net.TCPConn, which panics once finalmask is in front of it.
//
// Only finalmask.tcp matters here — TcpmaskManager (the thing that wraps the
// listener ahead of REALITY's handshake, in xray-core's own
// transport/internet/memory_settings.go) is only constructed when tcp masks
// are present; a finalmask.udp-only config never touches the TCP accept path
// REALITY runs on, so it doesn't reproduce this panic and shouldn't be
// rejected.
func finalMaskRealityTcpMasks(stream map[string]any) []any {
	if stream["security"] != "reality" {
		return nil
	}
	finalmask, ok := stream["finalmask"].(map[string]any)
	if !ok {
		return nil
	}
	tcp, _ := finalmask["tcp"].([]any)
	return tcp
}

// validateFinalMaskRealityCombo rejects finalmask.tcp configured together
// with REALITY security at save time. Upstream has confirmed this
// combination will be documented as unsupported rather than made graceful,
// so the panel must not let it be saved.
func validateFinalMaskRealityCombo(streamSettings string) error {
	if streamSettings == "" {
		return nil
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(streamSettings), &stream); err != nil {
		return nil
	}
	if len(finalMaskRealityTcpMasks(stream)) == 0 {
		return nil
	}
	return common.NewError("Finalmask is not supported with REALITY security — it crashes Xray-core on the first connection (see XTLS/Xray-core#6453). Remove the finalmask configuration or switch security to tls/none.")
}

var xmcProfileUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

// xmcMaskProfilesComplete reports whether an xmc finalmask carries the signed
// Minecraft session profiles xray-core has required since v26.7.28 (#6487).
// The core replaced the old `usernames` string list with `profiles` objects
// and removed the "default to Dream when empty" fallback, so a mask still on
// the legacy shape — or one whose profiles are incomplete — now fails
// conf.XMC.Build() and takes the entire config down with it rather than
// degrading that one inbound.
//
// The texture fields are a signed blob only Mojang's session server can issue
// (resolve the UUID by username, then fetch the profile with unsigned=false),
// so the panel cannot synthesize a valid profile from a legacy username; an
// incomplete mask can only be reported or dropped.
func xmcMaskProfilesComplete(mask map[string]any) bool {
	settings, ok := mask["settings"].(map[string]any)
	if !ok {
		return false
	}
	profiles, _ := settings["profiles"].([]any)
	if len(profiles) == 0 {
		return false
	}
	for _, entry := range profiles {
		profile, ok := entry.(map[string]any)
		if !ok {
			return false
		}
		username, _ := profile["username"].(string)
		if !xmcProfileUsernamePattern.MatchString(username) {
			return false
		}
		id, _ := profile["uuid"].(string)
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
		if value, _ := profile["texturesValue"].(string); value == "" {
			return false
		}
		if signature, _ := profile["texturesSignature"].(string); signature == "" {
			return false
		}
	}
	return true
}

// isIncompleteXmcMask reports whether a finalmask.tcp entry is an xmc mask
// xray-core would refuse to build.
func isIncompleteXmcMask(entry any) bool {
	mask, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	if maskType, _ := mask["type"].(string); maskType != "xmc" {
		return false
	}
	return !xmcMaskProfilesComplete(mask)
}

// incompleteXmcMaskCount counts the stream's xmc finalmask entries that
// xray-core would refuse to build.
func incompleteXmcMaskCount(stream map[string]any) int {
	finalmask, ok := stream["finalmask"].(map[string]any)
	if !ok {
		return 0
	}
	tcp, _ := finalmask["tcp"].([]any)
	count := 0
	for _, entry := range tcp {
		if isIncompleteXmcMask(entry) {
			count++
		}
	}
	return count
}

// stripIncompleteXmcMasks removes every xmc finalmask entry xray-core would
// refuse to build, returning how many were dropped, and clears the finalmask
// object once nothing is left in it.
//
// AddInbound and UpdateInbound reject an incomplete mask at save time, but a
// row that never went through those paths — an upgrade from a panel predating
// v26.7.28, node sync, a restored backup, a direct DB edit — would otherwise
// fail the whole config build and keep every other inbound offline too.
// Dropping only the offending mask degrades that one inbound instead, which
// the accompanying warning tells the admin to reconfigure.
func stripIncompleteXmcMasks(stream map[string]any) int {
	finalmask, ok := stream["finalmask"].(map[string]any)
	if !ok {
		return 0
	}
	tcp, _ := finalmask["tcp"].([]any)
	if len(tcp) == 0 {
		return 0
	}
	kept := make([]any, 0, len(tcp))
	dropped := 0
	for _, entry := range tcp {
		if isIncompleteXmcMask(entry) {
			dropped++
			continue
		}
		kept = append(kept, entry)
	}
	if dropped == 0 {
		return 0
	}
	if len(kept) == 0 {
		delete(finalmask, "tcp")
	} else {
		finalmask["tcp"] = kept
	}
	if len(finalmask) == 0 {
		delete(stream, "finalmask")
	}
	return dropped
}

// dropEmptyRandPackets removes the leftover empty "packet" from finalmask
// items that also carry a rand, and reports how many it cleared.
//
// xray-core treats even an empty array as a packet, and every item kind is
// exclusive: noise refuses "len(item.Packet) > 0 && item.Rand.To > 0" and
// header-custom refuses "exactly one item kind must be set". Either error
// fails the whole config build, so one such item keeps every inbound offline.
// The panel's mask editor wrote that pair whenever an item was switched to the
// rand-driven array kind, so stored rows carry it; clearing an empty packet
// changes nothing about the mask the admin configured.
func dropEmptyRandPackets(node any) int {
	switch value := node.(type) {
	case map[string]any:
		cleared := 0
		if packet, ok := value["packet"].([]any); ok && len(packet) == 0 && randIsSet(value["rand"]) {
			delete(value, "packet")
			cleared++
		}
		for _, child := range value {
			cleared += dropEmptyRandPackets(child)
		}
		return cleared
	case []any:
		cleared := 0
		for _, child := range value {
			cleared += dropEmptyRandPackets(child)
		}
		return cleared
	default:
		return 0
	}
}

// randIsSet reports whether a finalmask item's rand selects a random packet.
// It is a number on header-custom items and a dash-range string on noise ones.
func randIsSet(value any) bool {
	switch rand := value.(type) {
	case float64:
		return rand > 0
	case string:
		return rand != "" && rand != "0" && rand != "0-0"
	default:
		return false
	}
}

// validateFinalMaskXmcProfiles rejects an xmc finalmask without complete
// profiles at save time, so the admin gets a targeted error instead of a core
// that refuses to start (or, after GetXrayConfig heals it, an inbound quietly
// serving without the obfuscation they configured).
func validateFinalMaskXmcProfiles(streamSettings string) error {
	if streamSettings == "" {
		return nil
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(streamSettings), &stream); err != nil {
		return nil
	}
	if incompleteXmcMaskCount(stream) == 0 {
		return nil
	}
	return common.NewError("XMC finalmask requires at least one complete Minecraft profile — each needs a username (3-16 of A-Z a-z 0-9 _), a UUID, and both texture fields from Mojang's session server (XTLS/Xray-core#6487). Complete the profiles or remove the XMC mask.")
}

// normalizeMtprotoSecret rebuilds every mtproto client's FakeTLS secret so it is
// always valid before the row is persisted, and drops the vestigial inbound-level
// secret and adTag: MTProto is multi-client, so mtg and every share link read
// only the per-client values. Leaving an inbound-level secret behind is what
// produced stale links that failed with "incorrect client random".
func (s *InboundService) normalizeMtprotoSecret(inbound *model.Inbound) {
	if inbound.Protocol != model.MTProto {
		return
	}
	if stripped, ok := model.StripMtprotoInboundSecret(inbound.Settings); ok {
		inbound.Settings = stripped
	}
	if stripped, ok := model.StripMtprotoInboundAdTag(inbound.Settings); ok {
		inbound.Settings = stripped
	}
	if healed, ok := model.HealMtprotoClientSecrets(inbound.Settings); ok {
		inbound.Settings = healed
	}
}

// inboundIsBridged reports whether an inbound Xray does not serve is configured
// to egress through Xray's router (the loopback SOCKS bridge in §xray.go). The
// answer decides whether a change to it must regenerate the config, because the
// bridge lives there and nowhere else.
func inboundIsBridged(inbound *model.Inbound) bool {
	if inbound == nil || cores.ServedByXray(core.Kind(inbound.Protocol)) {
		return false
	}
	var parsed struct {
		RouteThroughXray bool `json:"routeThroughXray"`
	}
	if err := json.Unmarshal([]byte(inbound.Settings), &parsed); err != nil {
		return false
	}
	return parsed.RouteThroughXray
}

func settingsRouteXrayPort(parsed map[string]any) int {
	switch v := parsed["routeXrayPort"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return 0
}

func parseRouteXrayPort(settings string) int {
	if settings == "" {
		return 0
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return 0
	}
	return settingsRouteXrayPort(parsed)
}

// normalizeXrayBridgePort guarantees a routed inbound Xray does not serve carries
// a stable loopback egress port in its settings, so the generated Xray SOCKS
// bridge and the core behind it agree on where that core dials out. mtproto's
// sidecar was the first such core and SSH's in-process server is the second.
// The port is backend-owned: it is
// allocated once when routing is first enabled and preserved across edits
// (carried over from oldSettings, which wins over any value the client echoed
// back). When routing is off it — together with the now-inert outbound
// selection — is stripped so a disabled bridge leaves nothing stale behind.
//
// It returns an error when an egress port cannot be allocated or persisted, so
// the caller refuses the save rather than storing a routed-but-portless inbound,
// which would otherwise route no traffic and have its mtg metrics skipped (see
// the mtproto core's tag rollup) — silently losing its accounting.
// The bridge is a loopback listener, so a handful of tries is generous: it only
// re-rolls when the OS hands back a port the panel is already generating.
const mtprotoPortAllocationAttempts = 8

/*
loopbackPortIsClaimed reports whether another inbound would bind this port on
127.0.0.1, which is where the mtproto bridge listens.

The same three conjuncts checkPortConflict uses -- listen overlap, TCP, and the
local panel only -- because the bridge is exactly an inbound of that shape. A
node's inbounds are excluded: they run on their own box with their own core.
*/
func (s *InboundService) loopbackPortIsClaimed(port, ignoreID int) bool {
	if port <= 0 {
		return true
	}
	db := database.GetDB()
	if db == nil {
		// No database is not a claim, and it is asked FIRST: reservedAPIPort
		// reads a setting, so it needs one too. This normalize step also serves
		// callers with nothing behind them, and refusing every port there would
		// fail the allocation loop rather than protect anything.
		return false
	}
	if port == reservedAPIPort() {
		return true
	}
	// The SSH exits own a band of loopback ports that no inbound row shows, and
	// the ephemeral range the allocator draws from covers it. checkPortConflict
	// already reserves the band against an operator's own inbound; this is the
	// same reservation for a port the panel hands itself.
	if core.IsExitSocksPort(port) {
		return true
	}
	var rows []*model.Inbound
	q := db.Model(model.Inbound{}).Where("port = ? AND node_id IS NULL", port)
	if ignoreID > 0 {
		q = q.Where("id != ?", ignoreID)
	}
	if err := q.Find(&rows).Error; err != nil {
		/*
			Fails OPEN, like the WireGuard host check beside it. Answering
			"claimed" to a question that could not be asked burns every allocation
			attempt and turns an unreadable table into a refused save; answering
			"free" leaves exactly the behaviour that shipped before this check,
			and injectXrayBridge still catches the collision at generation time.
		*/
		logger.Warning("mtproto: cannot check whether the bridge port is claimed, allowing it:", err)
		return false
	}
	for _, row := range rows {
		if !listenOverlaps(row.Listen, "127.0.0.1") {
			continue
		}
		if inboundTransports(row.Protocol, row.StreamSettings, row.Settings)&transportTCP == 0 {
			continue
		}
		return true
	}
	return false
}

func (s *InboundService) normalizeXrayBridgePort(inbound *model.Inbound, oldSettings string) error {
	if cores.ServedByXray(core.Kind(inbound.Protocol)) {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(inbound.Settings), &parsed); err != nil || parsed == nil {
		return nil
	}
	routed, _ := parsed["routeThroughXray"].(bool)
	if !routed {
		_, hadPort := parsed["routeXrayPort"]
		_, hadTag := parsed["outboundTag"]
		if !hadPort && !hadTag {
			return nil
		}
		delete(parsed, "routeXrayPort")
		delete(parsed, "outboundTag")
		if bs, err := json.MarshalIndent(parsed, "", "  "); err == nil {
			inbound.Settings = string(bs)
		} else {
			logger.Warning("mtproto: failed to marshal settings after disabling routing:", err)
		}
		return nil
	}

	/*
		Prefer the already-stored port (carried across edits), then any value the
		client sent, then allocate a fresh one -- and check every one of them
		against the ports this panel already claims.

		None of the three was checked. FreeLocalPort binds 127.0.0.1:0 and closes
		it, which says the OS had that port free for an instant and nothing about
		what the panel is about to generate; an API caller could send any number
		at all. The bridge then landed on a port a real inbound holds, and
		injectXrayBridge -- which does check -- skipped the bridge, so the
		inbound saved clean, displayed as routed, and carried no Telegram traffic
		with only a log line to say why.

		A collision is repaired rather than refused: the port is an implementation
		detail the operator never chose, so refusing their save over it would be
		the panel blaming them for its own allocation. F035.
	*/
	port := parseRouteXrayPort(oldSettings)
	if port > 0 && s.loopbackPortIsClaimed(port, inbound.Id) {
		logger.Warning("mtproto: the stored Xray bridge port", port, "for inbound [", inbound.Tag,
			"] is now claimed by another inbound; allocating a new one")
		port = 0
	}
	if port <= 0 {
		if sent := settingsRouteXrayPort(parsed); sent > 0 && !s.loopbackPortIsClaimed(sent, inbound.Id) {
			port = sent
		}
	}
	for range mtprotoPortAllocationAttempts {
		if port > 0 {
			break
		}
		allocated, err := mtproto.FreeLocalPort()
		if err != nil {
			return common.NewError("mtproto: could not allocate an Xray egress port:", err)
		}
		if !s.loopbackPortIsClaimed(allocated, inbound.Id) {
			port = allocated
		}
	}
	if port <= 0 {
		return common.NewError("mtproto: could not find a free Xray egress port after",
			mtprotoPortAllocationAttempts, "attempts")
	}
	if settingsRouteXrayPort(parsed) == port {
		return nil
	}
	parsed["routeXrayPort"] = port
	bs, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return common.NewError("mtproto: could not persist the Xray egress port:", err)
	}
	inbound.Settings = string(bs)
	return nil
}

// AddInbound creates a new inbound configuration.
// It validates port uniqueness, client email uniqueness, and required fields,
// then saves the inbound to the database and optionally adds it to the running Xray instance.
// Returns the created inbound, whether Xray needs restart, a post-commit apply
// warning for a core the panel converges directly, and any error.
// RefuseUnrunnableCore holds the API path to the form's gate: an inbound for a
// core this host cannot run is refused with the core's own reason.
func RefuseUnrunnableCore(inbound *model.Inbound) error {
	if inbound.NodeID != nil {
		return nil
	}
	if err := cores.PreflightFor(context.Background(), core.Kind(inbound.Protocol)); err != nil {
		return common.NewErrorf("this host cannot run a %s inbound: %v", inbound.Protocol, err)
	}
	if cores.OnePerNode(core.Kind(inbound.Protocol)) {
		var existing int64
		if err := database.GetDB().Model(&model.Inbound{}).Where("protocol = ? AND node_id IS NULL AND id <> ?", inbound.Protocol, inbound.Id).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return common.NewErrorf("this host already runs a %s inbound, and that daemon serves the whole host; edit the existing one", inbound.Protocol)
		}
	}
	return nil
}

func (s *InboundService) AddInbound(inbound *model.Inbound) (*model.Inbound, bool, string, error) {
	inbound.Id = 0
	var applyWarn string
	// Attaching an egress is its own operation, with its own refusals; letting a
	// create carry the column would be a way past every one of them.
	inbound.EgressID = nil
	inbound.TrafficResetDay = normalizeTrafficResetDay(inbound.TrafficResetDay)
	// Normalize streamSettings based on protocol
	s.normalizeStreamSettings(inbound)
	if err := validateFinalMaskRealityCombo(inbound.StreamSettings); err != nil {
		return inbound, false, "", err
	}
	if err := validateFinalMaskXmcProfiles(inbound.StreamSettings); err != nil {
		return inbound, false, "", err
	}
	s.normalizeMtprotoSecret(inbound)
	if err := s.normalizeXrayBridgePort(inbound, ""); err != nil {
		return inbound, false, "", err
	}
	inbound.SubSortIndex = normalizeSubSortIndex(inbound.SubSortIndex)
	if err := normalizeInboundShareAddressStrict(inbound); err != nil {
		return inbound, false, "", err
	}

	if err := mintInstanceForKind(inbound, nil); err != nil {
		return inbound, false, "", err
	}
	// Settings the core that owns this kind would refuse. Asked of the core, not
	// decided here: what makes a configuration invalid is protocol knowledge.
	if err := validateInstanceForKind(inbound); err != nil {
		return inbound, false, "", err
	}

	// A bind nothing can serve: a listen that is not an address, or the port 0
	// that binds to whatever the OS hands out. F009 / F039.
	if err := validateInboundBind(inbound); err != nil {
		return inbound, false, "", err
	}

	// Credentials the core would parse and never check make this an open proxy.
	if err := refuseUnenforcedAccounts(inbound); err != nil {
		return inbound, false, "", err
	}

	inboundPortClaimMu.Lock()
	defer inboundPortClaimMu.Unlock()

	conflict, err := s.checkPortConflict(inbound, 0)
	if err != nil {
		return inbound, false, "", err
	}
	if conflict != nil {
		return inbound, false, "", common.NewError(conflict.String())
	}
	if err := s.checkIngressPoolConflict(context.Background(), inbound, 0); err != nil {
		return inbound, false, "", err
	}
	if err := s.checkWireguardAddressConflict(inbound, 0); err != nil {
		return inbound, false, "", err
	}
	// Create only. An EDIT that does not move the prefix must stay possible on an
	// inbound that already overlaps, or the operator cannot even rename it.
	if err := s.checkWireguardHostAddressConflict(inbound); err != nil {
		return inbound, false, "", err
	}

	inbound.Tag, err = s.resolveInboundTag(inbound, 0)
	if err != nil {
		return inbound, false, "", err
	}

	// xray would expect Vision while the generated link, which tlsFlowAllowed
	// strips, never sends it, so the client connects to nothing.
	if stripped, changed := stripVisionFlowForIneligibleInbound(inbound.Settings, inbound.StreamSettings, inbound.Protocol); changed {
		inbound.Settings = stripped
		logger.Warning("inbound on port", inbound.Port, "cannot carry XTLS Vision; cleared the flow on its client(s)")
	}

	clients, err := s.GetClients(inbound)
	if err != nil {
		return inbound, false, "", err
	}
	existEmail, err := s.clientService.checkEmailsExistForClients(s, clients, nil)
	if err != nil {
		return inbound, false, "", err
	}
	if existEmail != "" {
		return inbound, false, "", common.NewError("Duplicate email:", existEmail)
	}

	// Ensure created_at and updated_at on clients in settings
	if len(clients) > 0 {
		var settings map[string]any
		if err2 := json.Unmarshal([]byte(inbound.Settings), &settings); err2 == nil && settings != nil {
			now := time.Now().Unix() * 1000
			updatedClients := make([]model.Client, 0, len(clients))
			for _, c := range clients {
				if c.CreatedAt == 0 {
					c.CreatedAt = now
				}
				c.UpdatedAt = now
				updatedClients = append(updatedClients, c)
			}
			settings["clients"] = updatedClients
			if bs, err3 := json.MarshalIndent(settings, "", "  "); err3 == nil {
				inbound.Settings = string(bs)
			} else {
				logger.Debug("Unable to marshal inbound settings with timestamps:", err3)
			}
		} else if err2 != nil {
			logger.Debug("Unable to parse inbound settings for timestamps:", err2)
		}
	}

	// Defensively fix any Shadowsocks-2022 client PSK whose length doesn't match
	// the inbound method (e.g. an API caller supplied a wrong-size key).
	if err := refuseShortShadowsocksServerKey(inbound.Settings); err != nil {
		return inbound, false, "", err
	}
	if normalized, changed := normalizeShadowsocksClientKeys(inbound.Settings); changed {
		inbound.Settings = normalized
	}

	// Refused with the kind's own wording, asked of its core.
	identityField := identityFieldFor(inbound.Protocol)
	for i := range clients {
		client := clients[i]
		if err := validateClientForKind(inbound.Protocol, inbound.Settings, &client); err != nil {
			return inbound, false, "", err
		}
		// Against the earlier members only: the core keeps one user per identity,
		// so a duplicate inside one create is already two clients sharing an account.
		if other, field := identityCollision(inbound.Protocol,
			clientIdentityValue(&clients[i], identityField), clients[:i], -1); other != "" {
			return inbound, false, "", common.NewError(string(inbound.Protocol)+": "+field+" already used by client:", other)
		}
	}

	if err := validateInboundForXray(inbound); err != nil {
		return inbound, false, "", err
	}

	db := database.GetDB()
	needRestart := false
	var postCommitApply func()
	err = db.Transaction(func(tx *gorm.DB) error {
		markDirty := false
		if err := tx.Omit("ClientStats").Save(inbound).Error; err != nil {
			return err
		}
		// Emails seeded here (import's ClientStats, e.g. the controller's forced
		// Enable=true on every imported stat row) are authoritative for this call
		// and must not be clobbered by the AddClientStat loop below, which derives
		// its enable/total/expiry/reset from Settings.clients[] instead — a second,
		// possibly-stale source for the same columns on a plain (non-import) create.
		statEmails := make(map[string]bool, len(inbound.ClientStats))
		for i := range inbound.ClientStats {
			if inbound.ClientStats[i].Email == "" {
				continue
			}
			statEmails[inbound.ClientStats[i].Email] = true
			inbound.ClientStats[i].Id = 0
			inbound.ClientStats[i].InboundId = inbound.Id
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "email"}},
				DoNothing: true,
			}).Create(&inbound.ClientStats[i]).Error; err != nil {
				return err
			}
		}
		for _, client := range clients {
			if statEmails[client.Email] {
				continue
			}
			if err := s.AddClientStat(tx, inbound.Id, &client); err != nil {
				return err
			}
		}
		if err := s.clientService.SyncInbound(tx, inbound.Id, clients); err != nil {
			return err
		}
		if _, err := database.CreateHostsFromExternalProxy(tx, inbound.Id, inbound.StreamSettings); err != nil {
			return err
		}
		if inbound.NodeID != nil {
			nodeID := *inbound.NodeID
			if err := (&NodeService{}).EnsureInboundTagAllowedTx(tx, nodeID, inbound.Tag); err != nil {
				return err
			}
		}
		if inbound.Enable {
			if inbound.NodeID != nil {
				markDirty = true
			} else {
				rt, push, _, perr := s.nodePushPlan(inbound)
				if perr != nil {
					return perr
				}
				if push {
					payload := inbound
					pushable := true
					if inbound.Protocol == model.MTProto {
						if built, bErr := s.buildInboundForLocalRuntime(tx, inbound); bErr == nil {
							payload = built
						} else {
							logger.Debug("Unable to prepare runtime inbound config:", bErr)
							pushable = false
							applyWarn = bErr.Error()
						}
					}
					servedByXray := cores.ServedByXray(core.Kind(inbound.Protocol))
					if pushable {
						postCommitApply = func() {
							if err1 := rt.AddInbound(context.Background(), payload); err1 == nil {
								logger.Debug("New inbound added on", rt.Name(), ":", inbound.Tag)
								return
							} else if servedByXray {
								logger.Debug("Unable to add inbound on", rt.Name(), ":", err1)
								needRestart = true
							} else {
								// For a core the panel converges directly the push IS the apply:
								// no restart repairs it, so the operator has to be told.
								logger.Warning("Unable to add inbound on", rt.Name(), ":", err1)
								applyWarn = err1.Error()
							}
						}
					}
				}
			}
		}
		if markDirty && inbound.NodeID != nil {
			return (&NodeService{}).MarkNodeDirtyTx(tx, *inbound.NodeID)
		}
		return nil
	})
	if err != nil {
		return inbound, false, "", err
	}
	if postCommitApply != nil {
		postCommitApply()
	}
	ensureL3HostForwarding(inbound)

	// A routed mtproto inbound is not an Xray inbound itself, so the runtime
	// push above only (re)starts the mtg sidecar. The egress SOCKS bridge lives
	// in the generated config, so force a regen to wire it in.
	if inboundIsBridged(inbound) {
		needRestart = true
	}

	return inbound, needRestart, applyWarn, err
}

/*
ensureL3HostForwarding converges the host knobs a device-backed inbound needs,
at the save rather than on the next reconcile tick.

The 10s job would get there. An operator would not wait for it: they create the
inbound, add a client and hand out the .conf, and inside that window the tunnel
completes its handshake and drops every packet — which reads as a broken panel,
not as a knob that is a moment late.

Warned and never fatal, exactly as routing's own converge treats it: the inbound
is saved either way, and a host that refuses the knob still has a note saying so.
*/
func ensureL3HostForwarding(inbound *model.Inbound) {
	if inbound == nil || inbound.NodeID != nil {
		return
	}
	if cores.IngressSelectorFor(core.Kind(inbound.Protocol)) != core.IngressDevice {
		return
	}
	if err := (&RoutingService{}).EnsureHostForwarding(context.Background()); err != nil {
		logger.Warning("host forwarding could not be enabled for", inbound.Tag, ":", err)
	}
}

func (s *InboundService) delInboundRow(id int) (*model.Inbound, bool, error) {
	db := database.GetDB()

	needRestart := false
	var postCommitApply func()
	var ib model.Inbound
	loadErr := db.Model(model.Inbound{}).Where("id = ?", id).First(&ib).Error
	if loadErr == nil {
		shouldPushToRuntime := ib.NodeID != nil || ib.Enable
		if shouldPushToRuntime {
			if ib.NodeID != nil {
				rt, push, _, perr := s.nodePushPlan(&ib)
				if perr != nil {
					logger.Warning("DelInbound: node runtime lookup failed, deleting central row anyway:", perr)
				} else if push {
					postCommitApply = func() {
						if err1 := rt.DelInbound(context.Background(), &ib); err1 == nil {
							logger.Debug("Inbound deleted on", rt.Name(), ":", ib.Tag)
						} else {
							logger.Warning("DelInbound on", rt.Name(), "failed after commit:", err1)
						}
					}
				}
			} else {
				rt, push, _, perr := s.nodePushPlan(&ib)
				if perr != nil {
					logger.Warning("DelInbound: runtime lookup failed, deleting central row anyway:", perr)
				} else if push {
					postCommitApply = func() {
						if err1 := rt.DelInbound(context.Background(), &ib); err1 == nil {
							logger.Debug("Inbound deleted on", rt.Name(), ":", ib.Tag)
						} else {
							logger.Warning("DelInbound on", rt.Name(), "failed after commit:", err1)
							needRestart = true
						}
					}
				} else {
					needRestart = true
				}
			}
		} else {
			logger.Debug("DelInbound: skipping runtime push for disabled local inbound id:", id)
		}
	} else if errors.Is(loadErr, gorm.ErrRecordNotFound) {
		return nil, false, common.NewErrorf("inbound %d not found", id)
	} else {
		return nil, false, loadErr
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := s.clientService.DetachInbound(tx, id); err != nil {
			return err
		}
		if err := tx.Delete(model.Inbound{}, id).Error; err != nil {
			return err
		}
		if err := tx.Where("inbound_id = ?", id).Delete(&model.Host{}).Error; err != nil {
			return err
		}
		/*
			The per-inbound rows go with the inbound, or a recycled id adopts them.

			resyncPostgresSequences hands ids back after a delete, so a NEW inbound can
			be created on the id the old one had — and it inherits whatever these two
			tables still hold for that id. Credentials from a deleted inbound, and
			fallbacks pointing at something that no longer exists.
		*/
		if err := tx.Where("inbound_id = ?", id).Delete(&model.ClientCredential{}).Error; err != nil {
			return err
		}
		if err := tx.Where("master_id = ? OR child_id = ?", id, id).Delete(&model.InboundFallback{}).Error; err != nil {
			return err
		}
		if loadErr == nil && ib.NodeID != nil {
			return (&NodeService{}).MarkNodeDirtyTx(tx, *ib.NodeID)
		}
		return nil
	}); err != nil {
		return nil, needRestart, err
	}
	if postCommitApply != nil {
		postCommitApply()
	}
	return &ib, needRestart, nil
}

// repairAfterInboundDeletes runs the routing repairs once for a whole batch:
// one template rewrite for every deleted tag, one prune pass for every id.
func (s *InboundService) repairAfterInboundDeletes(deleted []*model.Inbound) bool {
	if len(deleted) == 0 {
		return false
	}
	needRestart := false
	tags := make([]string, 0, len(deleted))
	ids := make([]int, 0, len(deleted))
	for _, ib := range deleted {
		if ib.Tag != "" {
			tags = append(tags, ib.Tag)
		}
		ids = append(ids, ib.Id)
		// A routed mtproto inbound leaves its egress SOCKS bridge in the
		// config, and only a restart drops it.
		if inboundIsBridged(ib) {
			needRestart = true
		}
	}
	if routingChanged, syncErr := (&XraySettingService{}).RemoveInboundTagsReferences(tags); syncErr != nil {
		logger.Warning("DelInbound: sync routing on inbound delete failed:", syncErr)
	} else if routingChanged {
		needRestart = true
	}
	// Linux re-attaches a detached rule to the next inbound that reuses the
	// id, so the prune (and its converge) is not optional even for one row.
	if pruned, pruneErr := (&RoutingService{}).PruneInbounds(context.Background(), ids); pruneErr != nil {
		logger.Warning("DelInbound: prune routing rules failed:", pruneErr)
	} else if pruned {
		needRestart = true
	}
	return needRestart
}

func (s *InboundService) DelInbound(id int) (bool, error) {
	ib, needRestart, err := s.delInboundRow(id)
	if err != nil {
		return needRestart, err
	}
	if ib != nil && s.repairAfterInboundDeletes([]*model.Inbound{ib}) {
		needRestart = true
	}
	return needRestart, nil
}

type BulkDelInboundResult struct {
	Deleted int                    `json:"deleted"`
	Skipped []BulkDelInboundReport `json:"skipped,omitempty"`
}

type BulkDelInboundReport struct {
	Id     int    `json:"id"`
	Reason string `json:"reason"`
}

// DelInbounds removes every inbound in the list, reusing the single-delete
// path per id. Failures are recorded in Skipped and processing continues for
// the rest; the aggregated needRestart is returned so the caller restarts
// xray at most once.
func (s *InboundService) DelInbounds(ids []int) (BulkDelInboundResult, bool, error) {
	result := BulkDelInboundResult{}
	needRestart := false
	deleted := make([]*model.Inbound, 0, len(ids))
	for _, id := range ids {
		ib, r, err := s.delInboundRow(id)
		if err != nil {
			result.Skipped = append(result.Skipped, BulkDelInboundReport{Id: id, Reason: err.Error()})
			continue
		}
		result.Deleted++
		if r {
			needRestart = true
		}
		if ib != nil {
			deleted = append(deleted, ib)
		}
	}
	if s.repairAfterInboundDeletes(deleted) {
		needRestart = true
	}
	return result, needRestart, nil
}

func (s *InboundService) GetInbound(id int) (*model.Inbound, error) {
	db := database.GetDB()
	inbound := &model.Inbound{}
	err := db.Model(model.Inbound{}).First(inbound, id).Error
	if err != nil {
		return nil, err
	}
	return inbound, nil
}

func (s *InboundService) GetInboundDetail(id int) (*model.Inbound, error) {
	db := database.GetDB()
	inbound := &model.Inbound{}
	err := db.Model(model.Inbound{}).Preload("ClientStats").First(inbound, id).Error
	if err != nil {
		return nil, err
	}
	s.enrichClientStats(db, []*model.Inbound{inbound})
	s.overlayInboundsClientStats(db, []*model.Inbound{inbound})
	return inbound, nil
}

func (s *InboundService) SetInboundEnable(id int, enable bool) (bool, error) {
	inbound, err := s.GetInbound(id)
	if err != nil {
		return false, err
	}
	if inbound.Enable == enable {
		return false, nil
	}
	// Enabling is the moment the row joins the generated config, and it is the
	// one save that does not go through UpdateInbound. A row saved broken while
	// disabled would otherwise reach the core here, with a success toast.
	if enable {
		candidate := *inbound
		candidate.Enable = true
		if err := validateInboundForXray(&candidate); err != nil {
			return false, err
		}
	}

	db := database.GetDB()
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(model.Inbound{}).Where("id = ?", id).
			Update("enable", enable).Error; err != nil {
			return err
		}
		if inbound.NodeID != nil {
			return (&NodeService{}).MarkNodeDirtyTx(tx, *inbound.NodeID)
		}
		return nil
	}); err != nil {
		return false, err
	}
	inbound.Enable = enable

	needRestart := false
	rt, push, _, perr := s.nodePushPlan(inbound)
	if perr != nil {
		return false, perr
	}

	// Remote nodes interpret DelInbound as a real row delete (it hits
	// panel/api/inbounds/del/:id on the remote), so toggling the enable
	// switch on a remote inbound used to wipe the row entirely (#4402).
	// PATCH the remote row via UpdateInbound instead — preserves the
	// settings/client history and just flips the enable flag.
	if inbound.NodeID != nil {
		if push {
			if err := rt.UpdateInbound(context.Background(), inbound, inbound); err != nil {
				logger.Warning("SetInboundEnable: remote UpdateInbound on", rt.Name(), "failed:", err)
			}
		}
		return false, nil
	}

	if inboundIsBridged(inbound) {
		needRestart = true
	}

	if !push {
		return true, nil
	}

	if err := rt.DelInbound(context.Background(), inbound); err != nil &&
		!strings.Contains(err.Error(), "not found") {
		logger.Debug("SetInboundEnable: DelInbound on", rt.Name(), "failed:", err)
		needRestart = true
	}
	if !enable {
		return needRestart, nil
	}

	runtimeInbound, err := s.buildInboundForLocalRuntime(db, inbound)
	if err != nil {
		logger.Debug("SetInboundEnable: build runtime config failed:", err)
		return true, nil
	}
	if err := rt.AddInbound(context.Background(), runtimeInbound); err != nil {
		logger.Debug("SetInboundEnable: AddInbound on", rt.Name(), "failed:", err)
		needRestart = true
	}
	return needRestart, nil
}

type BulkInboundEnableResult struct {
	Changed int                    `json:"changed"`
	Skipped []BulkDelInboundReport `json:"skipped,omitempty"`
}

// SetInboundsEnable flips many inbounds, naming the ones already in that state.
func (s *InboundService) SetInboundsEnable(ids []int, enable bool) (BulkInboundEnableResult, bool, error) {
	result := BulkInboundEnableResult{}
	needRestart := false
	state := map[bool]string{true: "already enabled", false: "already disabled"}
	for _, id := range ids {
		ib, err := s.GetInbound(id)
		if err != nil {
			result.Skipped = append(result.Skipped, BulkDelInboundReport{Id: id, Reason: err.Error()})
			continue
		}
		if ib.Enable == enable {
			result.Skipped = append(result.Skipped, BulkDelInboundReport{Id: id, Reason: state[enable]})
			continue
		}
		nr, err := s.SetInboundEnable(id, enable)
		if err != nil {
			result.Skipped = append(result.Skipped, BulkDelInboundReport{Id: id, Reason: err.Error()})
			continue
		}
		result.Changed++
		if nr {
			needRestart = true
		}
	}
	return result, needRestart, nil
}

func (s *InboundService) UpdateInbound(inbound *model.Inbound) (*model.Inbound, bool, string, error) {
	inbound.TrafficResetDay = normalizeTrafficResetDay(inbound.TrafficResetDay)
	// Normalize streamSettings based on protocol
	s.normalizeStreamSettings(inbound)
	if err := validateFinalMaskRealityCombo(inbound.StreamSettings); err != nil {
		return inbound, false, "", err
	}
	if err := validateFinalMaskXmcProfiles(inbound.StreamSettings); err != nil {
		return inbound, false, "", err
	}
	s.normalizeMtprotoSecret(inbound)
	inbound.SubSortIndex = normalizeSubSortIndex(inbound.SubSortIndex)

	oldInbound, err := s.GetInbound(inbound.Id)
	if err != nil {
		return inbound, false, "", err
	}
	// Restore the stored NodeID before the port-conflict check so a node inbound
	// stays scoped to its own node (the payload's nodeId is unreliable, often absent).
	inbound.NodeID = oldInbound.NodeID

	// Settings the core that owns this kind would refuse -- judged against what
	// this edit replaces, so a row that already violates stays editable. Q1.
	if err := mintInstanceForKind(inbound, oldInbound); err != nil {
		return inbound, false, "", err
	}
	if err := refuseOnlyIfWorsened("configuration", oldInbound, inbound, validateInstanceForKind); err != nil {
		return inbound, false, "", err
	}

	/*
		Every client this update introduces or changes is asked about; a protocol
		change asks about all of them, because the kind is what decides whether a
		credential is enough.

		AddInbound has always run this loop and this path never did, so a switch
		between two kinds the same core does not serve -- wireguard to mtproto,
		say -- rewrote the row with credentials the new kind has no field for.
		validateInboundForXray covers the xray-served kinds only, and returns nil
		for a disabled or node-hosted inbound besides.

		A client that arrives unchanged is skipped on purpose: validating those
		too would refuse a remark edit because of a client an older build let in.
		F030 / F237.
	*/
	if err := s.validateUpdatedClients(inbound, oldInbound); err != nil {
		return inbound, false, "", err
	}

	// A bind nothing can serve: a listen that is not an address, or the port 0
	// that binds to whatever the OS hands out. F009 / F039.
	if err := validateInboundBind(inbound); err != nil {
		return inbound, false, "", err
	}

	/*
		Credentials the core would parse and never check make this an open proxy.

		Refused outright, NOT judged against what the edit replaces: forcing the
		fix on operator contact is this guard's whole repair path, which is why
		nothing auto-repairs it. Deliberately outside Q1's rule, and pinned by
		TestUpdateCannotCarryAnInboundThroughInTheBrokenState.
	*/
	if err := refuseUnenforcedAccounts(inbound); err != nil {
		return inbound, false, "", err
	}

	/*
		The host's own addresses, which create has always asked about and edit
		never did: a device prefix moved onto the LAN, docker's bridge or the
		panel's management network captures that traffic just as surely when it
		arrives by an edit.

		Judged against what the edit replaces, so an inbound that already overlaps
		can still be renamed or turned off. F067 / Q1.
	*/
	if err := refuseOnlyIfWorsened("subnet the host already holds", oldInbound, inbound,
		s.checkWireguardHostAddressConflict); err != nil {
		return inbound, false, "", err
	}

	inboundPortClaimMu.Lock()
	defer inboundPortClaimMu.Unlock()

	conflict, err := s.checkPortConflict(inbound, inbound.Id)
	if err != nil {
		return inbound, false, "", err
	}
	if conflict != nil {
		return inbound, false, "", common.NewError(conflict.String())
	}
	if err := s.checkIngressPoolConflict(context.Background(), inbound, inbound.Id); err != nil {
		return inbound, false, "", err
	}
	if err := s.checkWireguardAddressConflict(inbound, inbound.Id); err != nil {
		return inbound, false, "", err
	}
	if err := checkWireguardAddressCoverage(oldInbound, inbound); err != nil {
		return inbound, false, "", err
	}
	if err := s.checkFallbacksSurviveEdit(inbound); err != nil {
		return inbound, false, "", err
	}

	// Capture the pre-edit protocol and routing state before oldInbound is
	// overwritten with the new values further down, then ensure a routed
	// inbound keeps a stable egress port (reusing the one already stored).
	oldProtocol := oldInbound.Protocol
	oldBridged := inboundIsBridged(oldInbound)
	if err := s.normalizeXrayBridgePort(inbound, oldInbound.Settings); err != nil {
		return inbound, false, "", err
	}

	tag := oldInbound.Tag
	oldBits := inboundTransports(oldInbound.Protocol, oldInbound.StreamSettings, oldInbound.Settings)
	oldTagWasAuto := isAutoGeneratedTag(tag, oldInbound.Port, oldInbound.NodeID, oldBits)

	needRestart := false
	var applyWarn string
	var postCommitApply func()

	txErr := runSerializedTx(func(tx *gorm.DB) error {
		if err := s.updateClientTraffics(tx, oldInbound, inbound); err != nil {
			return err
		}

		// Ensure created_at and updated_at exist in inbound.Settings clients
		{
			var oldSettings map[string]any
			_ = json.Unmarshal([]byte(oldInbound.Settings), &oldSettings)
			emailToCreated := map[string]int64{}
			emailToUpdated := map[string]int64{}
			if oldSettings != nil {
				if oc, ok := oldSettings["clients"].([]any); ok {
					for _, it := range oc {
						if m, ok2 := it.(map[string]any); ok2 {
							if email, ok3 := m["email"].(string); ok3 {
								switch v := m["created_at"].(type) {
								case float64:
									emailToCreated[email] = int64(v)
								case int64:
									emailToCreated[email] = v
								}
								switch v := m["updated_at"].(type) {
								case float64:
									emailToUpdated[email] = int64(v)
								case int64:
									emailToUpdated[email] = v
								}
							}
						}
					}
				}
			}
			var newSettings map[string]any
			if err2 := json.Unmarshal([]byte(inbound.Settings), &newSettings); err2 == nil && newSettings != nil {
				now := time.Now().Unix() * 1000
				if nSlice, ok := newSettings["clients"].([]any); ok {
					for i := range nSlice {
						if m, ok2 := nSlice[i].(map[string]any); ok2 {
							email, _ := m["email"].(string)
							if _, ok3 := m["created_at"]; !ok3 {
								if v, ok4 := emailToCreated[email]; ok4 && v > 0 {
									m["created_at"] = v
								} else {
									m["created_at"] = now
								}
							}
							// Preserve client's updated_at if present; do not bump on parent inbound update
							if _, hasUpdated := m["updated_at"]; !hasUpdated {
								if v, ok4 := emailToUpdated[email]; ok4 && v > 0 {
									m["updated_at"] = v
								}
							}
							nSlice[i] = m
						}
					}
					newSettings["clients"] = nSlice
					if bs, err3 := json.MarshalIndent(newSettings, "", "  "); err3 == nil {
						inbound.Settings = string(bs)
					}
				}
			}
		}

		// The server key is refused rather than re-minted: rotating it would
		// invalidate every link already handed out. F329.
		if err := refuseShortShadowsocksServerKey(inbound.Settings); err != nil {
			return err
		}
		// A Shadowsocks-2022 method change resizes the key, but existing client PSKs
		// keep their old length and would be rejected by xray. Regenerate mismatched
		// client keys so the inbound stays connectable.
		if normalized, changed := normalizeShadowsocksClientKeys(inbound.Settings); changed {
			inbound.Settings = normalized
			logger.Warning("Shadowsocks inbound", inbound.Id, "method change resized keys; regenerated mismatched client PSK(s)")
		}

		// Re-gate Vision flow now that the new stream/encryption is known: if this
		// VLESS inbound just became flow-eligible (e.g. vlessenc was enabled on an
		// XHTTP inbound), restore Vision for clients whose intended flow is Vision
		// but was stripped while the inbound was ineligible.
		if restored, changed := s.restoreVisionFlowForEligibleInbound(tx, inbound.Settings, inbound.StreamSettings, inbound.Protocol); changed {
			inbound.Settings = restored
		}
		// The other direction: an edit that moves a flow-bearing inbound onto a
		// transport that cannot carry Vision must clear it, or the link stops working.
		if stripped, changed := stripVisionFlowForIneligibleInbound(inbound.Settings, inbound.StreamSettings, inbound.Protocol); changed {
			inbound.Settings = stripped
			logger.Warning("inbound", inbound.Id, "no longer supports XTLS Vision; cleared the flow on its client(s)")
		}

		oldInbound.Total = inbound.Total
		oldInbound.Remark = inbound.Remark
		oldInbound.SubSortIndex = inbound.SubSortIndex
		oldInbound.Enable = inbound.Enable
		oldInbound.ExpiryTime = inbound.ExpiryTime
		oldInbound.TrafficReset = inbound.TrafficReset
		oldInbound.TrafficResetDay = inbound.TrafficResetDay
		oldInbound.Listen = inbound.Listen
		oldInbound.Port = inbound.Port
		oldInbound.Protocol = inbound.Protocol
		// A protocol with no ingress device cannot be selected on, so keeping the
		// reference would pin the egress against every delete and disable forever.
		if !egressIngressSelectable(oldInbound) {
			oldInbound.EgressID = nil
		}
		oldInbound.Settings = inbound.Settings
		oldInbound.StreamSettings = inbound.StreamSettings
		oldInbound.Sniffing = inbound.Sniffing
		// After the normalisations above, which are what a method change relies on
		// to keep the row buildable, and before anything is written.
		if err := validateInboundForXray(oldInbound); err != nil {
			return err
		}
		if strings.TrimSpace(inbound.ShareAddrStrategy) == "" {
			normalizeInboundShareAddress(oldInbound)
			inbound.ShareAddrStrategy = oldInbound.ShareAddrStrategy
			inbound.ShareAddr = oldInbound.ShareAddr
		} else {
			if err := normalizeInboundShareAddressStrict(inbound); err != nil {
				return err
			}
			oldInbound.ShareAddrStrategy = inbound.ShareAddrStrategy
			oldInbound.ShareAddr = inbound.ShareAddr
		}
		if oldTagWasAuto && inbound.Tag == tag {
			inbound.Tag = ""
		}
		resolvedTag, err := s.resolveInboundTag(inbound, inbound.Id)
		if err != nil {
			return err
		}
		oldInbound.Tag = resolvedTag
		inbound.Tag = oldInbound.Tag

		if oldInbound.NodeID == nil {
			rt, push, _, perr := s.nodePushPlan(oldInbound)
			if perr != nil {
				return perr
			}
			if !push {
				needRestart = true
			} else if !cores.ServedByXray(core.Kind(oldProtocol)) || !cores.ServedByXray(core.Kind(oldInbound.Protocol)) {
				oldSnapshot := *oldInbound
				oldSnapshot.Tag = tag
				oldSnapshot.Protocol = oldProtocol
				payload := oldInbound
				pushable := true
				if inbound.Enable {
					if built, err2 := s.buildInboundForLocalRuntime(tx, oldInbound); err2 == nil {
						payload = built
					} else {
						logger.Debug("Unable to prepare runtime inbound config:", err2)
						pushable = false
					}
				}
				newProtocolServedByXray := cores.ServedByXray(core.Kind(oldInbound.Protocol))
				if !pushable {
					applyWarn = "the runtime configuration for this inbound could not be prepared"
				}
				if pushable {
					postCommitApply = func() {
						if err2 := rt.UpdateInbound(context.Background(), &oldSnapshot, payload); err2 == nil {
							logger.Debug("Updated inbound applied on", rt.Name(), ":", oldInbound.Tag)
						} else if newProtocolServedByXray {
							logger.Debug("Unable to update inbound on", rt.Name(), ":", err2)
							needRestart = true
						} else {
							// The push IS the apply for this core, so nothing later repairs it.
							logger.Warning("Unable to update inbound on", rt.Name(), ":", err2)
							applyWarn = err2.Error()
						}
					}
				}
			} else {
				oldSnapshot := *oldInbound
				oldSnapshot.Tag = tag
				var runtimeInbound *model.Inbound
				if inbound.Enable {
					var err2 error
					runtimeInbound, err2 = s.buildInboundForLocalRuntime(tx, oldInbound)
					if err2 != nil {
						logger.Debug("Unable to prepare runtime inbound config:", err2)
						needRestart = true
					}
				}
				/*
					The same converge-in-place call the branch above already uses.
					It used to be an unconditional delete followed by an add, which
					closes the listener and every session on it — for any edit at
					all, including one that changed only the remark.

					Nothing here decides which edits are cosmetic. UpdateInbound
					asks the core: PlanChange answers whether a rename would strand
					the old listener, and Xray's own hot diff narrows the rest down
					to the users that actually changed. A rule about which fields
					matter would be a second opinion for the core to disagree with.
				*/
				postCommitApply = func() {
					if runtimeInbound == nil {
						// Also reached for an ENABLED inbound whose runtime config failed
						// to build, so the log does not claim the inbound was disabled.
						if err2 := rt.DelInbound(context.Background(), &oldSnapshot); err2 == nil {
							logger.Debug("Inbound removed on", rt.Name(), ":", tag)
						} else {
							logger.Debug("Unable to remove inbound on", rt.Name(), ":", err2)
							needRestart = true
						}
						return
					}
					if err2 := rt.UpdateInbound(context.Background(), &oldSnapshot, runtimeInbound); err2 == nil {
						logger.Debug("Updated inbound applied on", rt.Name(), ":", oldInbound.Tag)
					} else {
						logger.Debug("Unable to update inbound on", rt.Name(), ":", err2)
						needRestart = true
					}
				}
			}
		} else {
			nodeID := *oldInbound.NodeID
			if err := (&NodeService{}).EnsureInboundTagAllowedTx(tx, nodeID, oldInbound.Tag); err != nil {
				return err
			}
		}

		if err := tx.Save(oldInbound).Error; err != nil {
			return err
		}
		newClients, gcErr := s.GetClients(oldInbound)
		if gcErr != nil {
			return gcErr
		}
		if err := s.clientService.SyncInbound(tx, oldInbound.Id, newClients); err != nil {
			return err
		}
		if oldInbound.NodeID != nil {
			if err := (&NodeService{}).MarkNodeDirtyTx(tx, *oldInbound.NodeID); err != nil {
				return err
			}
		}
		// (Re)generate the Xray config whenever routing was or is now enabled, so
		// the egress SOCKS bridge is added, moved, or dropped to match the new
		// settings.
		if inboundIsBridged(inbound) || oldBridged {
			needRestart = true
		}
		return nil
	})
	if txErr != nil {
		return inbound, false, "", txErr
	}
	if postCommitApply != nil {
		postCommitApply()
	}
	// An edit can be what makes this host forward: a protocol changed to a
	// device-backed kind, or an inbound enabled again after being switched off.
	ensureL3HostForwarding(inbound)
	// After the rename is committed, point any routing rules / loopback outbounds
	// in xrayTemplateConfig at the new tag (oldInbound.Tag now holds the resolved
	// new tag; tag holds the pre-edit one). Done post-commit so a sync failure
	// can't roll back the inbound edit.
	if tag != oldInbound.Tag {
		if routingChanged, syncErr := (&XraySettingService{}).PropagateInboundTagRename(tag, oldInbound.Tag); syncErr != nil {
			logger.Warning("UpdateInbound: sync routing on tag rename failed:", syncErr)
		} else if routingChanged {
			needRestart = true
		}
	}
	return inbound, needRestart, applyWarn, nil
}

// A node mirrors this payload into its own DB, so every client must survive:
// filtering one out makes the node delete it, and the master then mirrors that.
func (s *InboundService) buildInboundForNodePush(tx *gorm.DB, inbound *model.Inbound) (*model.Inbound, error) {
	if inbound == nil {
		return nil, fmt.Errorf("inbound is nil")
	}

	built := *inbound
	settings := map[string]any{}
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
		return nil, err
	}

	if !inboundCanHostFallbacks(inbound) {
		return &built, nil
	}
	fallbacks, err := s.fallbackService.BuildFallbacksJSON(tx, inbound.Id)
	if err != nil {
		return nil, err
	}
	if len(fallbacks) == 0 {
		return &built, nil
	}
	generic := make([]any, 0, len(fallbacks))
	for _, f := range fallbacks {
		generic = append(generic, f)
	}
	settings["fallbacks"] = generic

	modifiedSettings, mErr := json.MarshalIndent(settings, "", "  ")
	if mErr != nil {
		return nil, mErr
	}
	built.Settings = string(modifiedSettings)
	return &built, nil
}

// Strips disabled clients on top of the node payload. Safe only because the
// target here is an in-memory Xray/mtg config, not another panel's database.
func (s *InboundService) buildInboundForLocalRuntime(tx *gorm.DB, inbound *model.Inbound) (*model.Inbound, error) {
	built, err := s.buildInboundForNodePush(tx, inbound)
	if err != nil {
		return nil, err
	}

	settings := map[string]any{}
	if err := json.Unmarshal([]byte(built.Settings), &settings); err != nil {
		return nil, err
	}
	clients, ok := settings["clients"].([]any)
	if !ok {
		return built, nil
	}

	depleted, err := depletedEmails(tx)
	if err != nil {
		return nil, err
	}

	finalClients := make([]any, 0, len(clients))
	for _, client := range clients {
		c, ok := client.(map[string]any)
		if !ok {
			continue
		}
		email, _ := c["email"].(string)
		if depleted[email] {
			continue
		}
		if manualEnable, ok := c["enable"].(bool); ok && !manualEnable {
			continue
		}
		finalClients = append(finalClients, c)
	}
	settings["clients"] = finalClients

	modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	runtimeInbound := *built
	runtimeInbound.Settings = string(modifiedSettings)
	return &runtimeInbound, nil
}

// updateClientTraffics syncs the ClientTraffic rows with the inbound's clients
// list: removes rows for emails that disappeared, inserts rows for newly-added
// emails. Uses sets for O(N) lookup — the previous nested-loop implementation
// was O(N²) and degraded into multi-second pauses on inbounds with thousands
// of clients (toggling, saving, or deleting any such inbound felt frozen).
func (s *InboundService) updateClientTraffics(tx *gorm.DB, oldInbound *model.Inbound, newInbound *model.Inbound) error {
	oldClients, err := s.GetClients(oldInbound)
	if err != nil {
		return err
	}
	newClients, err := s.GetClients(newInbound)
	if err != nil {
		return err
	}

	// Email is the unique key for ClientTraffic rows. Clients without an
	// email have no stats row to sync — skip them on both sides instead of
	// risking a unique-constraint hit or accidental delete of an unrelated row.
	oldEmails := make(map[string]struct{}, len(oldClients))
	for i := range oldClients {
		if oldClients[i].Email == "" {
			continue
		}
		oldEmails[oldClients[i].Email] = struct{}{}
	}
	newEmails := make(map[string]struct{}, len(newClients))
	for i := range newClients {
		if newClients[i].Email == "" {
			continue
		}
		newEmails[newClients[i].Email] = struct{}{}
	}

	// Drop stats rows for removed emails — but not when a sibling inbound
	// still references the email, since the row is the shared accumulator.
	for i := range oldClients {
		email := oldClients[i].Email
		if email == "" {
			continue
		}
		if _, kept := newEmails[email]; kept {
			continue
		}
		stillUsed, err := s.emailUsedByOtherInbounds(email, oldInbound.Id)
		if err != nil {
			return err
		}
		if stillUsed {
			continue
		}
		if err := s.DelClientStat(tx, email); err != nil {
			return err
		}
		// Keep inbound_client_ips in sync when the inbound edit drops an
		// email, so the IP-limit job doesn't keep a ghost tracking row (#4963).
		if err := s.DelClientIPs(tx, email); err != nil {
			return err
		}
	}
	for i := range newClients {
		email := newClients[i].Email
		if email == "" {
			continue
		}
		if _, existed := oldEmails[email]; existed {
			if err := s.UpdateClientStat(tx, email, &newClients[i]); err != nil {
				return err
			}
			continue
		}
		if err := s.AddClientStat(tx, oldInbound.Id, &newClients[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *InboundService) GetInboundTags() (string, error) {
	db := database.GetDB()
	var inboundTags []string
	err := db.Model(model.Inbound{}).Select("tag").Find(&inboundTags).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	tags, _ := json.Marshal(inboundTags)
	return string(tags), nil
}

// Routing subject reason keys. Declared in Go so the frontend dead-key test
// counts them as referenced, the same way a Descriptor's title key is.
const (
	subjectReasonNode        = "pages.xray.subjects.reasonNode"
	subjectReasonDisabled    = "pages.xray.subjects.reasonDisabled"
	subjectReasonNoXray      = "pages.xray.subjects.reasonNoXrayInbound"
	subjectReasonBridgeOff   = "pages.xray.subjects.reasonBridgeOff"
	subjectReasonUnknownCore = "pages.xray.subjects.reasonUnknownCore"
)

// RoutingSubject is one inbound as a routing rule may name it. An unroutable tag
// is offered disabled with ReasonKey, never hidden — hiding it reads as data loss.
type RoutingSubject struct {
	Id        int    `json:"id" example:"3"`
	Tag       string `json:"tag" example:"inbound-443"`
	Remark    string `json:"remark" example:"main vless"`
	Protocol  string `json:"protocol" example:"vless"`
	Routable  bool   `json:"routable" example:"true"`
	ReasonKey string `json:"reasonKey,omitempty" example:"pages.xray.subjects.reasonNode"`
}

/*
GetRoutingSubjects answers which inbounds a routing rule can actually name.

GetInboundTags above is every row with no WHERE, so the editor has always offered
tags the router provably never sees — a rule on one is accepted and then silently
never matches. This is the same question RenderInbound answers, asked out loud.
*/
func (s *InboundService) GetRoutingSubjects() ([]RoutingSubject, error) {
	var inbounds []*model.Inbound
	err := database.GetDB().Model(model.Inbound{}).Find(&inbounds).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	subjects := make([]RoutingSubject, 0, len(inbounds))
	for _, inbound := range inbounds {
		reason := routingSubjectReason(inbound)
		subjects = append(subjects, RoutingSubject{
			Id:        inbound.Id,
			Tag:       inbound.Tag,
			Remark:    inbound.Remark,
			Protocol:  string(inbound.Protocol),
			Routable:  reason == "",
			ReasonKey: reason,
		})
	}
	return subjects, nil
}

/*
routingSubjectReason returns "" when a rule may name this inbound, else the
translation key saying why it cannot.

The answer comes from the core registry rather than from "does Xray serve it".
A kernel inbound is routable even though Xray never sees its packets, because
the routing compile fronts it and rewrites the rule onto the front's tag — so
the honest question is whether ANY core declares a routable ingress for it.
*/
func routingSubjectReason(inbound *model.Inbound) string {
	if inbound == nil {
		return subjectReasonNoXray
	}
	// A node serves its own core and never receives this host's rules array.
	if inbound.NodeID != nil {
		return subjectReasonNode
	}
	if !inbound.Enable {
		return subjectReasonDisabled
	}
	kind := core.Kind(inbound.Protocol)
	if !slices.Contains(cores.Kinds(), kind) {
		return subjectReasonUnknownCore
	}
	if cores.IngressSelectorFor(kind) == core.IngressNone {
		return subjectReasonNoXray
	}
	// The instance's own answer: an MTProto inbound with its bridge switched off
	// is a routable KIND whose current instance still exposes nothing.
	handle, err := cores.IngressHandleFor(context.Background(), core.Instance{
		ID: inbound.Id, Kind: kind, Tag: inbound.Tag, Settings: inbound.Settings,
	})
	if err != nil {
		return subjectReasonNoXray
	}
	if handle.BlockedKey != "" {
		return handle.BlockedKey
	}
	if handle.Tag == "" && !handle.Fronted() {
		return subjectReasonNoXray
	}
	return ""
}

func (s *InboundService) GetClientReverseTags() (string, error) {
	db := database.GetDB()
	var inbounds []model.Inbound
	err := db.Model(model.Inbound{}).Select("settings").Where("protocol = ?", "vless").Find(&inbounds).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "[]", err
	}

	tagSet := make(map[string]struct{})
	for _, inbound := range inbounds {
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			continue
		}
		clients, ok := settings["clients"].([]any)
		if !ok {
			continue
		}
		for _, client := range clients {
			clientMap, ok := client.(map[string]any)
			if !ok {
				continue
			}
			reverse, ok := clientMap["reverse"].(map[string]any)
			if !ok {
				continue
			}
			tag, _ := reverse["tag"].(string)
			tag = strings.TrimSpace(tag)
			if tag != "" {
				tagSet[tag] = struct{}{}
			}
		}
	}

	rawTags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		rawTags = append(rawTags, tag)
	}
	sort.Strings(rawTags)

	result, _ := json.Marshal(rawTags)
	return string(result), nil
}

func (s *InboundService) SearchInbounds(query string) ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Preload("ClientStats").Where("remark like ?", "%"+query+"%").Find(&inbounds).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return inbounds, nil
}
