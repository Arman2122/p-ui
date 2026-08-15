package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/policy"
	"github.com/Arman2122/p-ui/internal/shaping"
	"github.com/Arman2122/p-ui/internal/web/runtime"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

/*
The join. This is the only file where the rules and the mechanism are both
legal: internal/policy holds the product rules and may not know what a kernel
is, internal/shaping drives the kernel and may not know what a client is, and
neither imports the other.

It asks the registry what a core can do and never asks what protocol it serves,
which is why an arch guard forbids this file naming one.
*/

/*
shapingNamespaces is which device namespaces this panel may shape.

One set, shared with the manager, because "may I shape this device" has to have
exactly one answer: a core that brings its own device band registers it here at
wiring time, and both the refusal below and the manager's own planning read the
same registry rather than two copies of a list.
*/
var shapingNamespaces = shaping.DefaultNamespaces()

// shapingManager owns every qdisc, class and filter the ladders install. One,
// for egressManager's reason: two writers to one host's kernel state disagree.
var shapingManager = shaping.NewManager(shaping.HostPlane(), shapingNamespaces)

// PolicyService evaluates the panel's product rules against what the cores
// report, and converges the kernel on the answer.
type PolicyService struct {
	inboundService InboundService
}

// ipLimitLiveWindow is how long an address a master synced but this node did not
// observe still counts against the cap. It is what makes the cap cluster-wide.
const ipLimitLiveWindow = 120 * time.Second

// ipLimitStaleAfter drops an address from the persisted row once nothing has
// been seen from it for this long.
const ipLimitStaleAfter = 30 * time.Minute

// registry answers what each core can do. Read per call because the router is
// built before the panel wires one, exactly as CoreViews does.
func registry() *core.Registry {
	manager := runtime.GetManager()
	if manager == nil {
		return nil
	}
	return manager.Cores()
}

// Plans reads every ladder this panel holds, keyed by id. A ladder that will not
// parse is dropped rather than guessed at: a malformed plan must never throttle.
func Plans(db *gorm.DB) (map[int]policy.Plan, error) {
	var rows []model.Policy
	if err := db.Model(&model.Policy{}).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int]policy.Plan, len(rows))
	for _, row := range rows {
		plan, err := ParsePlan(row.Tiers)
		if err != nil {
			logger.Warning("policy: plan", row.Id, "has an unreadable ladder and is treated as unlimited:", err)
			continue
		}
		out[row.Id] = plan
	}
	return out, nil
}

// ParsePlan reads a stored ladder. An empty column is an empty plan, which
// Evaluate answers as unlimited.
func ParsePlan(tiers string) (policy.Plan, error) {
	if tiers == "" {
		return policy.Plan{}, nil
	}
	var parsed []policy.Tier
	if err := json.Unmarshal([]byte(tiers), &parsed); err != nil {
		return policy.Plan{}, err
	}
	return policy.Plan{Tiers: parsed}, nil
}

// SortTiers orders a ladder and drops repeated thresholds, so every pass reads a
// canonical value and Evaluate never has to sort.
func SortTiers(tiers []policy.Tier) []policy.Tier {
	out := slices.Clone(tiers)
	sort.SliceStable(out, func(i, j int) bool { return out[i].FromBytes < out[j].FromBytes })
	return slices.CompactFunc(out, func(a, b policy.Tier) bool { return a.FromBytes == b.FromBytes })
}

// clientFacts is what one client's rules are evaluated against.
type clientFacts struct {
	usedBytes int64
	plan      policy.Plan
	// assigned but unresolved: the row names a plan that is gone. Fail open and
	// report, never fall back to the strictest tier.
	unresolved bool
}

