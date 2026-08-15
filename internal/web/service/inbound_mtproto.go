package service

import (
	"context"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/mtproto"
)

// DesiredMtprotoInstances derives the mtg sidecar configs this panel should be
// running: one instance per enabled local mtproto inbound, serving only the
// secrets of clients that are both enabled in the inbound settings and not
// depletion-disabled in client_traffics. That is the same effective client set
// buildInboundForLocalRuntime pushes on interactive edits and that the
// supervision job reconciles, so all three agree on one fingerprint — a
// disagreement would surface as a needless mtg restart. Inbounds whose every
// secret is filtered away are omitted, so a caller reconciling this set stops
// their sidecar.
func (s *InboundService) DesiredMtprotoInstances() ([]mtproto.Instance, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).
		Where("protocol = ? AND enable = ? AND node_id IS NULL", model.MTProto, true).
		Find(&inbounds).Error
	if err != nil {
		return nil, err
	}
	if len(inbounds) == 0 {
		return nil, nil
	}

	depleted, err := depletedEmails(db)
	if err != nil {
		return nil, err
	}

	instances := make([]mtproto.Instance, 0, len(inbounds))
	for _, ib := range inbounds {
		inst, ok := mtproto.InstanceFromInbound(ib)
		if !ok {
			continue
		}
		if len(depleted) > 0 {
			kept := make([]mtproto.SecretEntry, 0, len(inst.Secrets))
			for _, sec := range inst.Secrets {
				if !depleted[sec.Name] {
					kept = append(kept, sec)
				}
			}
			inst.Secrets = kept
		}
		if len(inst.Secrets) == 0 {
			continue
		}
		instances = append(instances, inst)
	}
	return instances, nil
}

// applyLocalMtproto pushes a single local mtproto inbound's current client set
// to its mtg sidecar right after a client edit commits, so an add, removal,
// re-key or enable-toggle takes effect immediately instead of waiting up to
// 10s for the reconcile job. With a reload-capable mtg the change is applied in
// place without dropping other clients; older binaries fall back to a restart
// inside the manager. It re-reads the inbound so it sees the committed settings,
// filters depleted clients exactly like the reconcile job, and is a no-op for
// node-owned or non-mtproto inbounds. Failures are logged and swallowed: the
// reconcile job is the backstop, and an xray restart cannot help the sidecar.
func (s *InboundService) applyLocalMtproto(inboundId int) {
	inbound, err := s.GetInbound(inboundId)
	if err != nil || inbound == nil || inbound.Protocol != model.MTProto || inbound.NodeID != nil {
		return
	}
	rt, err := s.runtimeFor(inbound)
	if err != nil {
		return
	}
	payload := inbound
	if inbound.Enable {
		if built, bErr := s.buildInboundForLocalRuntime(database.GetDB(), inbound); bErr == nil {
			payload = built
		}
	}
	if err := rt.UpdateInbound(context.Background(), inbound, payload); err != nil {
		logger.Debug("mtproto: immediate client apply failed for inbound", inboundId, ":", err)
	}
}

func (s *InboundService) resetMtprotoClientQuota(email string) {
	mgr := mtproto.GetManager()
	if !mgr.HasRunning() {
		return
	}
	id, ok := s.localMtprotoInboundIdForEmail(email)
	if !ok {
		return
	}
	s.applyLocalMtproto(id)
	if err := mgr.ResetQuota(email); err != nil {
		logger.Warning("mtproto: quota reset failed for", email, ":", err, "— the client may stay blocked by the sidecar until it restarts")
	}
}

/*
resetCoreQuotas clears the daemon-side usage counter for renewed clients.

Asked of the registry rather than of mtproto by name: any core that pushes a
byte budget down answers QuotaEnforcer, and a renewed client whose daemon keeps
counting stays blocked by that daemon until it restarts, however the panel feels
about the client's quota.
*/
func resetCoreQuotas(ctx context.Context, emails []string) {
	reg := registry()
	if reg == nil || len(emails) == 0 {
		return
	}
	for _, bound := range reg.Cores() {
		if bound.Quota == nil {
			continue
		}
		for _, email := range emails {
			if err := bound.Quota.ResetQuota(ctx, email); err != nil {
				logger.Warning("quota reset failed for", email, "on core", bound.Core.Describe().ID, ":", err,
					"— the client may stay blocked by its daemon until that daemon restarts")
			}
		}
	}
}

func (s *InboundService) resetAllMtprotoQuotas() {
	mgr := mtproto.GetManager()
	if !mgr.HasRunning() {
		return
	}
	desired, err := s.DesiredMtprotoInstances()
	if err != nil {
		return
	}
	if err := mgr.Reconcile(desired); err != nil {
		logger.Debug("mtproto: reconcile before quota reset failed:", err)
	}
	for _, inst := range desired {
		for _, sec := range inst.Secrets {
			if err := mgr.ResetQuota(sec.Name); err != nil {
				logger.Warning("mtproto: quota reset failed for", sec.Name, ":", err)
			}
		}
	}
}

func (s *InboundService) localMtprotoInboundIdForEmail(email string) (int, bool) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	if err := db.Model(model.Inbound{}).
		Where("protocol = ? AND node_id IS NULL", model.MTProto).
		Find(&inbounds).Error; err != nil {
		return 0, false
	}
	for _, ib := range inbounds {
		inst, ok := mtproto.InstanceFromInbound(ib)
		if !ok {
			continue
		}
		for _, sec := range inst.Secrets {
			if sec.Name == email {
				return ib.Id, true
			}
		}
	}
	return 0, false
}
