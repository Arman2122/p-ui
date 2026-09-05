package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/drivers/coretun"
	"github.com/Arman2122/p-ui/internal/egress/drivers/wgclient"
	"github.com/Arman2122/p-ui/internal/egress/drivers/xraytun"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/util/common"
	"github.com/Arman2122/p-ui/internal/wireguard"
)

// The refusals an operator has to be able to tell apart, and a test has to be
// able to match without comparing a sentence.
var (
	ErrEgressUnknownType      = errors.New("egress: no driver serves this type")
	ErrEgressTargetUnresolved = errors.New("egress: the target names no outbound or balancer")
	ErrEgressUnrealisable     = errors.New("egress: its own driver cannot derive what the kernel needs")
	ErrEgressInUse            = errors.New("egress: the egress still serves inbounds")
)

// egressDriverRegistry is the driver set this build serves, one Register line
// per type like internal/cores/cores.go. Explicit, never init().
var egressDriverRegistry = newEgressDriverRegistry()

func newEgressDriverRegistry() *egress.Registry {
	registry := egress.NewRegistry()
	// The only failure is a duplicate type, which one literal list cannot make.
	_ = registry.Register(xraytun.New())
	// The shared engine, not a second one: an uplink and an inbound must never
	// end up as two writers to the same device namespace.
	_ = registry.Register(wgclient.New(wireguard.GetUplinkManager()))
	// AmneziaWG dials the same shape through the other module. A distinct type,
	// because the type decides which module makes the device and one dialled by
	// the plain WireGuard driver would carry no obfuscation while claiming to.
	_ = registry.Register(wgclient.NewTyped(awg.UplinkDriverType, awg.UplinkManager()).WithObfuscation(awg.ApplyUplinkObfuscation))
	// A core whose client daemon makes its own tun (OpenVPN, OpenConnect) earns
	// its driver by declaring a supervised device exit; the row type is the kind.
	for _, kind := range cores.ExitKinds() {
		if _, supervised := cores.ExitSupervisorFor(kind); !supervised || cores.ExitHandleKindFor(kind) != core.ExitDevice {
			continue
		}
		_ = registry.Register(coretun.New(string(kind), coreExitDevice(kind)))
	}
	return registry
}

// coreExitDevice asks the core for the name it gives an exit's device, so the
// plane routes into the device the daemon actually creates.
func coreExitDevice(kind core.Kind) func(id int) (string, error) {
	return func(id int) (string, error) {
		_, handle, err := cores.ExitHandleFor(context.Background(), core.Exit{ID: id, Kind: kind, Enable: true})
		return handle.Device, err
	}
}

// egressManager owns every kernel object the band derives. One instance, because
// a second would be a second writer to one host-global resource; a var so a test
// can converge against a fake plane without root or a Linux kernel.
var egressManager = egress.New(egress.HostPlane(), egressDriverRegistry)

/*
EgressService is CRUD over the egress rows plus the one path that turns them
into kernel state.

Every mutation converges through Reconcile rather than through a private
"just this row" shortcut: a second writer to one host-global band is how the
reconciler and the mutation path start disagreeing about who owns what.
*/
type EgressService struct{}

/*
egressIngressDevice names the interface an inbound's decrypted traffic crosses.

The answer comes from the core registry, so a new L3 core becomes selectable by
registering rather than by editing a comparison here. Selection is by ingress
device because cryptokey routing has already proven the peer's identity by the
time a packet appears there.
*/
func egressIngressDevice(ctx context.Context, inbound *model.Inbound) (string, bool) {
	handle, ok := egressIngressHandle(ctx, inbound)
	if !ok || handle.Device == "" {
		return "", false
	}
	return handle.Device, true
}