/*
factsFor reads usage and plan assignment for the named clients in one query.

Usage comes from UsedBytesExpr, the same expression the depletion scan binds, so
a client cannot be over quota by one definition and under a threshold by another.
*/
func factsFor(db *gorm.DB, emails []string, plans map[int]policy.Plan) (map[string]clientFacts, error) {
	out := make(map[string]clientFacts, len(emails))
	if len(emails) == 0 {
		return out, nil
	}
	used, usedArgs := UsedBytesExpr(db)
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []struct {
			Email     string
			UsedBytes int64
			PolicyId  *int
			// Assigned is the joined row's own key, so a surviving assignment can be
			// told from no assignment at all once the FK has nulled its plan.
			Assigned *string
		}
		err := db.Table("client_traffics").
			Select("client_traffics.email AS email, "+used+" AS used_bytes, "+
				"client_policies.policy_id AS policy_id, client_policies.email AS assigned", usedArgs...).
			Joins("LEFT JOIN client_policies ON client_policies.email = client_traffics.email").
			Where("client_traffics.email IN ?", batch).
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			facts := clientFacts{usedBytes: row.UsedBytes}
			switch {
			case row.PolicyId != nil:
				plan, known := plans[*row.PolicyId]
				facts.plan, facts.unresolved = plan, !known
			case row.Assigned != nil:
				// The row outlived its plan: the FK is ON DELETE SET NULL precisely so
				// this stays visible, and reading it as "never assigned" throws that away.
				facts.unresolved = true
			}
			out[row.Email] = facts
		}
	}
	return out, nil
}

// shapeableKinds names the kinds one core can give a kernel identity. A core
// that shapes nothing contributes none and is never asked for a target.
func shapeableKinds(bound *core.Bound) []core.Kind {
	if bound.Shape == nil {
		return nil
	}
	var out []core.Kind
	for _, kind := range bound.Core.Kinds() {
		if bound.Shape.ShapingSelector(kind) != core.SelectorNone {
			out = append(out, kind)
		}
	}
	return out
}

// deviceSubjects is one device's shapeable population before the rules are applied.
type deviceSubjects struct {
	device string
	keys   map[string]core.SubjectKey
}

/*
ShapingWants turns what the cores report into what the kernel should hold.

Every shapeable device is listed even when nothing on it is limited: a device
missing from the want is a device whose stranded tree is never torn down, and
the mirror GC only sees the devices it is handed.

A NIL answer means no core here can shape at all, so nothing is converged. An
EMPTY one is a different statement — something could shape and nothing is wanted
— and it must still be converged, because that is precisely when the mirror of a
deleted inbound is the thing left to collect.
*/
func (s *PolicyService) ShapingWants(ctx context.Context) ([]shaping.DeviceWant, error) {
	reg := registry()
	if reg == nil {
		return nil, nil
	}

	var failures []error
	var targets []deviceSubjects
	shapeable := false
	for _, bound := range reg.Cores() {
		kinds := shapeableKinds(bound)
		if len(kinds) == 0 {
			continue
		}
		shapeable = true
		instances, err := s.inboundService.DesiredInstances(kinds)
		if err != nil {
			failures = append(failures, fmt.Errorf("policy: desired state for %s: %w", bound.Core.Describe().ID, err))
			continue
		}
		for _, inst := range instances {
			target, err := bound.Shape.ShapingTargets(ctx, inst)
			if err != nil {
				failures = append(failures, fmt.Errorf("policy: identities for inbound %d: %w", inst.ID, err))
				continue
			}
			// An empty device is the core saying it is not hosting this instance
			// right now. Shaping goes quiet and retries; it does not fail.
			if target.Device == "" {
				continue
			}
			// Named here rather than left to the plane: the refusal is a fact about
			// the CORE's device naming, and the mechanism only ever sees a string.
			if !shapingNamespaces.Owns(target.Device) {
				failures = append(failures, fmt.Errorf(
					"policy: core %s offered device %q for inbound %d, which is outside the namespaces this panel may shape: %w",
					bound.Core.Describe().ID, target.Device, inst.ID, shaping.ErrNotOwned))
				continue
			}
			targets = append(targets, deviceSubjects{device: target.Device, keys: target.Keys})
		}
	}
	if !shapeable {
		return nil, errors.Join(failures...)
	}
	if len(targets) == 0 {
		// Wanting nothing is still an answer, and it is the one that reaps the
		// mirror device a deleted inbound leaves behind.
		return []shaping.DeviceWant{}, errors.Join(failures...)
	}

	emails := make([]string, 0)
	for _, target := range targets {
		for email := range target.keys {
			emails = append(emails, email)
		}
	}
	sort.Strings(emails)
	emails = slices.Compact(emails)

	db := database.GetDB()
	plans, err := Plans(db)
	if err != nil {
		return nil, errors.Join(append(failures, err)...)
	}
	facts, err := factsFor(db, emails, plans)
	if err != nil {
		return nil, errors.Join(append(failures, err)...)
	}

	wants := make([]shaping.DeviceWant, 0, len(targets))
	for _, target := range targets {
		want := shaping.DeviceWant{Device: target.device}
		for _, email := range slices.Sorted(maps.Keys(target.keys)) {
			fact, known := facts[email]
			if !known {
				continue
			}
			if fact.unresolved {
				// Debug, not Warning: this pass runs every 10s forever, and what an
				// operator acts on is EnforcedFor's Unresolved, which the panel shows.
				logger.Debug("policy: client", email, "is assigned a plan that no longer exists and is left unshaped")
				continue
			}
			limits := policy.Evaluate(fact.plan, fact.usedBytes)
			if limits == (policy.Limits{}) {
				// Cold-building a class costs real time and most clients have no
				// ladder at all, so only a client with a rate gets kernel state.
				continue
			}
			want.Subjects = append(want.Subjects, shaping.Subject{
				ID:     email,
				Keys:   shapingKeys(target.keys[email]),
				Limits: shaping.Limits{UpBps: limits.UpBps, DownBps: limits.DownBps},
			})
		}
		wants = append(wants, want)
	}
	return wants, errors.Join(failures...)
}

