package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/drivers/wgclient"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/routing"
	"github.com/Arman2122/p-ui/internal/util/json_util"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
RoutingService is the adapter between stored intent and the pure compile.

Everything protocol-specific is resolved here, once, by asking the core registry;
routing.Plan never learns which core it is serving. The service owns front
allocation because an id has to come out of the egresses sequence, and that is a
database fact the compile is deliberately kept free of.
*/
type RoutingService struct{}

// Rules returns every rule in the operator's own order.
func (s *RoutingService) Rules() ([]*model.RoutingRule, error) {
	var rules []*model.RoutingRule
	err := database.GetDB().Order("sort_index").Order("id").Find(&rules).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return rules, nil
}

/*
Subjects lists every inbound a rule may name, with its core's answer resolved.

node_id is decided HERE and never by a core: core.Instance carries no node id,
and DesiredInstances filters node inbounds out before any core is asked, so a
core structurally cannot answer "this one lives somewhere else".
*/
func (s *RoutingService) Subjects(ctx context.Context) ([]routing.Subject, error) {
	var inbounds []*model.Inbound
	err := database.GetDB().Where("node_id IS NULL AND enable = ?", true).Order("id").Find(&inbounds).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	out := make([]routing.Subject, 0, len(inbounds))
	for _, inbound := range inbounds {
		handle, hErr := cores.IngressHandleFor(ctx, core.Instance{
			ID: inbound.Id, Kind: core.Kind(inbound.Protocol), Tag: inbound.Tag, Settings: inbound.Settings,
		})
		if hErr != nil {
			continue
		}
		if handle.Tag == "" && handle.Device == "" && handle.BlockedKey == "" {
			continue
		}
		out = append(out, routing.Subject{
			InboundID: inbound.Id, Tag: inbound.Tag, Handle: handle,
			CriteriaMask: criteriaMaskFor(core.Kind(inbound.Protocol)),
		})
	}
	return out, nil
}

/*
criteriaMaskFor names the criteria that can actually work on one subject.

Asked of the kind's core, not inferred here. This used to branch on
`handle.Device != ""`, which reads the SELECTOR and not the surface: xray and
mtproto both answer IngressInternal, so an mtproto subject was granted `user`,
`domain`, `protocol` and `attrs` — none of which can match, because its
plaintext reaches Xray over a loopback SOCKS bridge that authenticated nobody
and sniffs nothing. A rule written on any of them saved and never matched, which
is the whole class of bug the routing editor exists to remove. F060.
*/
func criteriaMaskFor(kind core.Kind) []string {
	declared := cores.IngressCriteriaFor(kind)
	out := make([]string, 0, len(declared))
	for _, criterion := range declared {
		out = append(out, string(criterion))
	}
	return out
}

/*
frontSniffing is the sniffing block an inspected L3 front carries.

routeOnly is load-bearing, not a preference. Without it the dispatcher REPLACES
the dial target with the sniffed name, and the front's own containment guard
matches `ip: geoip:private` -- a target rewritten to a domain has no IP for that
guard to see, so a rule pointing at a private address would slip past the one
protection a front is not exempt from. With it, GetTargetDomain reads
RouteTarget while GetTargetIPs still reads Target: the name becomes matchable
and the guard keeps holding. Verified against the pinned core
(features/routing/session/context.go, app/dispatcher/default.go:311).

quic is in the list because it is most of what the operator is trying to catch:
a browser speaks QUIC to Google, and sniffing only http and tls would miss it.
*/
var frontSniffing = json_util.RawMessage(`{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`)

