package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func RunFleetPlacementScheduler(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return controlplane.ErrInvalid
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return controlplane.ErrInvalid
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- runFleetPlacementFanout(ctx, db) }()
	go func() { results <- runFleetPlacementNodes(ctx, db) }()
	err := <-results
	cancel()
	return errors.Join(err, <-results)
}

func runFleetPlacementFanout(ctx context.Context, db *gorm.DB) error {
	for ctx.Err() == nil {
		worked, err := ProcessFleetPlacementFanout(ctx, db, 32)
		if err != nil && !deferFleetSourceError(err) {
			return err
		}
		if worked && err == nil {
			continue
		}
		if err := waitFleetPlacementTick(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func waitFleetPlacementTick(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runFleetPlacementNodes(ctx context.Context, db *gorm.DB) error {
	cursor := ""
	for ctx.Err() == nil {
		query, cancel := context.WithTimeout(ctx, 5*time.Second)
		nodes, err := dueFleetPlacementNodes(query, db, cursor)
		cancel()
		if err != nil {
			return err
		}
		for _, nodeID := range nodes {
			if err := processFleetPlacementNode(ctx, db, nodeID); err != nil {
				return err
			}
			cursor = nodeID
		}
		if len(nodes) == 32 {
			continue
		}
		cursor = ""
		if err := waitFleetPlacementTick(ctx, 250*time.Millisecond); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func dueFleetPlacementNodes(ctx context.Context, db *gorm.DB, after string) ([]string, error) {
	const exists = `EXISTS (SELECT 1 FROM fleet_placement_intents i
WHERE i.node_id=fleet_nodes.id AND i.installation_id=fleet_nodes.installation_id
AND i.pending AND i.requires_stop=? AND i.available_unix_milli<=?)`
	now := time.Now().UnixMilli()
	query := db.WithContext(ctx).Model(&model.FleetNode{}).Where("enabled AND ("+exists+" OR "+exists+")", true, now, false, now)
	if after != "" {
		query = query.Where("id > ?", after)
	}
	var nodes []string
	err := query.Order("id").Limit(32).Pluck("id", &nodes).Error
	return nodes, err
}

func dueFleetPlacementIntents(ctx context.Context, db *gorm.DB, nodeID string, limit int) ([]model.FleetPlacementIntent, error) {
	if !fleetID(nodeID) || limit < 1 || limit > 128 {
		return nil, controlplane.ErrInvalid
	}
	var items []model.FleetPlacementIntent
	for _, stopping := range []bool{true, false} {
		var rows []model.FleetPlacementIntent
		if err := db.WithContext(ctx).Where("node_id=? AND installation_id=(SELECT installation_id FROM fleet_nodes WHERE id=?) AND pending AND requires_stop=? AND available_unix_milli<=?", nodeID, nodeID, stopping, time.Now().UnixMilli()).
			Order("available_unix_milli,id").Limit(limit - len(items)).Find(&rows).Error; err != nil {
			return nil, err
		}
		items = append(items, rows...)
		if len(items) == limit {
			break
		}
	}
	return items, nil
}

func processFleetPlacementNode(ctx context.Context, db *gorm.DB, nodeID string) error {
	work, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	items, err := dueFleetPlacementIntents(work, db, nodeID, 8)
	if err != nil {
		return err
	}
	for _, expected := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if work.Err() != nil {
			break
		}
		if _, err := ProcessFleetPlacementIntent(work, db, expected); err != nil && !deferFleetSourceError(err) {
			if work.Err() != nil && ctx.Err() == nil {
				break
			}
			return err
		}
	}
	return ctx.Err()
}