// shapingKeys carries one client's kernel identity across the fence. The two
// packages describe the same thing and neither may name the other's type.
func shapingKeys(key core.SubjectKey) []shaping.Key {
	out := make([]shaping.Key, 0, len(key.Prefixes))
	for _, prefix := range key.Prefixes {
		out = append(out, shaping.Key{Prefix: prefix})
	}
	if len(out) == 0 && key.Mark != 0 {
		out = append(out, shaping.Key{Mark: key.Mark})
	}
	return out
}

/*
ConvergeShaping drives the kernel to the ladders the plans describe.

Level-triggered: the tier is recomputed from committed usage every pass and
diffed against what the kernel reports, never against a panel-side cursor. That
is what makes a traffic reset, a plan edit and usage arriving from a remote node
all land without any of them having a hook.

ANY failure to read the wanted state aborts the whole pass. A device left out
because its core could not be asked is indistinguishable from a device nobody
wants shaped, and converging on that view would reap its mirror.
*/
func (s *PolicyService) ConvergeShaping(ctx context.Context) error {
	wants, err := s.ShapingWants(ctx)
	if err != nil {
		return err
	}
	// nil, not empty: no core here can shape, so nothing was ever installed and
	// there is nothing to collect. See ShapingWants on why the two differ.
	if wants == nil {
		return nil
	}
	return shapingManager.Converge(ctx, wants)
}

// Sighting is one address as reported for one client. It is the key enforcement
// is aimed with: which core saw a breach decides which core may be cut for it.
type Sighting struct{ Email, IP string }

// SessionScan is what the cores could tell us this pass, and which of them could
// not be asked.
type SessionScan struct {
	ByEmail map[string][]policy.Observation
	// Observers keeps the attribution the merge would otherwise throw away: one
	// client's addresses can come from several cores and only some are over a cap.
	Observers map[Sighting][]core.Kind
	Silent    []string
}

/*
ObserveSessions asks every core that can name its live connections.

Fail-open PER CORE, which is the repair that matters: today one core being down
skips the entire run, so a false ban is impossible but so is any enforcement at
all. A core whose Sessions() fails contributes nothing and its clients are simply
not evaluated this pass, while every other core's clients still are.
*/
func (s *PolicyService) ObserveSessions(ctx context.Context) SessionScan {
	scan := SessionScan{
		ByEmail:   map[string][]policy.Observation{},
		Observers: map[Sighting][]core.Kind{},
	}
	reg := registry()
	if reg == nil {
		return scan
	}
	nowMilli := time.Now().UnixMilli()
	for _, bound := range reg.Cores() {
		if bound.Sessions == nil {
			continue
		}
		reporter := bound.Core.Describe().ID
		id := string(reporter)
		sessions, err := bound.Sessions.Sessions(ctx)
		if err != nil {
			logger.Debug("policy: core", id, "could not name its sessions this pass:", err)
			scan.Silent = append(scan.Silent, id)
			continue
		}
		for _, session := range sessions {
			if session.Email == "" || !session.Source.IsValid() {
				continue
			}
			seen := session.LastSeenUnixMilli
			if seen <= 0 {
				// The core cannot say, so the observation is stamped now: it is
				// reporting the connection as live by reporting it at all.
				seen = nowMilli
			}
			address := session.Source.String()
			scan.ByEmail[session.Email] = append(scan.ByEmail[session.Email],
				policy.Observation{IP: address, LastSeenUnixMilli: seen})
			sighting := Sighting{Email: session.Email, IP: address}
			if !slices.Contains(scan.Observers[sighting], reporter) {
				scan.Observers[sighting] = append(scan.Observers[sighting], reporter)
			}
		}
	}
	return scan
}