// egressIngressHandle is the whole surface, for the caller that selects on it:
// a device for a core that owns one, a pool for a core whose sessions do not.
func egressIngressHandle(ctx context.Context, inbound *model.Inbound) (core.IngressHandle, bool) {
	if inbound == nil {
		return core.IngressHandle{}, false
	}
	handle, err := cores.IngressHandleFor(ctx, core.Instance{
		ID: inbound.Id, Kind: core.Kind(inbound.Protocol), Tag: inbound.Tag, Settings: inbound.Settings,
	})
	if err != nil || !handle.Fronted() {
		return core.IngressHandle{}, false
	}
	return handle, true
}

// egressIngressSelectable is the static half: whether this KIND can ever be
// selected on, asked before any instance exists and without touching the host.
func egressIngressSelectable(inbound *model.Inbound) bool {
	if inbound == nil {
		return false
	}
	switch cores.IngressSelectorFor(core.Kind(inbound.Protocol)) {
	case core.IngressDevice, core.IngressSubnet:
		return true
	}
	return false
}

// egressGatewayBase is where every front's own /32 is carved from. An addressless
// front fails reverse-path filtering, and only on the return path.
func egressGatewayBase() netip.Prefix { return xraytun.New().GatewayBase }

// GetAll returns every egress row in id order, so anything generated from it is
// byte-stable across builds.
func (s *EgressService) GetAll() ([]*model.Egress, error) {
	var rows []*model.Egress
	if err := database.GetDB().Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *EgressService) Get(id int) (*model.Egress, error) {
	row := &model.Egress{}
	if err := database.GetDB().First(row, id).Error; err != nil {
		return nil, err
	}
	return row, nil
}

// Add creates one egress. The id comes from the sequence and is the only thing
// this row stores that is not the operator's: everything else is derived.
func (s *EgressService) Add(row *model.Egress) (*model.Egress, error) {
	row.Id = 0
	/*
		A pointer at zero is not a selection. Inbound ids are autoincrement, so no
		row can ever be 0 — but the column carries a foreign key onto inbounds(id),
		and a JSON body with `"ingressInboundId": 0` binds to a NON-nil *int that
		reaches it. Every such create was refused by Postgres with a 23503.

		Normalised here rather than only in the caller that did it, because the
		next caller sends JSON too.
	*/
	if row.IngressInboundId != nil && *row.IngressInboundId == 0 {
		row.IngressInboundId = nil
	}
	if err := s.validate(row); err != nil {
		return nil, err
	}
	if err := database.GetDB().Create(row).Error; err != nil {
		return nil, err
	}
	if !egress.ValidID(row.Id) {
		// The band is exhausted rather than misconfigured: keeping the row would
		// leave an egress whose table number belongs to somebody else.
		_ = database.GetDB().Delete(&model.Egress{}, row.Id).Error
		return nil, fmt.Errorf("%w: the id sequence reached %d and every egress resource is derived from an id in %d..%d",
			egress.ErrIDOutOfRange, row.Id, egress.MinID, egress.MaxID)
	}
	convergeEgress(row.Id)
	return row, nil
}

// Update replaces the editable half of a row. Id, and so every kernel object it
// derives, is fixed for the row's whole life.
func (s *EgressService) Update(row *model.Egress) (*model.Egress, error) {
	stored, err := s.Get(row.Id)
	if err != nil {
		return nil, err
	}
	if err := s.validate(row); err != nil {
		return nil, err
	}
	// Locked, because "nothing references it" and the disable it justifies are two
	// statements apart: an attach landing between them leaves that inbound direct.
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := lockEgress(tx, row.Id, stored); err != nil {
			return err
		}
		if stored.Enable && !row.Enable {
			if err := checkNotReferenced(tx, row.Id); err != nil {
				return err
			}
		}
		stored.Type = row.Type
		stored.Enable = row.Enable
		stored.Remark = row.Remark
		stored.Target = row.Target
		stored.Settings = row.Settings
		return tx.Save(stored).Error
	})
	if err != nil {
		return nil, err
	}
	convergeEgress(stored.Id)
	return stored, nil
}

