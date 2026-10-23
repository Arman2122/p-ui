package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type FleetPlacementService struct{ db *gorm.DB }

func NewFleetPlacementService(db *gorm.DB) (*FleetPlacementService, error) {
	if db == nil || db.Name() != "postgres" {
		return nil, controlplane.ErrInvalid
	}
	return &FleetPlacementService{db: db}, nil
}

type FleetNodePlacementRequest struct {
	NodeID           string            `json:"nodeId" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	InstallationID   string            `json:"installationId" example:"948a18c5-a5d0-4c28-8052-d46224088bd6"`
	ExpectedRevision uint64            `json:"expectedRevision" example:"0"`
	Enabled          bool              `json:"enabled" example:"true"`
	Labels           map[string]string `json:"labels" example:"{\"region\":\"eu\"}"`
	InboundIDs       []int             `json:"inboundIds" example:"[1,2]"`
}

type FleetNodePlacementView struct {
	NodeID         string            `json:"nodeId" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	InstallationID string            `json:"installationId" example:"948a18c5-a5d0-4c28-8052-d46224088bd6"`
	Revision       uint64            `json:"revision" example:"1"`
	Enabled        bool              `json:"enabled" example:"true"`
	Labels         map[string]string `json:"labels" example:"{\"region\":\"eu\"}"`
	InboundIDs     []int             `json:"inboundIds" example:"[1,2]"`
	SHA256         string            `json:"sha256" example:"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`
	Pending        bool              `json:"pending" example:"true"`
}

type FleetPlacementRuleRequest struct {
	ID               string            `json:"id" example:"04ba844f-c64b-4453-82f5-d19a3b81e2f3"`
	ExpectedRevision uint64            `json:"expectedRevision" example:"0"`
	Scope            string            `json:"scope" example:"plan"`
	PlanID           *int              `json:"planId" example:"1"`
	GroupID          *int              `json:"groupId" example:"null"`
	Enabled          bool              `json:"enabled" example:"true"`
	NodeIDs          []string          `json:"nodeIds" example:"[\"33253fd2-c71a-43c6-84e5-6277e435e4d2\"]"`
	Labels           map[string]string `json:"labels" example:"{\"region\":\"eu\"}"`
	InboundIDs       []int             `json:"inboundIds" example:"[1,2]"`
}

type FleetPlacementRuleView struct {
	ID         string            `json:"id" example:"04ba844f-c64b-4453-82f5-d19a3b81e2f3"`
	Revision   uint64            `json:"revision" example:"1"`
	Scope      string            `json:"scope" example:"plan"`
	PlanID     *int              `json:"planId" example:"1"`
	GroupID    *int              `json:"groupId" example:"null"`
	Enabled    bool              `json:"enabled" example:"true"`
	NodeIDs    []string          `json:"nodeIds" example:"[\"33253fd2-c71a-43c6-84e5-6277e435e4d2\"]"`
	Labels     map[string]string `json:"labels" example:"{\"region\":\"eu\"}"`
	InboundIDs []int             `json:"inboundIds" example:"[1,2]"`
	SHA256     string            `json:"sha256" example:"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`
	Pending    bool              `json:"pending" example:"true"`
}

type FleetPlacementDefaultRequest struct {
	ExpectedRevision uint64  `json:"expectedRevision" example:"0"`
	NodeID           *string `json:"nodeId" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	InstallationID   *string `json:"installationId" example:"948a18c5-a5d0-4c28-8052-d46224088bd6"`
}

type FleetPlacementDefaultView struct {
	Revision       uint64  `json:"revision" example:"1"`
	NodeID         *string `json:"nodeId" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	InstallationID *string `json:"installationId" example:"948a18c5-a5d0-4c28-8052-d46224088bd6"`
	Pending        bool    `json:"pending" example:"true"`
}

func validatePlacementLabels(labels map[string]string) error {
	if len(labels) > 64 {
		return controlplane.ErrInvalid
	}
	for key, value := range labels {
		if len(key) == 0 || len(key) > 64 || len(value) == 0 || len(value) > 128 || strings.ContainsFunc(key+value, func(r rune) bool { return r < 32 || r == 127 }) {
			return controlplane.ErrInvalid
		}
	}
	return nil
}

func (s *FleetPlacementService) ConfigureNode(ctx context.Context, actor *model.User, request FleetNodePlacementRequest) (FleetNodePlacementView, error) {
	var view FleetNodePlacementView
	if !fleetID(request.NodeID) || !fleetID(request.InstallationID) || request.ExpectedRevision >= math.MaxInt64 {
		return view, controlplane.ErrInvalid
	}
	ids, err := canonicalPlacementInboundIDs(request.InboundIDs)
	if err != nil {
		return view, err
	}
	if err := validatePlacementLabels(request.Labels); err != nil {
		return view, err
	}
	if request.Labels == nil {
		request.Labels = map[string]string{}
	}
	view = FleetNodePlacementView{NodeID: request.NodeID, InstallationID: request.InstallationID, Enabled: request.Enabled, Labels: request.Labels, InboundIDs: ids}
	digest, err := fleetDigest(view)
	if err != nil {
		return view, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		if err := lockFleetNode(tx, request.NodeID, request.InstallationID); err != nil {
			return err
		}
		if err := requirePlacementInboundTemplates(tx, ids); err != nil {
			return err
		}
		var current model.FleetNodePlacementPolicy
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("node_id = ?", request.NodeID).Find(&current).Error; err != nil {
			return err
		}
		if current.NodeID != "" && current.InstallationID != request.InstallationID {
			current.Revision = 0
		}
		if current.Revision > 0 && bytes.Equal(current.Digest, digest) && (uint64(current.Revision) == request.ExpectedRevision || uint64(current.Revision) == request.ExpectedRevision+1) {
			view.Revision, view.SHA256, view.Pending = uint64(current.Revision), hex.EncodeToString(digest), true
			return nil
		}
		if uint64(current.Revision) != request.ExpectedRevision {
			return controlplane.ErrRevision
		}
		labels, _ := json.Marshal(request.Labels)
		inbounds, _ := json.Marshal(ids)
		current = model.FleetNodePlacementPolicy{
			NodeID: request.NodeID, InstallationID: request.InstallationID, Revision: current.Revision + 1,
			Enabled: request.Enabled, Labels: string(labels), InboundIDs: string(inbounds), Digest: digest, ActorID: actor.Id, UpdatedUnixMilli: time.Now().UnixMilli(),
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&current).Error; err != nil {
			return err
		}
		view.Revision, view.SHA256, view.Pending = uint64(current.Revision), hex.EncodeToString(digest), true
		return controlplane.QueuePlacementReevaluationTx(tx, "node", request.NodeID+"/"+request.InstallationID)
	})
	return view, err
}