// IPLimitVerdict is one client's answer for this pass. Ban is what the caller
// reports; Bounce says whether cutting the client's sessions is safe for its core.
type IPLimitVerdict struct {
	Email   string
	Ban     []policy.Observation
	Bounce  bool
	Inbound *model.Inbound
}

/*
EvaluateIPLimits merges what the cores reported into the persisted rows, applies
each client's cap, and writes the result back.

Enforcement is deliberately the caller's: the fail2ban line and any bounce are
network and filesystem work, and running them inside this write transaction is
how a scan's lock is held across a round trip.
*/
func (s *PolicyService) EvaluateIPLimits(scan SessionScan, enforce bool) ([]IPLimitVerdict, error) {
	emails := make([]string, 0, len(scan.ByEmail))
	for email := range scan.ByEmail {
		emails = append(emails, email)
	}
	sort.Strings(emails)
	if len(emails) == 0 {
		return nil, nil
	}

	db := database.GetDB()
	limits := s.inboundService.limitIPByEmail(emails)
	inbounds := s.inboundService.inboundsByEmail(emails)
	persisted := s.inboundService.clientIPRows(emails)

	nowSeconds := time.Now().Unix()
	nowMilli := nowSeconds * 1000
	staleCutoff := nowMilli - ipLimitStaleAfter.Milliseconds()

	attribution := make(map[string][]model.ClientIpEntry, len(emails))
	var verdicts []IPLimitVerdict

	tx := db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	for _, email := range emails {
		observed := scan.ByEmail[email]
		hosted := inbounds[email]
		if len(hosted) == 0 {
			// The observation names a client that was renamed or deleted; drop the
			// orphaned tracking row instead of recreating it every run (#4963).
			logger.Debugf("policy: skipping stale observed email %q (renamed or deleted)", email)
			if err := tx.Where("client_email = ?", email).Delete(&model.InboundClientIps{}).Error; err != nil {
				logger.Warning("policy: drop orphaned ip row:", err)
			}
			continue
		}

		// Stamped now, not at the connection's start: the stale cutoff would
		// otherwise evict an address that is connected this second.
		entries := make([]model.ClientIpEntry, 0, len(observed))
		for _, obs := range observed {
			entries = append(entries, model.ClientIpEntry{IP: obs.IP, Timestamp: nowSeconds})
		}
		if len(entries) > 0 {
			attribution[email] = entries
		}

		row, tracked := persisted[email]
		if !tracked {
			row = &model.InboundClientIps{ClientEmail: email}
		}
		limit := limits[email]
		if !enforce || limit <= 0 || !hosted[0].Enable {
			// Nothing to enforce: record what was seen so the panel can show it.
			row.Ips = encodeClientIPs(observationsToEntries(observed))
			if err := tx.Save(row).Error; err != nil {
				logger.Error("policy: save observed ips:", err)
			}
			continue
		}

		verdict := policy.Decide(decodeClientIPs(row.Ips), observed, limit,
			nowMilli, staleCutoff, ipLimitLiveWindow, true)

		// Kept-live plus historical stays in the blob so the panel keeps showing
		// recently seen addresses; a banned one reappears if it reconnects.
		row.Ips = encodeClientIPs(observationsToEntries(append(slices.Clone(verdict.Keep), verdict.Retain...)))
		if err := tx.Save(row).Error; err != nil {
			logger.Error("policy: save ip limit result:", err)
			continue
		}
		// Every client the cap was actually applied to, an EMPTY ban included: the
		// caller's re-ban memory is pruned by being handed the clients that are back
		// under their cap, and a verdict it never sees can never be forgotten.
		target := s.bounceTarget(hosted, scan, email, verdict.Ban)
		verdicts = append(verdicts, IPLimitVerdict{
			Email:   email,
			Ban:     verdict.Ban,
			Bounce:  target != nil,
			Inbound: target,
		})
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	committed = true

	s.recordAttribution(attribution)
	return verdicts, nil
}

/*
bounceTarget picks the inbound whose core actually observed the banned addresses.

Cutting a client is per-CORE and not per-client: the runtime resolves the core
from the inbound it is handed, and RemoveUser drops ALL of that client's sessions
on it. Choosing by id instead would cut a compliant core's connections while the
core that saw the breach carries on, and would do it silently — the retired job's
protocol allowlist was the only thing that ever stopped a cross-core cut.

No observing core that can safely be cut means report-only, which is the same
answer the fail2ban line already carries.
*/
func (s *PolicyService) bounceTarget(hosted []*model.Inbound, scan SessionScan, email string, banned []policy.Observation) *model.Inbound {
	reg := registry()
	if reg == nil || len(banned) == 0 {
		return nil
	}
	saw := make(map[core.Kind]bool, len(banned))
	for _, over := range banned {
		for _, reporter := range scan.Observers[Sighting{Email: email, IP: over.IP}] {
			saw[reporter] = true
		}
	}
	for _, inbound := range hosted {
		bound, known := reg.For(core.Kind(inbound.Protocol))
		if !known || !saw[bound.Core.Describe().ID] {
			continue
		}
		if bounceable(inbound, bound) {
			return inbound
		}
	}
	return nil
}

/*
bounceable reports whether cutting a client's sessions is a safe way to enforce
a cap on this inbound's core.

The question is what a removal COSTS, and only the core can answer it: for a
wgkernel peer a remove ZEROES the byte counters the panel bills from, so losing
real money to a soft product rule is never worth it. That is deliberately not
inferred from whether the core can shape — two orthogonal properties that happen
to coincide on the one core doing both today.
*/
func bounceable(inbound *model.Inbound, bound *core.Bound) bool {
	if inbound == nil || inbound.NodeID != nil {
		// A node's inbound is served by a core this host never ran, so its tag
		// names nothing here and the cut would have to travel as a node push.
		return false
	}
	if bound == nil || bound.Users == nil {
		return false
	}
	if bound.CounterLoss == nil {
		return true
	}
	return !bound.CounterLoss.RemovalLosesCounters(core.Kind(inbound.Protocol))
}

/*
BounceClient drops a client's live connections by removing and re-adding it.

It goes through the runtime rather than straight to a core's API, which is what
removes both the layering violation and the protocol switch the old job carried:
any core with a user provisioner is served by the same three lines.
*/
func (s *PolicyService) BounceClient(ctx context.Context, inbound *model.Inbound, email string) error {
	rt, err := s.inboundService.runtimeFor(inbound)
	if err != nil {
		return err
	}
	if err := rt.RemoveUser(ctx, inbound, email); err != nil {
		return err
	}
	// The daemon needs a moment to drop the sockets before the user is let back
	// in, or the re-add races the disconnect it exists to force.
	time.Sleep(100 * time.Millisecond)
	return rt.AddUser(ctx, inbound, email)
}

// recordAttribution files this pass's observations under this panel's guid, so a
// master can say which node an address is on. Advisory: it never blocks a ban.
func (s *PolicyService) recordAttribution(attribution map[string][]model.ClientIpEntry) {
	if len(attribution) == 0 {
		return
	}
	guid, err := (&SettingService{}).GetPanelGuid()
	if err != nil || guid == "" {
		return
	}
	if err := s.inboundService.RecordLocalClientIps(guid, attribution); err != nil {
		logger.Debug("policy: record local ip attribution failed:", err)
	}
}

// AnyClientHasAnIPLimit probes the normalized clients table, so a panel with no
// limits configured never pays for the rest of the pass.
func AnyClientHasAnIPLimit() bool {
	var probe int64
	err := database.GetDB().Model(&model.ClientRecord{}).Where("limit_ip > 0").Limit(1).Count(&probe).Error
	return err == nil && probe > 0
}

// The persisted blob is unix SECONDS and is synced between panels, so its unit
// is a compatibility contract; the rules work in milliseconds.
func observationsToEntries(observations []policy.Observation) []model.ClientIpEntry {
	out := make([]model.ClientIpEntry, 0, len(observations))
	for _, obs := range observations {
		out = append(out, model.ClientIpEntry{IP: obs.IP, Timestamp: obs.LastSeenUnixMilli / 1000})
	}
	return out
}

func decodeClientIPs(blob string) []policy.Observation {
	if blob == "" {
		return nil
	}
	var entries []model.ClientIpEntry
	if err := json.Unmarshal([]byte(blob), &entries); err != nil {
		return nil
	}
	out := make([]policy.Observation, 0, len(entries))
	for _, entry := range entries {
		out = append(out, policy.Observation{IP: entry.IP, LastSeenUnixMilli: entry.Timestamp * 1000})
	}
	return out
}

func encodeClientIPs(entries []model.ClientIpEntry) string {
	blob, err := json.Marshal(entries)
	if err != nil {
		return "[]"
	}
	return string(blob)
}

// limitIPByEmail maps each client to its cap in a few chunked queries.
func (s *InboundService) limitIPByEmail(emails []string) map[string]int {
	db := database.GetDB()
	out := make(map[string]int, len(emails))
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []struct {
			Email   string
			LimitIp int
		}
		if err := db.Model(&model.ClientRecord{}).
			Select("email, limit_ip").
			Where("email IN ?", batch).
			Scan(&rows).Error; err != nil {
			logger.Warning("policy: read ip limits:", err)
			continue
		}
		for _, row := range rows {
			out[row.Email] = row.LimitIp
		}
	}
	return out
}