// lockEgress takes the row's write lock and reads it back, so every decision made
// after it is made against the committed row rather than a stale copy.
func lockEgress(tx *gorm.DB, id int, into *model.Egress) error {
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(into, id).Error
}

// convergeEgress brings the host in line after a CRUD edit. The edit itself has
// landed by now, so a failure here is the drift job's business, not the caller's.
func convergeEgress(id int) {
	if err := (&EgressService{}).Reconcile(context.Background()); err != nil {
		logger.Warning("egress", id, "was written but the host has not converged yet:", err)
	}
}

// Del removes a row and the kernel state its id owns. Refused while an inbound
// still selects it: the alternative is silently moving that inbound to direct.
func (s *EgressService) Del(id int) error {
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		// Locking Find rather than First: deleting a row that is already gone stays
		// the no-op it was, while a concurrent attach still has to wait for us.
		var locked []model.Egress
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&locked, id).Error; err != nil {
			return err
		}
		if err := checkNotReferenced(tx, id); err != nil {
			return err
		}
		return tx.Delete(&model.Egress{}, id).Error
	})
	if err != nil {
		return err
	}
	// Synchronous, and before the config drops the front: taking the rule down is
	// what stops traffic, and it must not wait for the next tick.
	if err := egressManager.Remove(context.Background(), id); err != nil {
		logger.Warning("egress", id, "row is gone but its kernel state did not come down:", err)
	}
	return nil
}

// Reconcile drives the host toward every row. It is the ONE convergence path:
// mutations call it synchronously and the drift job calls it on a tick.
func (s *EgressService) Reconcile(ctx context.Context) error {
	rows, err := s.GetAll()
	if err != nil {
		return err
	}
	desired, err := s.desired(ctx, rows)
	if err != nil {
		return err
	}
	return errors.Join(egressManager.Reconcile(ctx, desired), reconcileCoreExits(ctx, rows))
}

/*
egressRestartKickDelays is when the passes of a post-restart reconcile run.

The front device belongs to the core and appears AFTER the process does —
measured on 6.8.0-111 with the shipped binary: 20 ms for a bare config, 535 ms
for a geoip/geosite one, in every run later than the panel's own Start() returns.
So a single immediate pass would usually find no device and repair nothing. The
10s drift job stays the backstop for a core slower than the last delay.
*/
var egressRestartKickDelays = []time.Duration{250 * time.Millisecond, 750 * time.Millisecond, 2 * time.Second}

/*
kickEgressAfterCoreRestart restores the one object a core restart destroys and
does not rebuild: the `default dev <front>` route into each egress table.

Measured: after a restart that recreated the front, the table still held only its
blackhole, so every attached inbound is contained — not leaking, but dark — until
something reconciles. Waiting for the 10s tick is up to ten seconds of that.
*/
func kickEgressAfterCoreRestart() {
	egressKicks.Add(1)
	go func() {
		defer egressKicks.Done()
		service := &EgressService{}
		for _, delay := range egressRestartKickDelays {
			time.Sleep(delay)
			if err := service.Reconcile(context.Background()); err != nil {
				logger.Debug("egress: the host has not converged since the core restarted:", err)
			}
		}
	}()
}

// Preflight answers whether this host can carry an egress at all, naming the
// exact resource and remedy for anything it refuses. The rows go with it so a
// row whose front never came up is named rather than left reading as healthy.
func (s *EgressService) Preflight(ctx context.Context) egress.Report {
	rows, err := s.GetAll()
	if err != nil {
		return egress.Report{Refusals: []error{err}}
	}
	converted := make([]egress.Egress, 0, len(rows))
	for _, row := range rows {
		converted = append(converted, egressRow(row))
	}
	report := egressManager.Preflight(ctx, egressGatewayBase(), converted...)
	report.Notes = append(report.Notes, deadTargets(rows)...)
	return report
}

