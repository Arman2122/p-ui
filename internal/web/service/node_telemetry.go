package service

import (
	"sync"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

var nodeMetricAvailability sync.Map

type NodeInboundTelemetry struct {
	ID       int    `json:"id" example:"1"`
	Tag      string `json:"tag" example:"vless-443"`
	Protocol string `json:"protocol" example:"vless"`
	Up       int64  `json:"up" example:"1048576"`
	Down     int64  `json:"down" example:"8388608"`
}

func enrichNodeTelemetry(nodes []*model.Node, views []*NodeView) error {
	var inbounds []model.Inbound
	if err := database.GetDB().Select("id, tag, protocol, node_id, origin_node_guid, up, down").
		Where("node_id IS NOT NULL").Find(&inbounds).Error; err != nil {
		return err
	}
	self, _ := (&SettingService{}).GetPanelGuid()
	ambiguous := ambiguousNodeGuids(nodes, self)
	byID := map[int]string{}
	byGUID := map[string]*NodeView{}
	for i, node := range nodes {
		guid := effectiveNodeGuid(node, ambiguous)
		byGUID[guid] = views[i]
		if !node.Transitive {
			byID[node.Id] = guid
		}
	}
	for _, inbound := range inbounds {
		guid := byID[*inbound.NodeID]
		if inbound.OriginNodeGuid != "" && inbound.OriginNodeGuid != self {
			guid = inbound.OriginNodeGuid
		}
		view := byGUID[guid]
		if view == nil {
			continue
		}
		view.ServedInbounds = append(view.ServedInbounds, NodeInboundTelemetry{
			ID: inbound.Id, Tag: inbound.Tag, Protocol: string(inbound.Protocol), Up: inbound.Up, Down: inbound.Down,
		})
	}
	return nil
}