/*
inboundsByEmail resolves each observed client to EVERY inbound that carries it,
local ones first and then by id.

Every one of them, not the best one: a client can be attached to two cores and
only one of them observed the addresses being banned, so the caller picks by
attribution rather than by a sort order. The first entry is still the local,
lowest-id inbound, which is the one whose enable flag governs the client here.
*/
func (s *InboundService) inboundsByEmail(emails []string) map[string][]*model.Inbound {
	db := database.GetDB()
	type candidate struct {
		id    int
		local bool
	}
	found := make(map[string][]candidate, len(emails))
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var pairs []struct {
			Email     string
			InboundId int
			NodeID    *int `gorm:"column:node_id"`
		}
		if err := db.Table("client_inbounds").
			Select("clients.email AS email, client_inbounds.inbound_id AS inbound_id, inbounds.node_id AS node_id").
			Joins("JOIN clients ON clients.id = client_inbounds.client_id").
			Joins("JOIN inbounds ON inbounds.id = client_inbounds.inbound_id").
			Where("clients.email IN ?", batch).
			Scan(&pairs).Error; err != nil {
			logger.Warning("policy: resolve inbounds:", err)
			return nil
		}
		for _, pair := range pairs {
			found[pair.Email] = append(found[pair.Email],
				candidate{id: pair.InboundId, local: pair.NodeID == nil})
		}
	}

	ids := make([]int, 0, len(found))
	for _, picks := range found {
		// Local first, then by id: the head is what a caller needing exactly one
		// inbound has always been given.
		slices.SortFunc(picks, func(a, b candidate) int {
			if a.local != b.local {
				if a.local {
					return -1
				}
				return 1
			}
			return a.id - b.id
		})
		for _, pick := range picks {
			if !slices.Contains(ids, pick.id) {
				ids = append(ids, pick.id)
			}
		}
	}
	slices.Sort(ids)
	byID := make(map[int]*model.Inbound, len(ids))
	for _, batch := range chunkInts(ids, sqlInChunk) {
		var page []*model.Inbound
		if err := db.Model(&model.Inbound{}).Where("id IN ?", batch).Find(&page).Error; err != nil {
			logger.Warning("policy: load inbounds:", err)
			return nil
		}
		for _, inbound := range page {
			byID[inbound.Id] = inbound
		}
	}
	out := make(map[string][]*model.Inbound, len(found))
	for email, picks := range found {
		for _, pick := range picks {
			if inbound, ok := byID[pick.id]; ok {
				out[email] = append(out[email], inbound)
			}
		}
	}
	// A client the relation does not carry yet is resolved one at a time, which is
	// why the relation is tried first: this scan parses every candidate's settings.
	for _, email := range emails {
		if len(out[email]) > 0 {
			continue
		}
		if inbound, ok := s.inboundBySettingsEmail(email); ok {
			out[email] = []*model.Inbound{inbound}
		}
	}
	return out
}