// ForwardingNotes is the host-forwarding half of Preflight on its own, for the
// wgkernel form: an L3 inbound needs it whether or not an egress exists.
func (s *EgressService) ForwardingNotes(ctx context.Context) []string {
	notes := egressManager.ForwardingNotes(ctx)
	if notes == nil {
		return []string{}
	}
	return notes
}

// deadTargets names every enabled row whose target stopped resolving. validate()
// checks it once at save, so without this nothing ever says it again.
func deadTargets(rows []*model.Egress) []string {
	var notes []string
	for _, row := range rows {
		if !row.Enable {
			continue
		}
		// Only a front has a target to lose, exactly as validate() decides it. An
		// uplink IS the destination, so asking it resolves "" and condemns the row.
		if !egressFronts(row.Type) {
			continue
		}
		// A panel-provisioned front carries no target by design -- ensureFronts
		// creates it without one -- so asking it to resolve condemns every one.
		if row.Owner == model.EgressOwnerPanel {
			continue
		}
		if resolves, err := egressTargetResolves(row.Target); err == nil && !resolves {
			notes = append(notes, fmt.Sprintf(
				"egress %d targets %q, which is no longer an outbound or a balancer tag, so everything attached to it is contained rather than routed",
				row.Id, row.Target))
		}
	}
	return notes
}

/*
desired turns the rows into what the manager converges.

Disabled rows are included so a row just switched off is torn down instead of
stranded, and an attached inbound contributes its rule whether or not it is
enabled: a rule whose device is absent is inert and reattaches by itself, so
including it removes the window where enabling an inbound egresses direct.
*/
func (s *EgressService) desired(ctx context.Context, rows []*model.Egress) ([]egress.Egress, error) {
	// A front names the inbound it exists for; an operator uplink names none. The
	// selection therefore reads the FRONT rows, not a column on the inbound.
	byInbound := map[int]int{}
	for _, row := range rows {
		if row.IngressInboundId != nil {
			byInbound[*row.IngressInboundId] = row.Id
		}
	}
	ingress := map[int][]string{}
	sources := map[int][]netip.Prefix{}
	if len(byInbound) > 0 {
		ids := make([]int, 0, len(byInbound))
		for id := range byInbound {
			ids = append(ids, id)
		}
		var inbounds []*model.Inbound
		err := database.GetDB().Model(&model.Inbound{}).
			Select("id", "protocol", "tag", "settings", "node_id").
			Where("id IN ?", ids).Find(&inbounds).Error
		if err != nil {
			return nil, err
		}
		for _, inbound := range inbounds {
			handle, ok := egressIngressHandle(ctx, inbound)
			if !ok || inbound.NodeID != nil {
				continue
			}
			front := byInbound[inbound.Id]
			if handle.Device != "" {
				ingress[front] = append(ingress[front], handle.Device)
			}
			sources[front] = append(sources[front], handle.Sources...)
		}
	}
	out := make([]egress.Egress, 0, len(rows))
	for _, row := range rows {
		// A core-owned exit has no plane state; fed to the plane it would report
		// an unknown driver on every pass.
		if _, known := egressDriverRegistry.For(row.Type); !known {
			continue
		}
		converted := egressRow(row)
		converted.Ingress = ingress[row.Id]
		converted.Sources = sources[row.Id]
		out = append(out, converted)
	}
	return out, nil
}

// egressRow is the model row as the engine takes it. The engine is a leaf: it
// must not be able to reach the data layer, so the shapes stay separate.
func egressRow(row *model.Egress) egress.Egress {
	converted := egress.Egress{ID: row.Id, Type: row.Type, Enable: row.Enable, Target: row.Target}
	if row.Settings != "" {
		converted.Settings = json.RawMessage(row.Settings)
	}
	return converted
}

