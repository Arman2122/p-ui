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

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
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
egressIngressDevice is the one place an inbound becomes an `iif` selector, and
the single protocol dispatch site this feature is budgeted for.

Selection is by ingress device because cryptokey routing has already proven the
peer's identity by the time a packet appears there — a stronger claim than a
source prefix, and one that survives an AllowedIPs edit.
*/
func egressIngressDevice(inbound *model.Inbound) (string, bool) {
	if inbound == nil || inbound.Protocol != model.WGKernel {
		return "", false
	}
	return wireguard.InterfaceName(inbound.Id), true
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

/*
Attach points one inbound at an egress; an egressID of 0 detaches it.

Synchronous by design. A reconcile that caught up on the next tick would leave a
just-attached inbound egressing with the server's own identity for up to a tick,
which is the one outcome the operator asked for the egress to prevent.
*/
func (s *EgressService) Attach(inboundID, egressID int) error {
	inbound := &model.Inbound{}
	if err := database.GetDB().First(inbound, inboundID).Error; err != nil {
		return err
	}
	var selected *int
	if egressID > 0 {
		row, err := s.Get(egressID)
		if err != nil {
			return err
		}
		if err := s.checkAttachable(inbound, row); err != nil {
			return err
		}
		selected = &row.Id
	}
	// Locked, because checkAttachable and the reference it justifies are two
	// statements apart: a disable landing between them leaves this inbound direct.
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		if selected != nil {
			locked := &model.Egress{}
			if err := lockEgress(tx, *selected, locked); err != nil {
				return err
			}
			if !locked.Enable {
				return fmt.Errorf("%w: egress %d is disabled, so attaching to it would egress direct",
					ErrEgressDisabled, locked.Id)
			}
		}
		return tx.Model(&model.Inbound{}).Where("id = ?", inboundID).Update("egress_id", selected).Error
	})
	if err != nil {
		return err
	}

	ctx := context.Background()
	converged := s.Reconcile(ctx)
	if err := s.verifyAttachment(ctx, inbound, egressID); err != nil {
		// Attached but unrouted is the one outcome worse than a failed attach: the
		// inbound would egress with the server's own identity and nobody would know.
		_ = database.GetDB().Model(&model.Inbound{}).Where("id = ?", inboundID).
			Update("egress_id", inbound.EgressID).Error
		return errors.Join(err, converged)
	}
	if converged != nil {
		logger.Warning("egress: inbound", inboundID, "is attached, but another row on this host has not converged:", converged)
	}
	return nil
}

/*
verifyAttachment asks the kernel whether THIS inbound is routed as asked.

Reconcile converges the whole host and joins every row's failure, so a permanent
condition on an unrelated egress — a front whose family the host disabled, an id
left behind by a row somebody deleted — would otherwise revert every attach on
the box forever. Only this inbound's own rule may decide this inbound's fate.
*/
func (s *EgressService) verifyAttachment(ctx context.Context, inbound *model.Inbound, egressID int) error {
	device, ok := egressIngressDevice(inbound)
	if !ok {
		return nil
	}
	routed, err := egressManager.Selects(ctx, device, egressID)
	if err != nil {
		return err
	}
	if !routed {
		if egressID == 0 {
			return fmt.Errorf("%w: %s is still selected into the egress band", ErrEgressNotRouted, device)
		}
		return fmt.Errorf("%w: no rule selects %s into egress %d", ErrEgressNotRouted, device, egressID)
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
	desired, err := s.desired(rows)
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
// exact resource and remedy for anything it refuses.
func (s *EgressService) Preflight(ctx context.Context) egress.Report {
	return egressManager.Preflight(ctx, egressGatewayBase())
}

/*
desired turns the rows into what the manager converges.

Disabled rows are included so a row just switched off is torn down instead of
stranded, and an attached inbound contributes its rule whether or not it is
enabled: a rule whose device is absent is inert and reattaches by itself, so
including it removes the window where enabling an inbound egresses direct.
*/
func (s *EgressService) desired(rows []*model.Egress) ([]egress.Egress, error) {
	var inbounds []*model.Inbound
	err := database.GetDB().Model(&model.Inbound{}).
		Select("id", "protocol", "node_id", "egress_id").
		Where("egress_id IS NOT NULL").
		Find(&inbounds).Error
	if err != nil {
		return nil, err
	}
	ingress := map[int][]string{}
	for _, inbound := range inbounds {
		device, ok := egressIngressDevice(inbound)
		if !ok || inbound.EgressID == nil || inbound.NodeID != nil {
			continue
		}
		ingress[*inbound.EgressID] = append(ingress[*inbound.EgressID], device)
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

func (s *EgressService) validate(row *model.Egress) error {
	if _, known := egressDriverRegistry.For(row.Type); !known {
		return fmt.Errorf("%w: %q — this build serves %v", ErrEgressUnknownType, row.Type, egressDriverRegistry.Types())
	}
	// A disabled row routes nothing, so its target is not resolved against
	// anything: an egress whose outbound was deleted first can still be switched off.
	if !row.Enable {
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

// checkNotReferenced refuses to delete or disable a row inbounds still select.
// Both would move those inbounds to direct without anybody asking for it.
func checkNotReferenced(tx *gorm.DB, id int) error {
	var attached int64
	err := tx.Model(&model.Inbound{}).Where("egress_id = ?", id).Count(&attached).Error
	if err != nil {
		return err
	}
	if attached > 0 {
		return fmt.Errorf("%w: egress %d still serves %d inbound(s) — detach them first", ErrEgressInUse, id, attached)
	}
	return nil
}

func (s *EgressService) checkAttachable(inbound *model.Inbound, row *model.Egress) error {
	if _, ok := egressIngressDevice(inbound); !ok {
		return fmt.Errorf("%w: inbound %d is %q", ErrEgressNoIngressDevice, inbound.Id, inbound.Protocol)
	}
	if inbound.NodeID != nil {
		return fmt.Errorf("%w: inbound %d lives on node %d, and every resource egress %d derives is per-host",
			ErrEgressMasterLocal, inbound.Id, *inbound.NodeID, row.Id)
	}
	if !row.Enable {
		return fmt.Errorf("%w: egress %d is disabled, so attaching to it would egress direct", ErrEgressDisabled, row.Id)
	}
	report := egressManager.Preflight(context.Background(), egressGatewayBase())
	for _, note := range report.Notes {
		logger.Warning("egress preflight:", note)
	}
	return report.Err()
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