/*
inboundBySettingsEmail finds the inbound whose settings JSON carries this client.

The LIKE is a candidate filter and never the answer: a substring match hits an
email that merely contains another, or text that happens to appear elsewhere in
the blob, so every candidate's client list is parsed before one is accepted.
*/
func (s *InboundService) inboundBySettingsEmail(email string) (*model.Inbound, bool) {
	var candidates []model.Inbound
	if err := database.GetDB().Model(&model.Inbound{}).
		Where("settings LIKE ?", "%"+email+"%").Find(&candidates).Error; err != nil {
		return nil, false
	}
	for i := range candidates {
		clients, err := ParseInboundSettingsClients(candidates[i].Settings)
		if err != nil {
			continue
		}
		for _, client := range clients {
			if client.Email == email {
				return &candidates[i], true
			}
		}
	}
	return nil, false
}

func (s *InboundService) clientIPRows(emails []string) map[string]*model.InboundClientIps {
	db := database.GetDB()
	out := make(map[string]*model.InboundClientIps, len(emails))
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []model.InboundClientIps
		if err := db.Where("client_email IN ?", batch).Find(&rows).Error; err != nil {
			logger.Warning("policy: read tracked ips:", err)
			continue
		}
		for i := range rows {
			out[rows[i].ClientEmail] = &rows[i]
		}
	}
	return out
}