/*
egressFronts reports whether this type needs an outbound tag to send traffic to.

Only a FRONT does: it terminates into a core and injects a rule pointing at one,
so an unresolvable tag would leave it dark. An uplink IS the destination, and
asking it which outbound to forward to is meaningless. The one answer, because
save and preflight asking it separately is how an uplink came to be reported as
containing the traffic it was in fact carrying.
*/
func egressFronts(egressType string) bool {
	driver, known := egressDriverRegistry.For(egressType)
	if !known {
		return false
	}
	_, fronts := driver.(egress.Injector)
	return fronts
}

/*
egressDriverFills asks the row's own driver to derive what the kernel needs.

Fill is where a driver reads its settings, so this is the only thing that can
tell a saveable row from one that will fail to be realised — and the manager
zeroes a failed Fill and carries on, so nothing downstream ever says it again.
*/
func egressDriverFills(row *model.Egress) error {
	driver, known := egressDriverRegistry.For(row.Type)
	if !known {
		return nil
	}
	if _, err := driver.Fill(egressRow(row)); err != nil {
		return fmt.Errorf("%w: %w", ErrEgressUnrealisable, err)
	}
	return nil
}

func (s *EgressService) validate(row *model.Egress) error {
	_, driven := egressDriverRegistry.For(row.Type)
	kind := exitKindFor(row.Type)
	supervisor, supervised := cores.ExitSupervisorFor(kind)
	// A core-owned exit behind a SOCKS port needs no plane state. One behind a
	// device needs a driver as well, or nothing would ever be routed into it.
	if !driven && (!supervised || cores.ExitHandleKindFor(kind) != core.ExitSocksPort) {
		return fmt.Errorf("%w: %q — this build serves %v", ErrEgressUnknownType, row.Type, servedEgressTypes())
	}
	// A disabled row routes nothing, so nothing of it is resolved against the
	// host: an egress whose outbound was deleted first can still be switched off.
	if !row.Enable {
		return nil
	}
	// The core that dials it refuses settings it cannot dial with.
	if supervised {
		if err := supervisor.ValidateExit(coreExit(row, kind)); err != nil {
			return err
		}
	}
	if !driven {
		return nil
	}
	/*
		The half validate used to skip outright. An uplink's settings are read by
		nothing but its own driver, so this is the only check that can refuse one
		that cannot be realised — and until it existed, a typo in an Address saved
		cleanly, Fill failed later at plan time, and the fwmark rule was silently
		never installed while the rule still emitted a marked outbound.

		Not applied to a front: its Fill derives a device Xray creates, so it
		answers for the running process rather than for these settings.
	*/
	if !egressFronts(row.Type) {
		return egressDriverFills(row)
	}
	resolves, err := egressTargetResolves(row.Target)
	if err != nil {
		return err
	}
	if !resolves {
		return fmt.Errorf("%w: %q is neither an outbound tag nor a balancer tag", ErrEgressTargetUnresolved, row.Target)
	}
	return nil
}

