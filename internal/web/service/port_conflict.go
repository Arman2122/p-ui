package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/util/common"
)

type transportBits uint8

const (
	transportTCP transportBits = 1 << iota
	transportUDP
)

/*
inboundTransports asks the kind's core what it binds.

This used to derive the answer from the protocol string: two switches plus a
generic streamSettings peek, all in the service layer, all guessing on behalf of
cores that know. A kind no core claims keeps the oldest rule — TCP — so a
quarantined inbound still occupies its port for conflict purposes rather than
becoming invisible to the check.
*/
func inboundTransports(protocol model.Protocol, streamSettings, settings string) transportBits {
	kind := core.Kind(protocol)
	bound, ok := cores.TransportAuthority(kind)
	if !ok {
		return transportTCP
	}
	answer := bound.Transports(kind, settings, streamSettings)
	// The one answer that means it: a kind declaring None binds no port at all,
	// so zero bits overlap nothing and the checks below skip it. An EMPTY answer
	// still reads TCP -- that is negligence, not a declaration. F319.
	if answer.None {
		return 0
	}
	var bits transportBits
	if answer.TCP {
		bits |= transportTCP
	}
	if answer.UDP {
		bits |= transportUDP
	}
	if bits == 0 {
		// A core that claims to bind nothing would disable the check for its own
		// kind; the port is occupied either way.
		bits = transportTCP
	}
	return bits
}

func listenOverlaps(a, b string) bool {
	if isAnyListen(a) || isAnyListen(b) {
		return true
	}
	return a == b
}

func isAnyListen(s string) bool {
	return s == "" || s == "0.0.0.0" || s == "::" || s == "::0"
}

type portConflictDetail struct {
	InboundID  int
	Remark     string
	Tag        string
	Listen     string
	Port       int
	Transports transportBits
}

// String renders the detail as a single-line, user-facing summary.
func (d *portConflictDetail) String() string {
	name := d.Remark
	if name == "" {
		name = d.Tag
	}
	if name == "" {
		name = fmt.Sprintf("#%d", d.InboundID)
	} else if d.InboundID > 0 {
		name = fmt.Sprintf("'%s' (#%d)", name, d.InboundID)
	} else {
		// reserved/system inbounds (e.g. the Xray API) have no DB id.
		name = fmt.Sprintf("'%s'", name)
	}
	listen := d.Listen
	if isAnyListen(listen) {
		listen = "*"
	}
	// The panel's own listeners and the Xray API have no row, so "inbound" would
	// send the operator looking for one on a page that will never show it.
	kind := "inbound"
	if d.InboundID == 0 {
		kind = "listener"
	}
	return fmt.Sprintf("port %d (%s) already used by %s %s on %s",
		d.Port, transportTagSuffix(d.Transports), kind, name, listen)
}

// defaultXrayAPIPort is the loopback port of the internal Xray API inbound
// (tag "api") seeded into the config template. Used as a fallback when the
// template can't be parsed.
const defaultXrayAPIPort = 62789

// reservedAPIPort returns the port of the internal Xray API inbound declared
// in the config template, falling back to defaultXrayAPIPort.
func reservedAPIPort() int {
	tmpl, err := (&SettingService{}).GetXrayConfigTemplate()
	if err != nil || tmpl == "" {
		return defaultXrayAPIPort
	}
	var parsed struct {
		Inbounds []struct {
			Port int    `json:"port"`
			Tag  string `json:"tag"`
		} `json:"inbounds"`
	}
	if json.Unmarshal([]byte(tmpl), &parsed) != nil {
		return defaultXrayAPIPort
	}
	for _, in := range parsed.Inbounds {
		if in.Tag == "api" && in.Port > 0 {
			return in.Port
		}
	}
	return defaultXrayAPIPort
}

