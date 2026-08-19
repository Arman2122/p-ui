package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
			CriteriaMask: criteriaMaskFor(handle),
		})
	}
	return out, nil
}

/*
criteriaMaskFor names the criteria that can actually work on one subject.

`user` is absent for a device ingress and that is not a policy choice: Xray's tun
handler builds a MemoryUser with no Email, so UserMatcher returns false for every
packet. Offering the field would produce a rule that saves and never matches,
which is the whole class of bug this feature exists to remove.
*/
func criteriaMaskFor(handle core.IngressHandle) []string {
	base := []string{"ip", "port", "sourcePort", "network", "source", "attrs"}
	if handle.Device != "" {
		// Fronted traffic reaches Xray without an identity, and without sniffing it
		// carries no name either. Both are P2 work; until then, say so.
		return base
	}
	return append(base, "user", "domain", "protocol")
}

// compile resolves everything the plan needs and runs it. Front ids are
// allocated first, so the compile stays pure and repeatable.
func (s *RoutingService) compile(ctx context.Context, cfgOutbounds []map[string]any) (routing.Compiled, error) {
	rules, err := s.Rules()
	if err != nil {
		return routing.Compiled{}, err
	}
	subjects, err := s.Subjects(ctx)
	if err != nil {
		return routing.Compiled{}, err
	}
	fronts, err := s.ensureFronts(subjects, rules)
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
func (s *RoutingService) ensureFronts(subjects []routing.Subject, rules []*model.RoutingRule) (map[int]int, error) {
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
	return out, s.reapFronts(out)
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
	for _, row := range rows {
		if row.IngressInboundId == nil {
			continue
		}
		if id, wanted := keep[*row.IngressInboundId]; wanted && id == row.Id {
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
			Dest: routing.Dest{Kind: rule.DestKind, Tag: rule.DestTag, ExitID: exitID},
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
func applyRoutingPlan(ctx context.Context, cfg *xray.Config) error {
	var outbounds []map[string]any
	if len(cfg.OutboundConfigs) > 0 {
		_ = json.Unmarshal(cfg.OutboundConfigs, &outbounds)
	}
	compiled, err := (&RoutingService{}).compile(ctx, outbounds)
	if err != nil {
		return err
	}
	if len(compiled.XrayRules) == 0 && len(compiled.Fronts) == 0 {
		return nil
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
		cfg.InboundConfigs = append(cfg.InboundConfigs, inbound)
	}

	if len(compiled.Outbounds) > 0 {
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
	var next int
	if err := database.GetDB().Model(&model.RoutingRule{}).
		Select("COALESCE(MAX(sort_index), -1) + 1").Scan(&next).Error; err != nil {
		return nil, err
	}
	rule.SortIndex = next
	if err := database.GetDB().Create(rule).Error; err != nil {
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

Xray does not fail an unknown outboundTag — it falls back to the first outbound,
so a rule left pointing at a deleted or renamed one keeps matching and sends that
traffic somewhere the operator never chose. Shape validation cannot see this: the
tag is a non-empty string either way.

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

// ReplaceInboundRule expresses "send this whole inbound there" as one rule,
// replacing whatever single-subject rules that inbound already had.
func (s *RoutingService) ReplaceInboundRule(ctx context.Context, inboundID int, destTag string) error {
	if err := s.ClearInbound(ctx, inboundID); err != nil {
		return err
	}
	ids, err := json.Marshal([]int{inboundID})
	if err != nil {
		return err
	}
	_, err = s.Add(ctx, &model.RoutingRule{
		Enable: true, IngressScope: model.RoutingScopeSelected, IngressIds: string(ids),
		DestKind: model.RoutingDestOutbound, DestTag: destTag, Criteria: "{}",
	})
	return err
}

// ClearInbound drops the rules that name ONLY this inbound, which is what
// detaching used to mean. A rule naming others keeps them.
func (s *RoutingService) ClearInbound(ctx context.Context, inboundID int) error {
	rules, err := s.Rules()
	if err != nil {
		return err
	}
	db := database.GetDB()
	for _, rule := range rules {
		var ids []int
		if json.Unmarshal([]byte(rule.IngressIds), &ids) != nil {
			continue
		}
		if len(ids) != 1 || ids[0] != inboundID {
			continue
		}
		if err := db.Delete(&model.RoutingRule{}, rule.Id).Error; err != nil {
			return err
		}
	}
	return s.converge(ctx)
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
	if err := egress.EnsureMasquerade(ctx, devices); err != nil && !errors.Is(err, egress.ErrNoNft) {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// dropMasquerade removes the panel's nat table, tolerating a host that never
// had nft to begin with.
func dropMasquerade(ctx context.Context) error {
	if err := egress.EnsureMasquerade(ctx, nil); err != nil && !errors.Is(err, egress.ErrNoNft) {
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