/*
sniffingEnabled reports whether an inbound already reads the payload.

Only "enabled" is read, because that is the whole question: which protocols it
overrides, what it excludes and whether it rewrites the target are the
operator's answers to a question they have already been asked. An unparsable
block reads as enabled -- overwriting settings the panel could not understand is
worse than leaving a rule inert, and the diagnostics say when a rule did not
match.
*/
func sniffingEnabled(raw json_util.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var parsed struct {
		Enabled *bool `json:"enabled"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return true
	}
	return parsed.Enabled == nil || *parsed.Enabled
}

// compile resolves everything the plan needs and runs it. Front ids are
// allocated first, so the compile stays pure and repeatable.
func (s *RoutingService) compile(ctx context.Context, cfgOutbounds []map[string]any, retireFronts bool) (routing.Compiled, error) {
	rules, err := s.Rules()
	if err != nil {
		return routing.Compiled{}, err
	}
	subjects, err := s.Subjects(ctx)
	if err != nil {
		return routing.Compiled{}, err
	}
	fronts, err := s.ensureFronts(subjects, rules, retireFronts)
	if err != nil {
		return routing.Compiled{}, err
	}
	exits, err := s.Exits(ctx)
	if err != nil {
		return routing.Compiled{}, err
	}
	blackhole, direct := outboundsByProtocol(cfgOutbounds)
	return routing.Plan(routing.Input{
		Rules: toPlanRules(rules), Subjects: subjects, Exits: exits,
		Blackhole: blackhole, Direct: direct,
		FrontIDFor: func(inboundID int) (int, bool) {
			id, ok := fronts[inboundID]
			return id, ok
		},
	}), nil
}

/*
ensureFronts gives every device ingress that a rule names exactly one front row.

Upserted on ingress_inbound_id, which is UNIQUE, so the one-front-per-ingress
rule is the schema's rather than a convention. Fronts and exits come out of the
SAME sequence, which is what guarantees a front and an exit can never derive the
same table, priority or device.
*/
func (s *RoutingService) ensureFronts(subjects []routing.Subject, rules []*model.RoutingRule, retireFronts bool) (map[int]int, error) {
	needed := map[int]bool{}
	for _, rule := range rules {
		if !rule.Enable {
			continue
		}
		for _, id := range ruleIngressIDs(rule, subjects) {
			needed[id] = true
		}
	}
	out := map[int]int{}
	db := database.GetDB()
	for _, subject := range subjects {
		if subject.Handle.Device == "" || !needed[subject.InboundID] {
			continue
		}
		row := &model.Egress{
			Type: "xray-tun", Enable: true, Owner: model.EgressOwnerPanel,
			Remark: subject.Tag, IngressInboundId: &subject.InboundID,
		}
		err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "ingress_inbound_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable", "owner", "type"}),
		}).Create(row).Error
		if err != nil {
			return nil, fmt.Errorf("routing: provision a front for inbound %d: %w", subject.InboundID, err)
		}
		if row.Id == 0 {
			var existing model.Egress
			if err := db.Where("ingress_inbound_id = ?", subject.InboundID).First(&existing).Error; err != nil {
				return nil, err
			}
			row.Id = existing.Id
		}
		out[subject.InboundID] = row.Id
	}
	// Reaping is cleanup, and it must not be able to stop a start. Now that a
	// compile error refuses the whole config (F055), a Del that fails here --
	// ErrEgressInUse on a stale row, say -- would darken every inbound on the
	// box to tidy up one. The orphan it leaves behind costs the one ingress
	// whose front row survived.
	// And a READ must not retire a front the running core still forwards
	// through: only the caller that is about to apply the config may reap.
	if retireFronts {
		if err := s.reapFronts(out); err != nil {
			logger.Warning("routing: a front that no rule names could not be retired:", err)
		}
	}
	return out, nil
}

/*
reapFronts removes the front of an ingress no rule names any more.

Without this the row outlives its last rule, and the row is not the harm: the
ip rule it derives keeps selecting that device into a table that now holds only
its blackhole, so the inbound stops forwarding entirely. Measured -- deleting
the last rule took a working WireGuard client from the internet to nothing,
which is worse than the state before any rule existed.

Panel-owned rows only. An operator's uplink is theirs to delete.
*/
func (s *RoutingService) reapFronts(keep map[int]int) error {
	var rows []*model.Egress
	err := database.GetDB().Where("owner = ?", model.EgressOwnerPanel).Find(&rows).Error
	if err != nil {
		return err
	}
	/*
		A front whose ingress is merely DISABLED is kept.

		Subjects() reads enabled inbounds only, so a disabled one has no subject,
		its front was not in `keep`, and it was retired — then re-enabling created
		a fresh row with a fresh id. Every disable/enable cycle burned one of the
		999 ids the whole scheme has, and renamed the device and routing table
		derived from it.

		The row costs nothing while the inbound is down: nothing renders that
		inbound, so nothing selects its device. It is reaped the moment the
		inbound is back and no rule names it. F070.
	*/
	var disabled []int
	if err := database.GetDB().Model(&model.Inbound{}).
		Where("node_id IS NULL AND enable = ?", false).
		Pluck("id", &disabled).Error; err != nil {
		return err
	}
	dormant := make(map[int]struct{}, len(disabled))
	for _, id := range disabled {
		dormant[id] = struct{}{}
	}
	for _, row := range rows {
		if row.IngressInboundId == nil {
			continue
		}
		if id, wanted := keep[*row.IngressInboundId]; wanted && id == row.Id {
			continue
		}
		if _, asleep := dormant[*row.IngressInboundId]; asleep {
			continue
		}
		if err := (&EgressService{}).Del(row.Id); err != nil {
			return fmt.Errorf("routing: retire the front of inbound %d: %w", *row.IngressInboundId, err)
		}
	}
	return nil
}

// ruleIngressIDs resolves a rule's subjects, expanding "all" against what is
// routable right now rather than against what was routable when it was saved.
func ruleIngressIDs(rule *model.RoutingRule, subjects []routing.Subject) []int {
	if rule.IngressScope == model.RoutingScopeAll {
		out := make([]int, 0, len(subjects))
		for _, subject := range subjects {
			out = append(out, subject.InboundID)
		}
		return out
	}
	var ids []int
	if json.Unmarshal([]byte(rule.IngressIds), &ids) != nil {
		return nil
	}
	return ids
}

func toPlanRules(rules []*model.RoutingRule) []routing.Rule {
	out := make([]routing.Rule, 0, len(rules))
	for _, rule := range rules {
		var ids []int
		_ = json.Unmarshal([]byte(rule.IngressIds), &ids)
		criteria := map[string]json.RawMessage{}
		_ = json.Unmarshal([]byte(rule.Criteria), &criteria)
		exitID := 0
		if rule.DestExitId != nil {
			exitID = *rule.DestExitId
		}
		out = append(out, routing.Rule{
			ID: rule.Id, SortIndex: rule.SortIndex, Enable: rule.Enable,
			Scope: rule.IngressScope, IngressIDs: ids, Criteria: criteria,
			Dest:    routing.Dest{Kind: rule.DestKind, Tag: rule.DestTag, ExitID: exitID},
			Inspect: rule.Inspect,
		})
	}
	return out
}

// outboundsByProtocol finds the blackhole and the direct outbound by PROTOCOL,
// never by the tags the default template happens to call them.
func outboundsByProtocol(outbounds []map[string]any) (blackhole, direct string) {
	for _, outbound := range outbounds {
		tag, _ := outbound["tag"].(string)
		if tag == "" {
			continue
		}
		switch protocol, _ := outbound["protocol"].(string); protocol {
		case "blackhole":
			if blackhole == "" {
				blackhole = tag
			}
		case "freedom":
			if direct == "" {
				direct = tag
			}
		}
	}
	return blackhole, direct
}

/*
applyRoutingPlan puts the compile's answer into the generated config.

The derived array is deliberately ordered: the api rule stays pinned first (a
catch-all above it starves the gRPC stats path and every client reads offline),
then the panel's own injections, then intent rules, then the operator's tail.
Both are safe because every injected and intent rule is inboundTag-scoped.
*/
func applyRoutingPlan(ctx context.Context, cfg *xray.Config, retireFronts bool) error {
	var outbounds []map[string]any
	readable := true
	if len(cfg.OutboundConfigs) > 0 {
		if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
			// The contract mergeSubscriptionOutbounds spells out and the other three
			// injectors keep: a template we cannot read is one we must not rewrite.
			logger.Warning("routing: the template outbounds do not parse; leaving them untouched:", err)
			readable = false
		}
	}
	compiled, err := (&RoutingService{}).compile(ctx, outbounds, retireFronts)
	if err != nil {
		return err
	}
	/*
		Saving a rule the compiler would drop is refused (F049); these are the ones
		that went inert AFTERWARDS, when an inbound or an exit changed under them.
		The log line is the floor. The snapshot is the surface: the rules list the
		Routing page polls serves it, because generation happens on restart and has
		no response of its own to attach a warning to. Ledger L8.
	*/
	refusals := compiled.Refusals()
	recordRoutingRefusals(refusals)
	for _, diag := range refusals {
		logger.Warningf("routing: rule %d is inert for %s: %s %v",
			diag.RuleID, diag.SubjectTag, diag.MessageKey, diag.Args)
	}

	if len(compiled.XrayRules) == 0 && len(compiled.Fronts) == 0 && len(compiled.InspectTags) == 0 {
		return nil
	}

	/*
		Every inbound a domain rule names is made to sniff, in the GENERATED
		config only.

		The stored row is deliberately untouched: an operator who turned sniffing
		off did that for their traffic, and this is the router asking a question
		about one connection. An inbound already sniffing keeps its own block --
		theirs may exclude domains or carry fakedns, and replacing it would answer
		a question they had already answered.
	*/
	if len(compiled.InspectTags) > 0 {
		wanted := make(map[string]struct{}, len(compiled.InspectTags))
		for _, tag := range compiled.InspectTags {
			wanted[tag] = struct{}{}
		}
		for i := range cfg.InboundConfigs {
			inbound := &cfg.InboundConfigs[i]
			if _, ok := wanted[inbound.Tag]; !ok {
				continue
			}
			if sniffingEnabled(inbound.Sniffing) {
				continue
			}
			inbound.Sniffing = frontSniffing
		}
	}

	for _, front := range compiled.Fronts {
		driver, known := egressDriverRegistry.For("xray-tun")
		if !known {
			continue
		}
		injector, injects := driver.(egress.Injector)
		if !injects {
			continue
		}
		injection, iErr := injector.Inject(egress.Egress{ID: front.ID, Type: "xray-tun", Enable: true})
		if iErr != nil {
			logger.Warning("routing: could not build the front for ingress", front.ID, iErr)
			continue
		}
		var inbound xray.InboundConfig
		if json.Unmarshal(injection.Inbound, &inbound) != nil {
			continue
		}
		if front.Sniff {
			inbound.Sniffing = frontSniffing
		}
		cfg.InboundConfigs = append(cfg.InboundConfigs, inbound)
	}

	if readable && len(compiled.Outbounds) > 0 {
		merged := append([]map[string]any(nil), outbounds...)
		for _, raw := range compiled.Outbounds {
			var one map[string]any
			if json.Unmarshal(raw, &one) == nil {
				merged = append(merged, one)
			}
		}
		if body, mErr := json.Marshal(merged); mErr == nil {
			cfg.OutboundConfigs = body
		}
	}

	routingSection := map[string]any{}
	if len(cfg.RouterConfig) > 0 {
		if json.Unmarshal(cfg.RouterConfig, &routingSection) != nil {
			return errors.New("routing: the generated routing section is unparsable")
		}
	}
	existing, _ := routingSection["rules"].([]any)
	head, tail := splitPinnedAPIRule(existing)
	next := append([]any{}, head...)
	for _, raw := range compiled.XrayRules {
		var one map[string]any
		if json.Unmarshal(raw, &one) == nil {
			next = append(next, one)
		}
	}
	routingSection["rules"] = append(next, tail...)
	body, err := json.Marshal(routingSection)
	if err != nil {
		return err
	}
	cfg.RouterConfig = body
	return nil
}

// splitPinnedAPIRule keeps the api rule and the panel's own prepended injections
// above intent. EnsureStatsRouting re-pins the api rule on every save, and a
// catch-all above it would starve the stats path for every client.
func splitPinnedAPIRule(rules []any) (head, tail []any) {
	for i, raw := range rules {
		rule, _ := raw.(map[string]any)
		if rule == nil {
			continue
		}
		if out, _ := rule["outboundTag"].(string); out == "api" {
			return rules[:i+1], rules[i+1:]
		}
	}
	return nil, rules
}

/*
PruneInbound takes a deleted inbound out of every rule, then converges.

The FK cascade removes the panel-owned front row, but a row is not kernel state:
the ip rule selecting that device survives as a DETACHED rule, and Linux
re-attaches a detached iif rule as soon as a device of that name reappears --
which resyncPostgresSequences makes possible by re-handing inbound ids. So the
converge here is the fix, and it has to be synchronous.

A rule left naming nobody is disabled rather than deleted: the operator wrote it,
and silently removing their rule is the behaviour this feature exists to end.
*/
func (s *RoutingService) PruneInbound(ctx context.Context, inboundID int) (bool, error) {
	rules, err := s.Rules()
	if err != nil {
		return false, err
	}
	changed := false
	db := database.GetDB()
	for _, rule := range rules {
		var ids []int
		if json.Unmarshal([]byte(rule.IngressIds), &ids) != nil {
			continue
		}
		kept := make([]int, 0, len(ids))
		for _, id := range ids {
			if id != inboundID {
				kept = append(kept, id)
			}
		}
		if len(kept) == len(ids) {
			continue
		}
		body, mErr := json.Marshal(kept)
		if mErr != nil {
			return changed, mErr
		}
		updates := map[string]any{"ingress_ids": string(body)}
		if len(kept) == 0 && rule.IngressScope != model.RoutingScopeAll {
			updates["enable"] = false
			updates["remark"] = orphanRemark(rule.Remark)
		}
		if err := db.Model(&model.RoutingRule{}).Where("id = ?", rule.Id).Updates(updates).Error; err != nil {
			return changed, err
		}
		changed = true
	}
	if err := s.converge(ctx); err != nil {
		return changed, err
	}
	return changed, nil
}

// orphanRemark says why a rule was switched off, in the one place an operator
// will look for it.
func orphanRemark(existing string) string {
	const note = "disabled: its last inbound was deleted"
	if existing == "" {
		return note
	}
	return existing + " (" + note + ")"
}

// RoutingSubjectView is one inbound as the editor offers it, with the reason it
// cannot be named when it cannot.
type RoutingSubjectView struct {
	InboundId    int      `json:"inboundId" example:"7"`
	Tag          string   `json:"tag" example:"wg-home"`
	Selector     string   `json:"selector" example:"device"`
	Routable     bool     `json:"routable" example:"true"`
	BlockedKey   string   `json:"blockedKey,omitempty" example:"pages.xray.subjects.reasonBridgeOff"`
	CriteriaMask []string `json:"criteriaMask"`
}

/*
RoutingExitView is one uplink as the rule editor offers it.

The handle is deliberately not here: which device or port realises an exit is
the compile's business, and an editor that showed it would invite an operator to
depend on it.
*/
type RoutingExitView struct {
	Id    int    `json:"id" example:"4"`
	Label string `json:"label" example:"US-sfo | Surfshark"`
}

// ExitViews is the editor's half of Exits: every uplink a rule may point at,
// named as the operator named it.
func (s *RoutingService) ExitViews(ctx context.Context) ([]RoutingExitView, error) {
	exits, err := s.Exits(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RoutingExitView, 0, len(exits))
	for _, exit := range exits {
		out = append(out, RoutingExitView{Id: exit.ID, Label: exit.Label})
	}
	return out, nil
}

// SubjectViews is the editor's half of Subjects: the same answers, shaped for a
// picker that has to disable a row and say why.
func (s *RoutingService) SubjectViews(ctx context.Context) ([]RoutingSubjectView, error) {
	subjects, err := s.Subjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RoutingSubjectView, 0, len(subjects))
	for _, subject := range subjects {
		selector := "internal"
		if subject.Handle.Device != "" {
			selector = "device"
		}
		out = append(out, RoutingSubjectView{
			InboundId: subject.InboundID, Tag: subject.Tag, Selector: selector,
			Routable:   subject.Handle.BlockedKey == "",
			BlockedKey: subject.Handle.BlockedKey, CriteriaMask: subject.CriteriaMask,
		})
	}
	return out, nil
}

// Add appends a rule and converges before returning: a tick that caught up later
// would leave a just-saved rule egressing with the server's own identity.
func (s *RoutingService) Add(ctx context.Context, rule *model.RoutingRule) (*model.RoutingRule, error) {
	if err := normalizeRule(rule); err != nil {
		return nil, err
	}
	if err := destResolves(rule); err != nil {
		return nil, err
	}
	rule.Id = 0
	candidate, cErr := s.candidateRules(rule)
	if cErr != nil {
		return nil, cErr
	}
	if err := s.refuseInertRule(ctx, candidate); err != nil {
		return nil, err
	}

	var next int
	if err := database.GetDB().Model(&model.RoutingRule{}).
		Select("COALESCE(MAX(sort_index), -1) + 1").Scan(&next).Error; err != nil {
		return nil, err
	}
	rule.SortIndex = next
	// Read before the insert: GORM writes the column's default back into the
	// struct, so afterwards the caller's own intent is gone.
	staged := rule.Enable
	if err := database.GetDB().Create(rule).Error; err != nil {
		return nil, err
	}
	rule.Enable = staged
	if err := applyStagedOff(database.GetDB(), &model.RoutingRule{}, "enable", staged, rule.Id); err != nil {
		return nil, err
	}
	return rule, s.converge(ctx)
}

func (s *RoutingService) Update(ctx context.Context, rule *model.RoutingRule) (*model.RoutingRule, error) {
	if err := normalizeRule(rule); err != nil {
		return nil, err
	}
	if err := destResolves(rule); err != nil {
		return nil, err
	}
	candidate, cErr := s.candidateRules(rule)
	if cErr != nil {
		return nil, cErr
	}
	if err := s.refuseInertRule(ctx, candidate); err != nil {
		return nil, err
	}
	err := database.GetDB().Model(&model.RoutingRule{}).Where("id = ?", rule.Id).Updates(map[string]any{
		"enable": rule.Enable, "remark": rule.Remark,
		"ingress_scope": rule.IngressScope, "ingress_ids": rule.IngressIds,
		"dest_kind": rule.DestKind, "dest_tag": rule.DestTag, "dest_exit_id": rule.DestExitId,
		"criteria": rule.Criteria, "inspect": rule.Inspect,
	}).Error
	if err != nil {
		return nil, err
	}
	return rule, s.converge(ctx)
}

func (s *RoutingService) Del(ctx context.Context, id int) error {
	if err := database.GetDB().Delete(&model.RoutingRule{}, id).Error; err != nil {
		return err
	}
	return s.converge(ctx)
}

// Reorder writes the whole order in one transaction. A partial application would
// silently change which rule wins first match.
func (s *RoutingService) Reorder(ctx context.Context, ids []int) error {
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		for index, id := range ids {
			if err := tx.Model(&model.RoutingRule{}).Where("id = ?", id).
				Update("sort_index", index).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.converge(ctx)
}

/*
converge hands the compile's kernel half to the one manager that owns kernel
state, and arms the config half.

Arming here rather than in each controller is deliberate: a rule change alters
the generated rules array, and every path that writes a rule passes through this
one function. The old attach control changed only kernel state and so never armed
a restart — leaving that would make an attach report success and route nothing.
*/
func (s *RoutingService) converge(ctx context.Context) error {
	(&XrayService{}).SetToNeedRestart()
	// Before the kernel objects, because a rule that routes into a device on a
	// host that forwards nothing is inert, and this is the one knob a fresh
	// install ships off.
	if err := s.EnsureHostForwarding(ctx); err != nil {
		logger.Warning("routing: host forwarding could not be enabled:", err)
	}
	return (&EgressService{}).Reconcile(ctx)
}

/*
destResolves refuses a rule aimed at a tag nothing answers to.

A rule left pointing at a deleted or renamed tag keeps matching, and the pinned
core then drops what it matched: app/dispatcher/default.go logs "non existing
outTag", closes the writer and interrupts the reader, under a DO NOT CHANGE
comment. Shape validation cannot see it — the tag is a non-empty string either
way. This guards the rule-save direction; PruneOutbound guards the other one.

A config this cannot read does not block the save. Refusing every rule write
because the base config is unreadable trades one silent misroute for a total
outage of the page.
*/
func destResolves(rule *model.RoutingRule) error {
	if rule.DestKind != model.RoutingDestOutbound && rule.DestKind != model.RoutingDestBalancer {
		return nil
	}
	resolves, err := egressTargetResolves(rule.DestTag)
	if err != nil {
		logger.Warning("routing: could not check whether", rule.DestTag, "resolves:", err)
		return nil
	}
	if !resolves {
		return fmt.Errorf("routing: %q is neither an outbound tag nor a balancer tag", rule.DestTag)
	}
	return nil
}

// normalizeRule refuses a shape the compile could not realise, at the boundary
// rather than by emitting a rule that never matches.
func normalizeRule(rule *model.RoutingRule) error {
	if rule == nil {
		return errors.New("routing: no rule in the request")
	}
	if rule.IngressScope == "" {
		rule.IngressScope = model.RoutingScopeSelected
	}
	if rule.IngressScope != model.RoutingScopeSelected && rule.IngressScope != model.RoutingScopeAll {
		return fmt.Errorf("routing: %q is not an ingress scope", rule.IngressScope)
	}
	if rule.IngressIds == "" {
		rule.IngressIds = "[]"
	}
	var ids []int
	if json.Unmarshal([]byte(rule.IngressIds), &ids) != nil {
		return errors.New("routing: ingressIds must be a JSON array of inbound ids")
	}
	if rule.IngressScope == model.RoutingScopeSelected && len(ids) == 0 {
		return errors.New("routing: a rule must name at least one inbound, or use the all scope")
	}
	if rule.Criteria == "" {
		rule.Criteria = "{}"
	}
	criteria := map[string]json.RawMessage{}
	if json.Unmarshal([]byte(rule.Criteria), &criteria) != nil {
		return errors.New("routing: criteria must be a JSON object")
	}
	// compileRule copies these values into the emitted rule verbatim, so one
	// unreadable character here is a config the core refuses whole.
	if err := xray.ValidateRoutingCriteria(criteria); err != nil {
		return fmt.Errorf("routing: xray core rejects these criteria: %w", err)
	}
	switch rule.DestKind {
	case model.RoutingDestOutbound, model.RoutingDestBalancer:
		if rule.DestTag == "" {
			return fmt.Errorf("routing: a %s destination needs a tag", rule.DestKind)
		}
	case model.RoutingDestExit:
		if rule.DestExitId == nil {
			return errors.New("routing: an exit destination needs an exit")
		}
	case model.RoutingDestDirect, model.RoutingDestBlock:
	default:
		return fmt.Errorf("routing: %q is not a destination kind", rule.DestKind)
	}
	return nil
}

/*
EnsureHostForwarding turns on packet forwarding when this host has an inbound
that needs it, and leaves the knob alone when it does not.

Asked through the registry rather than by protocol, so a future L3 core enables
it by declaring IngressDevice and nothing here changes. Conditional because a
box serving only Xray inbounds forwards nothing and should not be quietly
reconfigured; enabled-never-disabled because docker, another VPN or a container
network may depend on the same knob, and none of those are the panel's to break.
*/
func (s *RoutingService) EnsureHostForwarding(ctx context.Context) error {
	var inbounds []*model.Inbound
	err := database.GetDB().Model(&model.Inbound{}).
		Select("id", "protocol", "tag", "settings").
		Where("node_id IS NULL AND enable = ?", true).Find(&inbounds).Error
	if err != nil {
		return err
	}
	var devices []string
	for _, inbound := range inbounds {
		if cores.IngressSelectorFor(core.Kind(inbound.Protocol)) != core.IngressDevice {
			continue
		}
		handle, hErr := cores.IngressHandleFor(ctx, core.Instance{
			ID: inbound.Id, Kind: core.Kind(inbound.Protocol), Tag: inbound.Tag, Settings: inbound.Settings,
		})
		if hErr == nil && handle.Device != "" {
			devices = append(devices, handle.Device)
		}
	}
	if len(devices) == 0 {
		// Nothing on this host forwards, so neither knob nor rule is ours to set.
		// The table is still cleared, or a deleted inbound would leave one behind.
		return dropMasquerade(ctx)
	}
	var failures []error
	if err := egressManager.EnsureForwarding(ctx); err != nil {
		failures = append(failures, err)
	}
	// Forwarding without translation is a tunnel that hands out addresses and
	// drops every packet: the client's in-tunnel source never survives upstream.
	// Fronts are exempt -- Xray re-originates that traffic, and translating it
	// first is what hid the client's address from every source criterion. F066.
	fronts, fErr := panelFrontDevices()
	if fErr != nil {
		failures = append(failures, fErr)
	}
	if err := egress.EnsureMasquerade(ctx, devices, fronts); err != nil && !errors.Is(err, egress.ErrNoNft) {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// panelFrontDevices names the xray-tun devices this panel owns. Panel-owned
// egress rows ARE the fronts: reapFronts removes one no rule names any more.
func panelFrontDevices() ([]string, error) {
	var ids []int
	err := database.GetDB().Model(&model.Egress{}).
		Where("owner = ?", model.EgressOwnerPanel).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, err
	}
	devices := make([]string, 0, len(ids))
	for _, id := range ids {
		devices = append(devices, egress.Device(id))
	}
	return devices, nil
}

// dropMasquerade removes the panel's nat table, tolerating a host that never
// had nft to begin with.
func dropMasquerade(ctx context.Context) error {
	if err := egress.EnsureMasquerade(ctx, nil, nil); err != nil && !errors.Is(err, egress.ErrNoNft) {
		return err
	}
	return nil
}

/*
Exits resolves the uplinks a rule may target, asking each core what its own
handle is.

Operator-owned rows only: a panel-owned row is a front, which is machinery for
an ingress and never a destination. The kind comes from the row's type, and the
handle from whichever core claims it, so an openvpn or ikev2 uplink appears here
by registering a driver and answering RoutableEgress -- nothing in this function
learns a protocol name.
*/
func (s *RoutingService) Exits(ctx context.Context) ([]routing.ResolvedExit, error) {
	rows, err := (&EgressService{}).GetAll()
	if err != nil {
		return nil, err
	}
	out := make([]routing.ResolvedExit, 0, len(rows))
	for _, row := range rows {
		if row == nil || !row.Enable || row.Owner == model.EgressOwnerPanel {
			continue
		}
		kind := exitKindFor(row.Type)
		if kind == "" {
			continue
		}
		/*
			A row whose own driver cannot derive the kernel state is not routable,
			whatever its core says. ExitHandle is unconditional for an uplink — it
			returns a device name without reading settings — so without this the
			planner emits a marked outbound while the manager, which zeroes a failed
			Fill and carries on, installs no rule to catch the mark. The packets
			leave with the server's own address.
		*/
		if err := egressDriverFills(row); err != nil {
			logger.Warning("routing: exit", row.Id, "is not routable:", err)
			continue
		}
		handleKind, handle, hErr := cores.ExitHandleFor(ctx, core.Exit{
			ID: row.Id, Kind: kind, Enable: row.Enable, Settings: row.Settings,
		})
		if hErr != nil || handleKind == core.ExitNone {
			continue
		}
		out = append(out, routing.ResolvedExit{
			ID: row.Id, Label: exitLabel(row), Kind: handleKind, Handle: handle,
		})
	}
	return out, nil
}

// exitKindFor maps an egress row's type to the core kind that serves it. The one
// place the two vocabularies meet, so a new uplink type is one line here.
func exitKindFor(rowType string) core.Kind {
	switch rowType {
	case wgclient.Type:
		return "wgkernel"
	case awg.UplinkDriverType:
		return "awgkernel"
	}
	return ""
}

// exitLabel is what the operator named it, falling back to something they can
// still recognise in a picker.
func exitLabel(row *model.Egress) string {
	if row.Remark != "" {
		return row.Remark
	}
	return fmt.Sprintf("%s #%d", row.Type, row.Id)
}

/*
RefusalError is a compile refusal on its way to the operator.

It carries the diag's own message key and args rather than a sentence, because
the panel ships two locales and the controller is where a key becomes text.
*/
type RefusalError struct {
	Key  string
	Args map[string]string
}

func (e *RefusalError) Error() string {
	if len(e.Args) == 0 {
		return e.Key
	}
	parts := make([]string, 0, len(e.Args))
	for name, value := range e.Args {
		parts = append(parts, name+"="+value)
	}
	sort.Strings(parts)
	return e.Key + " (" + strings.Join(parts, " ") + ")"
}

/*
refuseInertRule compiles a candidate rule set and refuses a save the planner
would drop.

Compiled.Refusals() had no production caller at all, so a rule the compiler
marks inert was stored enabled, shown as active, and then silently not emitted —
which for an exit rule means every client of that inbound leaves with the
server's own address instead of through the uplink, with nothing anywhere saying
so.

Nothing is provisioned here. The real compile upserts a front row per device
ingress; doing that for a rule that is about to be refused would leave the row
behind, so this answers FrontIDFor unconditionally. KeyNoFront cannot fire as a
result, which is correct: a missing front is an infrastructure failure the
operator cannot fix by editing the rule.
*/
func (s *RoutingService) refuseInertRule(ctx context.Context, candidate []*model.RoutingRule) error {
	subjects, err := s.Subjects(ctx)
	if err != nil {
		return err
	}
	exits, err := s.Exits(ctx)
	if err != nil {
		return err
	}
	blackhole, direct := outboundsByProtocol(templateOutbounds())
	compiled := routing.Plan(routing.Input{
		Rules: toPlanRules(candidate), Subjects: subjects, Exits: exits,
		Blackhole: blackhole, Direct: direct,
		FrontIDFor: func(int) (int, bool) { return 1, true },
	})
	for _, diag := range compiled.Refusals() {
		if diag.MessageKey == "" {
			continue
		}
		return &RefusalError{Key: diag.MessageKey, Args: diag.Args}
	}
	return nil
}

// templateOutbounds reads the operator's own outbound list, which is where
// direct and block resolve from. An unreadable one answers nothing, and the
// planner then refuses or synthesizes exactly as it does at generate time.
func templateOutbounds() []map[string]any {
	tmpl, err := (&SettingService{}).GetXrayConfigTemplate()
	if err != nil || tmpl == "" {
		return nil
	}
	var parsed struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if json.Unmarshal([]byte(tmpl), &parsed) != nil {
		return nil
	}
	return parsed.Outbounds
}

// candidateRules is the stored set with one rule added or replacing its own id,
// so a save is judged as the state it would produce.
func (s *RoutingService) candidateRules(rule *model.RoutingRule) ([]*model.RoutingRule, error) {
	stored, err := s.Rules()
	if err != nil {
		return nil, err
	}
	out := make([]*model.RoutingRule, 0, len(stored)+1)
	replaced := false
	for _, existing := range stored {
		if rule.Id != 0 && existing.Id == rule.Id {
			out = append(out, rule)
			replaced = true
			continue
		}
		out = append(out, existing)
	}
	if !replaced {
		out = append(out, rule)
	}
	return out, nil
}

/*
PruneOutbound repairs the rules that named an outbound the template no longer
carries.

The rule-save direction has always been guarded — destResolves refuses a rule
naming a tag that resolves to nothing. The outbound-CRUD direction had no guard
at all, so renaming or deleting an outbound in the Xray settings left every rule
pointing at it naming something that does not exist.

That used to be survivable and is not any more: the doc on destResolves said
Xray falls back to the first outbound, and in the pinned core it does not.
app/dispatcher/default.go logs "non existing outTag", closes the writer and
interrupts the reader, under a DO NOT CHANGE comment. So the rule's traffic is
dropped rather than misrouted, silently, for as long as the rule stands.

Same shape as PruneInbound and the same marker: the rule is disabled and its
remark says why, so nothing needs a new column and the operator can find it.
*/
func (s *RoutingService) PruneOutbound(ctx context.Context, present map[string]bool) (bool, error) {
	rules, err := s.Rules()
	if err != nil {
		return false, err
	}
	changed := false
	db := database.GetDB()
	for _, rule := range rules {
		if !rule.Enable || rule.DestTag == "" {
			continue
		}
		if rule.DestKind != model.RoutingDestOutbound && rule.DestKind != model.RoutingDestBalancer {
			continue
		}
		if present[rule.DestTag] {
			continue
		}
		updates := map[string]any{
			"enable": false,
			"remark": missingDestRemark(rule.Remark, rule.DestTag),
		}
		if err := db.Model(&model.RoutingRule{}).Where("id = ?", rule.Id).Updates(updates).Error; err != nil {
			return changed, err
		}
		logger.Warning("routing: rule", rule.Id, "was disabled; its destination", rule.DestTag, "is no longer in the template")
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, s.converge(ctx)
}

// missingDestRemark names the tag, because an operator looking at a disabled
// rule needs to know which outbound went away rather than that one did.
func missingDestRemark(existing, tag string) string {
	note := "disabled: its destination " + tag + " is no longer in the Xray template"
	if existing == "" {
		return note
	}
	return existing + " (" + note + ")"
}

// TemplateDestinations is every tag a rule may name as its destination: the
// template's own outbounds, plus its balancers.
func TemplateDestinations(template string) map[string]bool {
	out := map[string]bool{}
	var parsed struct {
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
		Routing struct {
			Balancers []struct {
				Tag string `json:"tag"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if json.Unmarshal([]byte(template), &parsed) != nil {
		return nil
	}
	for _, outbound := range parsed.Outbounds {
		if outbound.Tag != "" {
			out[outbound.Tag] = true
		}
	}
	for _, balancer := range parsed.Routing.Balancers {
		if balancer.Tag != "" {
			out[balancer.Tag] = true
		}
	}
	return out
}