// The refusals an operator has to be able to tell apart, and a test has to be
// able to match without comparing a sentence.
var (
	ErrPolicyUnknown    = errors.New("policy: no plan with that id")
	ErrPolicyNameTaken  = errors.New("policy: a plan with that name already exists")
	ErrPolicyBadLadder  = errors.New("policy: the tier ladder is not readable")
	ErrPolicyNoSuchUser = errors.New("policy: no client with that email")
)

func (s *PolicyService) GetAll() ([]*model.Policy, error) {
	var rows []*model.Policy
	err := database.GetDB().Model(&model.Policy{}).Order("id").Find(&rows).Error
	return rows, err
}

func (s *PolicyService) Get(id int) (*model.Policy, error) {
	var row model.Policy
	if err := database.GetDB().Where("id = ?", id).First(&row).Error; err != nil {
		return nil, fmt.Errorf("%w: %d", ErrPolicyUnknown, id)
	}
	return &row, nil
}

// canonicalTiers sorts and de-duplicates on write, so every pass reads a ladder
// it can scan without sorting and two equal thresholds cannot both be stored.
func canonicalTiers(row *model.Policy) error {
	plan, err := ParsePlan(row.Tiers)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPolicyBadLadder, err)
	}
	blob, err := json.Marshal(SortTiers(plan.Tiers))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPolicyBadLadder, err)
	}
	row.Tiers = string(blob)
	return nil
}

func (s *PolicyService) Add(row *model.Policy) (*model.Policy, error) {
	if err := canonicalTiers(row); err != nil {
		return nil, err
	}
	row.Id = 0
	if err := database.GetDB().Create(row).Error; err != nil {
		return nil, fmt.Errorf("%w: %q", ErrPolicyNameTaken, row.Name)
	}
	return row, nil
}

// Update replaces the editable half. Nothing here touches a core's config, so a
// plan edit provably cannot restart a daemon or drop a connection.
func (s *PolicyService) Update(row *model.Policy) (*model.Policy, error) {
	if _, err := s.Get(row.Id); err != nil {
		return nil, err
	}
	if err := canonicalTiers(row); err != nil {
		return nil, err
	}
	err := database.GetDB().Model(&model.Policy{}).Where("id = ?", row.Id).
		Updates(map[string]any{"name": row.Name, "tiers": row.Tiers}).Error
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrPolicyNameTaken, row.Name)
	}
	return s.Get(row.Id)
}

func (s *PolicyService) Del(id int) error {
	if _, err := s.Get(id); err != nil {
		return err
	}
	return database.GetDB().Delete(&model.Policy{}, id).Error
}

/*
Assign puts one client on a plan; a policyId of 0 takes them off it.

Keyed by email like the client's quota, and deliberately not by client id: a
deleted-and-resynced client keeps the same email and a new id, so an id-keyed
assignment would vanish and drop a paying customer back to no plan.
*/
func (s *PolicyService) Assign(email string, policyID int) error {
	db := database.GetDB()
	var clients int64
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", email).Count(&clients).Error; err != nil {
		return err
	}
	if clients == 0 {
		return fmt.Errorf("%w: %q", ErrPolicyNoSuchUser, email)
	}
	if policyID == 0 {
		return db.Where("email = ?", email).Delete(&model.ClientPolicy{}).Error
	}
	if _, err := s.Get(policyID); err != nil {
		return err
	}
	row := model.ClientPolicy{Email: email, PolicyId: &policyID}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "email"}},
		DoUpdates: clause.AssignmentColumns([]string{"policy_id", "updated_at"}),
	}).Create(&row).Error
}

