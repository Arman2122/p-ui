package service

import (
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
DesiredInstances renders the local enabled inbounds of these kinds as the state
the core serving them must converge on.

It builds through the same path an interactive edit pushes, so a reconcile and
an edit cannot disagree about what should be running — a disagreement surfaces
as a needless restart of whatever the core supervises. An inbound whose clients
are all gone stays in the list carrying none: its core drops what it cannot
serve, which is how the last sidecar of an emptied inbound is stopped.
*/
func (s *InboundService) DesiredInstances(kinds []core.Kind) ([]core.Instance, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	protocols := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		protocols = append(protocols, string(kind))
	}

	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).
		Where("protocol IN ? AND enable = ? AND node_id IS NULL", protocols, true).
		Find(&inbounds).Error
	if err != nil {
		return nil, err
	}

	instances := make([]core.Instance, 0, len(inbounds))
	for _, inbound := range inbounds {
		// A build failure fails the whole set rather than omitting one inbound:
		// omitted reads as "stop it", and a transient error must not do that.
		built, buildErr := s.buildInboundForLocalRuntime(db, inbound)
		if buildErr != nil {
			return nil, buildErr
		}
		instances = append(instances, runtime.InstanceOf(built))
	}
	return instances, nil
}

/*
RenderForRuntime is what the local runtime applies on the hot path: Xray's own
render for the kinds Xray serves, and for every other kind the row built the
way DesiredInstances builds it, so an edit and a reconcile converge on one
state. Nil means nothing local serves the row — disabled, or a node's.
*/
func (s *InboundService) RenderForRuntime(inbound *model.Inbound) (*core.InboundConfig, error) {
	if inbound == nil || !inbound.Enable || inbound.NodeID != nil {
		return nil, nil
	}
	if servedByXrayCore(inbound.Protocol) {
		return (&XrayService{}).RenderInbound(inbound)
	}
	built, err := s.buildInboundForLocalRuntime(database.GetDB(), inbound)
	if err != nil {
		return nil, err
	}
	return built.GenXrayInboundConfig(), nil
}
