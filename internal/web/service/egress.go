package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/drivers/wgclient"
	"github.com/Arman2122/p-ui/internal/egress/drivers/xraytun"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/wireguard"
)

// The refusals an operator has to be able to tell apart, and a test has to be
// able to match without comparing a sentence.
var (
	ErrEgressUnknownType      = errors.New("egress: no driver serves this type")
	ErrEgressTargetUnresolved = errors.New("egress: the target names no outbound or balancer")
	ErrEgressNoIngressDevice  = errors.New("egress: this protocol has no ingress device to select on")
	ErrEgressMasterLocal      = errors.New("egress: egress is master-local while the id is a global key")
	ErrEgressInUse            = errors.New("egress: the egress still serves inbounds")
	ErrEgressDisabled         = errors.New("egress: a disabled egress installs no containment")
	ErrEgressNotRouted        = errors.New("egress: the attachment did not reach the host")
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
	return registry
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
	if inbound == nil {
		return "", false
	}
	handle, err := cores.IngressHandleFor(ctx, core.Instance{
		ID: inbound.Id, Kind: core.Kind(inbound.Protocol), Tag: inbound.Tag, Settings: inbound.Settings,
	})
	if err != nil || handle.Device == "" {
		return "", false
	}
	return handle.Device, true
}

// egressIngressSelectable is the static half: whether this KIND can ever be
// selected on, asked before any instance exists and without touching the host.
func egressIngressSelectable(inbound *model.Inbound) bool {
	if inbound == nil {
		return false
	}
	return cores.IngressSelectorFor(core.Kind(inbound.Protocol)) == core.IngressDevice
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
	return egressManager.Reconcile(ctx, desired)
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
	go func() {
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
			device, ok := egressIngressDevice(ctx, inbound)
			if !ok || inbound.NodeID != nil {
				continue
			}
			front := byInbound[inbound.Id]
			ingress[front] = append(ingress[front], device)
		}
	}
	out := make([]egress.Egress, 0, len(rows))
	for _, row := range rows {
		converted := egressRow(row)
		converted.Ingress = ingress[row.Id]
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

func (s *EgressService) validate(row *model.Egress) error {
	_, known := egressDriverRegistry.For(row.Type)
	if !known {
		return fmt.Errorf("%w: %q — this build serves %v", ErrEgressUnknownType, row.Type, egressDriverRegistry.Types())
	}
	// A disabled row routes nothing, so its target is not resolved against
	// anything: an egress whose outbound was deleted first can still be switched off.
	if !row.Enable {
		return nil
	}
	if !egressFronts(row.Type) {
		return nil
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