/*
movePolicyAssignment carries a plan across a client rename.

The assignment is keyed by email so it survives the hard-delete-and-recreate a
node re-sync performs, which is exactly why a rename has to move it by hand: the
quota beside it is migrated the same way, and a client whose plan silently
vanished runs at full line rate with nothing on any screen saying so.
*/
func movePolicyAssignment(tx *gorm.DB, oldEmail, newEmail string) error {
	if oldEmail == newEmail {
		return nil
	}
	// The caller only renames onto a free identity, so a row already sitting there
	// is stale — the same rule UpdateClientIPs applies to the tracking row.
	if err := tx.Where("email = ?", newEmail).Delete(&model.ClientPolicy{}).Error; err != nil {
		return err
	}
	return tx.Model(&model.ClientPolicy{}).Where("email = ?", oldEmail).Update("email", newEmail).Error
}

// EnforcedLimits is one client's answer: the plan the rules derive, and what the
// kernel is actually holding. They differ exactly when something did not land.
type EnforcedLimits struct {
	Email string `json:"email" example:"user1"`
	// UsedBytes is the same number the quota is enforced against.
	UsedBytes int64 `json:"usedBytes" example:"53687091200"`
	// PolicyId is 0 when the client is on no plan; Unresolved means the row names
	// a plan that no longer exists, which never throttles and must be reported.
	PolicyId   int  `json:"policyId" example:"1"`
	Unresolved bool `json:"unresolved" example:"false"`
	// Shapeable reports whether the kernel holds state for this client NOW, which
	// needs a core that can identify them and a plan granting them a rate.
	Shapeable       bool  `json:"shapeable" example:"true"`
	WantUpBps       int64 `json:"wantUpBps" example:"10000000"`
	WantDownBps     int64 `json:"wantDownBps" example:"10000000"`
	EnforcedUpBps   int64 `json:"enforcedUpBps" example:"10000000"`
	EnforcedDownBps int64 `json:"enforcedDownBps" example:"10000000"`
}

/*
EnforcedFor reports one client's rules beside what the kernel holds for them.

The enforced half is read out of the kernel rather than remembered, because the
value the panel pushed is not evidence that anything is limited: a class that was
never installed and a class installed correctly look identical from the caller's side.
*/
func (s *PolicyService) EnforcedFor(ctx context.Context, email string) (EnforcedLimits, error) {
	out := EnforcedLimits{Email: email}
	db := database.GetDB()
	plans, err := Plans(db)
	if err != nil {
		return out, err
	}
	facts, err := factsFor(db, []string{email}, plans)
	if err != nil {
		return out, err
	}
	fact, known := facts[email]
	if !known {
		return out, fmt.Errorf("%w: %q", ErrPolicyNoSuchUser, email)
	}
	out.UsedBytes, out.Unresolved = fact.usedBytes, fact.unresolved

	var assignment model.ClientPolicy
	if err := db.Where("email = ?", email).First(&assignment).Error; err == nil && assignment.PolicyId != nil {
		out.PolicyId = *assignment.PolicyId
	}
	if !fact.unresolved {
		limits := policy.Evaluate(fact.plan, fact.usedBytes)
		out.WantUpBps, out.WantDownBps = limits.UpBps, limits.DownBps
	}

	wants, err := s.ShapingWants(ctx)
	if err != nil {
		return out, err
	}
	for _, want := range wants {
		if !slices.ContainsFunc(want.Subjects, func(s shaping.Subject) bool { return s.ID == email }) {
			continue
		}
		out.Shapeable = true
		enforced, err := shapingManager.Enforced(ctx, want)
		if err != nil {
			return out, err
		}
		out.EnforcedUpBps, out.EnforcedDownBps = enforced[email].UpBps, enforced[email].DownBps
	}
	return out, nil
}

// ShapingAvailable reports whether this host can carry the mechanism at all, so
// the UI can say shaping is unavailable instead of showing a field that does nothing.
func ShapingAvailable(ctx context.Context) (bool, string) {
	report := shapingManager.Preflight(ctx)
	if report.OK() {
		return true, ""
	}
	return false, report.Err().Error()
}