// servedEgressTypes names every type a row may carry in this build: the plane's
// drivers and the kinds a core dials itself, once each.
func servedEgressTypes() []string {
	out := egressDriverRegistry.Types()
	for _, kind := range cores.ExitKinds() {
		if _, ok := cores.ExitSupervisorFor(kind); ok {
			out = append(out, string(kind))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func coreExit(row *model.Egress, kind core.Kind) core.Exit {
	return core.Exit{ID: row.Id, Kind: kind, Enable: row.Enable, Settings: row.Settings}
}

// reconcileCoreExits hands each supervising core the enabled rows of its kinds.
// The plane never sees them: a SOCKS exit has no device, table or rule.
func reconcileCoreExits(ctx context.Context, rows []*model.Egress) error {
	reg := registry()
	if reg == nil {
		return nil
	}
	var failures []error
	for _, bound := range reg.Cores() {
		if bound.ExitSupervise == nil {
			continue
		}
		var desired []core.Exit
		for _, row := range rows {
			if row == nil || !row.Enable || row.Owner == model.EgressOwnerPanel {
				continue
			}
			kind := exitKindFor(row.Type)
			if kind == "" || !slices.Contains(bound.Core.Kinds(), kind) {
				continue
			}
			desired = append(desired, coreExit(row, kind))
		}
		if err := bound.ExitSupervise.ReconcileExits(ctx, desired); err != nil {
			failures = append(failures, fmt.Errorf("%s exits: %w", bound.Core.Describe().ID, err))
		}
	}
	return errors.Join(failures...)
}

// checkNotReferenced refuses to delete or disable a row a routing rule sends
// traffic to. Either would move that traffic to direct — the server's own
// identity — without anybody asking.
func checkNotReferenced(tx *gorm.DB, id int) error {
	var ruleIDs []int
	err := tx.Model(&model.RoutingRule{}).
		Where("dest_kind = ? AND dest_exit_id = ?", model.RoutingDestExit, id).
		Order("id").Pluck("id", &ruleIDs).Error
	if err != nil {
		return err
	}
	if len(ruleIDs) > 0 {
		return fmt.Errorf("%w: egress %d is the exit of routing rule(s) %v — repoint or delete them first", ErrEgressInUse, id, ruleIDs)
	}
	return nil
}

// egressTargetResolves reports whether tag names an outbound or a balancer the
// injection will find: the template plus subscriptions, which is that surface.
func egressTargetResolves(tag string) (bool, error) {
	cfg, err := (&XrayService{}).GetXrayBaseConfig()
	if err != nil {
		return false, err
	}
	subscriptions := &OutboundSubscriptionService{}
	if prepend, appendList, err := subscriptions.activeOutboundsSplit(); err == nil {
		mergeSubscriptionOutbounds(cfg, prepend, appendList)
	}
	routing := map[string]any{}
	if len(cfg.RouterConfig) > 0 {
		if err := json.Unmarshal(cfg.RouterConfig, &routing); err != nil {
			return false, err
		}
	}
	return routingTargetExists(routing, cfg.OutboundConfigs, tag), nil
}

// egressKicks counts restart kicks in flight: a kick outlives its caller by the
// whole ladder and reads a manager the test harness swaps. CI run 33641933638.
var egressKicks sync.WaitGroup

/*
checkIngressPoolConflict refuses an inbound whose address pool overlaps one
another inbound already hands out.

Two pools that overlap were only untidy while nothing selected on them. They are
a hijack now: each attached inbound installs `ip rule from <pool>` at its own
priority, and for the addresses both cover the lower-priority rule never sees a
packet -- one inbound's clients leave through the other's exit, silently and
only for part of the range.

Asked through the contract rather than by protocol, so a second core whose
sessions have no device is covered by registering. WireGuard is not: it reports
a device, and its own address check already refuses the same overlap.

A DISABLED inbound still holds its pool here, deliberately: re-enabling it is
what would create the hijack, and it is not the moment to discover that.
*/
func (s *InboundService) checkIngressPoolConflict(ctx context.Context, inbound *model.Inbound, ignoreID int) error {
	handle, ok := egressIngressHandle(ctx, inbound)
	if !ok || len(handle.Sources) == 0 {
		return nil
	}
	q := database.GetDB().Model(model.Inbound{}).Select("id", "protocol", "tag", "remark", "settings", "node_id")
	if ignoreID > 0 {
		q = q.Where("id != ?", ignoreID)
	}
	var candidates []*model.Inbound
	if err := q.Find(&candidates).Error; err != nil {
		return err
	}
	for _, other := range candidates {
		if !sameNode(other.NodeID, inbound.NodeID) {
			continue
		}
		taken, resolved := egressIngressHandle(ctx, other)
		if !resolved {
			continue
		}
		for _, mine := range handle.Sources {
			for _, theirs := range taken.Sources {
				if mine.Overlaps(theirs) {
					return common.NewError("address pool", mine.String(), "overlaps", theirs.String(),
						"already served by inbound", inboundName(other))
				}
			}
		}
	}
	return nil
}