/*
panelListenerConflict names the two TCP ports the panel process binds itself.

Neither is a row in inbounds, so nothing above sees them — and both defaults,
2053 and 2096, are in Cloudflare's proxied-HTTPS set, which is exactly why an
operator picks them for a CDN-frontable inbound. Xray cannot bind a port the
panel already holds, and a failed listener is fatal to the WHOLE process rather
than to that one inbound, so the box goes dark and the watchdog restarts it into
the same failure every couple of seconds.

Not caught anywhere else: the -test pre-flight is LoadConfig plus core.New, and
binds nothing.
*/
func panelListenerConflict(inbound *model.Inbound, bits transportBits) (*portConflictDetail, error) {
	// Same two conjuncts the api guard opens with: a node runs its own Xray, and
	// both listeners are TCP, so a UDP-only inbound may share the port.
	if inbound.NodeID != nil || bits&transportTCP == 0 {
		return nil, nil
	}

	setting := &SettingService{}
	webPort, err := setting.GetPort()
	if err != nil {
		return nil, err
	}
	// web.go binds PUI_PORT when it is set, so the stored setting is then neither
	// the port in use nor a port anything will bind.
	if envPort, configured, envErr := config.GetPortOverride(); configured && envErr == nil {
		webPort = envPort
	}
	webListen, err := setting.GetListen()
	if err != nil {
		return nil, err
	}
	if inbound.Port == webPort && listenOverlaps(webListen, inbound.Listen) {
		return &portConflictDetail{
			Tag: "panel", Listen: webListen, Port: inbound.Port, Transports: transportTCP,
		}, nil
	}

	subEnable, err := setting.GetSubEnable()
	if err != nil {
		return nil, err
	}
	if !subEnable {
		return nil, nil
	}
	subPort, err := setting.GetSubPort()
	if err != nil {
		return nil, err
	}
	subListen, err := setting.GetSubListen()
	if err != nil {
		return nil, err
	}
	if inbound.Port == subPort && listenOverlaps(subListen, inbound.Listen) {
		return &portConflictDetail{
			Tag: "subscription", Listen: subListen, Port: inbound.Port, Transports: transportTCP,
		}, nil
	}
	return nil, nil
}

func (s *InboundService) checkPortConflict(inbound *model.Inbound, ignoreId int) (*portConflictDetail, error) {
	newBits := inboundTransports(inbound.Protocol, inbound.StreamSettings, inbound.Settings)

	// The internal Xray API inbound (tag "api", loopback TCP) isn't a DB row,
	// so a local user inbound reusing its port would leave Xray binding the
	// port twice (#5304). Nodes run their own Xray, so this only applies to
	// the local panel.
	if inbound.NodeID == nil && inbound.Port == reservedAPIPort() &&
		newBits&transportTCP != 0 && listenOverlaps("127.0.0.1", inbound.Listen) {
		return &portConflictDetail{
			Tag:        "api",
			Listen:     "127.0.0.1",
			Port:       inbound.Port,
			Transports: transportTCP,
		}, nil
	}

	// The band core-owned exits serve their SOCKS ports in, loopback TCP.
	if inbound.NodeID == nil && core.IsExitSocksPort(inbound.Port) &&
		newBits&transportTCP != 0 && listenOverlaps("127.0.0.1", inbound.Listen) {
		return &portConflictDetail{
			Tag:        "exit-socks",
			Listen:     "127.0.0.1",
			Port:       inbound.Port,
			Transports: transportTCP,
		}, nil
	}
	if hit, err := panelListenerConflict(inbound, newBits); err != nil || hit != nil {
		return hit, err
	}

	db := database.GetDB()

	var candidates []*model.Inbound
	q := db.Model(model.Inbound{}).Where("port = ?", inbound.Port)
	if ignoreId > 0 {
		q = q.Where("id != ?", ignoreId)
	}
	if err := q.Find(&candidates).Error; err != nil {
		return nil, err
	}

	for _, c := range candidates {
		if !sameNode(c.NodeID, inbound.NodeID) {
			continue
		}
		if !listenOverlaps(c.Listen, inbound.Listen) {
			continue
		}
		existingBits := inboundTransports(c.Protocol, c.StreamSettings, c.Settings)
		shared := existingBits & newBits
		if shared == 0 {
			continue
		}
		return &portConflictDetail{
			InboundID:  c.Id,
			Remark:     c.Remark,
			Tag:        c.Tag,
			Listen:     c.Listen,
			Port:       c.Port,
			Transports: shared,
		}, nil
	}
	return nil, nil
}