func requirePlacementInboundTemplates(tx *gorm.DB, ids []int) error {
	var count int64
	if len(ids) == 0 {
		return nil
	}
	if err := tx.Model(&model.Inbound{}).Where("id IN ? AND node_id IS NULL", ids).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return ErrFleetSource
	}
	return nil
}

func (s *FleetPlacementService) PutRule(ctx context.Context, actor *model.User, request FleetPlacementRuleRequest) (FleetPlacementRuleView, error) {
	var view FleetPlacementRuleView
	validScope := request.Scope == "plan" && request.PlanID != nil && *request.PlanID > 0 && request.GroupID == nil || request.Scope == "group" && request.GroupID != nil && *request.GroupID > 0 && request.PlanID == nil
	if !fleetID(request.ID) || request.ExpectedRevision >= math.MaxInt64 ||
		!validScope ||
		len(request.NodeIDs) > 1000 || len(request.NodeIDs) == 0 && len(request.Labels) == 0 || len(request.InboundIDs) == 0 {
		return view, controlplane.ErrInvalid
	}
	ids, err := canonicalPlacementInboundIDs(request.InboundIDs)
	if err != nil {
		return view, err
	}
	if err := validatePlacementLabels(request.Labels); err != nil {
		return view, err
	}
	nodes := append([]string{}, request.NodeIDs...)
	slices.Sort(nodes)
	for i, node := range nodes {
		if !fleetID(node) || i > 0 && nodes[i-1] == node {
			return view, controlplane.ErrInvalid
		}
	}
	if request.Labels == nil {
		request.Labels = map[string]string{}
	}
	view = FleetPlacementRuleView{ID: request.ID, Scope: request.Scope, PlanID: request.PlanID, GroupID: request.GroupID, Enabled: request.Enabled, NodeIDs: nodes, Labels: request.Labels, InboundIDs: ids}
	digest, err := fleetDigest(view)
	if err != nil {
		return view, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.fleet-placement-rule/' || ?,0))", request.ID).Error; err != nil {
			return err
		}
		if err := requirePlacementRuleSource(tx, request); err != nil {
			return err
		}
		if err := requirePlacementInboundTemplates(tx, ids); err != nil {
			return err
		}
		var current model.FleetPlacementRule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", request.ID).Find(&current).Error; err != nil {
			return err
		}
		if current.ID != "" && (current.Scope != request.Scope || !equalPlacementScopeID(current.PlanID, request.PlanID) || !equalPlacementScopeID(current.GroupID, request.GroupID)) {
			return controlplane.ErrScope
		}
		if current.Revision > 0 && bytes.Equal(current.Digest, digest) && (uint64(current.Revision) == request.ExpectedRevision || uint64(current.Revision) == request.ExpectedRevision+1) {
			view.Revision, view.SHA256, view.Pending = uint64(current.Revision), hex.EncodeToString(digest), true
			return nil
		}
		if uint64(current.Revision) != request.ExpectedRevision {
			return controlplane.ErrRevision
		}
		nodeJSON, _ := json.Marshal(nodes)
		labelJSON, _ := json.Marshal(request.Labels)
		inboundJSON, _ := json.Marshal(ids)
		current = model.FleetPlacementRule{
			ID: request.ID, Revision: current.Revision + 1, Scope: request.Scope, PlanID: request.PlanID, GroupID: request.GroupID,
			Enabled: request.Enabled, NodeIDs: string(nodeJSON), Labels: string(labelJSON), InboundIDs: string(inboundJSON), Digest: digest, ActorID: actor.Id, UpdatedUnixMilli: time.Now().UnixMilli(),
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&current).Error; err != nil {
			return err
		}
		view.Revision, view.SHA256, view.Pending = uint64(current.Revision), hex.EncodeToString(digest), true
		return controlplane.QueuePlacementReevaluationTx(tx, "rule", current.ID)
	})
	return view, err
}

func equalPlacementScopeID(a, b *int) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func requirePlacementRuleSource(tx *gorm.DB, request FleetPlacementRuleRequest) error {
	if request.PlanID != nil {
		var plan model.Policy
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&plan, *request.PlanID).Error; err != nil {
			return err
		}
		if plan.Archived {
			return ErrFleetSource
		}
		return nil
	}
	var group model.ClientGroup
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&group, *request.GroupID).Error
}