func sameNode(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func baseInboundTag(port int) string {
	return fmt.Sprintf("in-%v", port)
}

func transportTagSuffix(b transportBits) string {
	switch b {
	// Only a kind that declared it binds nothing reaches zero bits.
	case 0:
		return "none"
	case transportTCP:
		return "tcp"
	case transportUDP:
		return "udp"
	case transportTCP | transportUDP:
		return "tcpudp"
	}
	return "any"
}

// nodeTagPrefix scopes a tag to one remote node so the same listen+port
// can live on the central panel and on a node without bumping the global
// UNIQUE(inbounds.tag) constraint. nil → "" (local panel).
func nodeTagPrefix(nodeID *int) string {
	if nodeID == nil {
		return ""
	}
	return fmt.Sprintf("n%d-", *nodeID)
}

func composeInboundTag(port int, nodeID *int, bits transportBits) string {
	return nodeTagPrefix(nodeID) + baseInboundTag(port) + "-" + transportTagSuffix(bits)
}

func isAutoGeneratedTag(tag string, port int, nodeID *int, bits transportBits) bool {
	base := composeInboundTag(port, nodeID, bits)
	if tag == base {
		return true
	}
	suffix, ok := strings.CutPrefix(tag, base+"-")
	if !ok || suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *InboundService) generateInboundTag(inbound *model.Inbound, ignoreId int) (string, error) {
	bits := inboundTransports(inbound.Protocol, inbound.StreamSettings, inbound.Settings)
	candidate := composeInboundTag(inbound.Port, inbound.NodeID, bits)
	exists, err := s.tagExists(candidate, ignoreId)
	if err != nil {
		return "", err
	}
	if !exists {
		return candidate, nil
	}

	for i := 2; i < 100; i++ {
		c := fmt.Sprintf("%s-%d", candidate, i)
		exists, err = s.tagExists(c, ignoreId)
		if err != nil {
			return "", err
		}
		if !exists {
			return c, nil
		}
	}
	return "", common.NewError("could not pick a unique inbound tag for port:", inbound.Port)
}

func (s *InboundService) resolveInboundTag(inbound *model.Inbound, ignoreId int) (string, error) {
	if inbound.Tag != "" {
		if reserved := reservedTagOwner(inbound.Tag); reserved != "" {
			return "", common.NewError("the tag \"" + inbound.Tag + "\" is reserved for " + reserved +
				". Inbound and outbound tags share one namespace in the generated config, so this would replace it. Pick another tag.")
		}
		taken, err := s.tagExists(inbound.Tag, ignoreId)
		if err != nil {
			return "", err
		}
		if !taken {
			return inbound.Tag, nil
		}
	}
	return s.generateInboundTag(inbound, ignoreId)
}

/*
reservedTagOwner names what already owns a tag, or "" when it is the
operator's to use.

tagExists only counts rows in `inbounds`, and the tags that matter here belong
to objects that have no row: the panel's own egress bridge, a routing front, and
the template's api/direct/blocked outbounds. So an inbound tagged "panel-egress" was accepted,
and injectPanelEgress then found the tag already present and skipped injection —
the panel's SOCKS bridge and its routing rule silently vanished from the
generated config behind a single Warning line.

Derived from the template rather than hardcoded a second time, the way
reservedAPIPort is: an operator who renames the api outbound must not have the
stale name reserved and the live one free.
*/
func reservedTagOwner(tag string) string {
	if tag == PanelEgressInboundTag {
		return "the panel's own egress bridge"
	}
	// A routing front's Xray inbound is synthesised at compile time and tagged
	// with its device name; it never becomes a row in `inbounds`, so tagExists
	// cannot see it and two inbounds with one tag reached the core, which then
	// refuses the whole config. F231.
	if egress.NamesAFront(tag) {
		return "a routing front"
	}
	for _, templateTag := range templateReservedTags() {
		if tag == templateTag {
			return "an outbound in the Xray config template"
		}
	}
	return ""
}

// templateReservedTags lists the tags the config template already declares, so
// a renamed template moves the reservation with it.
func templateReservedTags() []string {
	fallback := []string{"api", "direct", "blocked"}
	tmpl, err := (&SettingService{}).GetXrayConfigTemplate()
	if err != nil || tmpl == "" {
		return fallback
	}
	var parsed struct {
		Inbounds  []struct{ Tag string } `json:"inbounds"`
		Outbounds []struct{ Tag string } `json:"outbounds"`
	}
	if json.Unmarshal([]byte(tmpl), &parsed) != nil {
		return fallback
	}
	out := make([]string, 0, len(parsed.Inbounds)+len(parsed.Outbounds))
	for _, in := range parsed.Inbounds {
		if in.Tag != "" {
			out = append(out, in.Tag)
		}
	}
	for _, outbound := range parsed.Outbounds {
		if outbound.Tag != "" {
			out = append(out, outbound.Tag)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func (s *InboundService) tagExists(tag string, ignoreId int) (bool, error) {
	db := database.GetDB()
	q := db.Model(model.Inbound{}).Where("tag = ?", tag)
	if ignoreId > 0 {
		q = q.Where("id != ?", ignoreId)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
