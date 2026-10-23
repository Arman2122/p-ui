package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func RunFleetSourceScheduler(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return controlplane.ErrInvalid
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return controlplane.ErrInvalid
	}
	cursor := ""
	for ctx.Err() == nil {
		query, cancel := context.WithTimeout(ctx, 5*time.Second)
		nodes, err := dueFleetSourceNodes(query, db, cursor)
		cancel()
		if err != nil {
			return err
		}
		for _, nodeID := range nodes {
			if err := processFleetSourceNode(ctx, db, nodeID); err != nil {
				return err
			}
			cursor = nodeID
		}
		if len(nodes) == 32 {
			continue
		}
		cursor = ""
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func dueFleetSourceNodes(ctx context.Context, db *gorm.DB, after string) ([]string, error) {
	const exists = `EXISTS (
SELECT 1 FROM fleet_source_works w WHERE w.node_id = fleet_nodes.id AND w.installation_id = fleet_nodes.installation_id
AND w.pending AND w.requires_stop = ? AND w.available_unix_milli <= ?)`
	now := time.Now().UnixMilli()
	query := db.WithContext(ctx).Model(&model.FleetNode{}).Where("enabled AND ("+exists+" OR "+exists+")", true, now, false, now)
	if after != "" {
		query = query.Where("id > ?", after)
	}
	var nodes []string
	err := query.Order("id").Limit(32).Pluck("id", &nodes).Error
	return nodes, err
}

func processFleetSourceNode(ctx context.Context, db *gorm.DB, nodeID string) error {
	workCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	items, err := PendingFleetSourceWork(workCtx, db, nodeID, 8)
	if err != nil {
		return err
	}
	for _, expected := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if workCtx.Err() != nil {
			break
		}
		action := ReconcileFleetSourceWorkTx
		if expected.RequiresStop {
			action = StopFleetSourceWorkTx
		}
		result, err := ProcessFleetSourceWork(workCtx, db, expected, action)
		if err != nil && !deferFleetSourceError(err) {
			return err
		}
		if err == nil && !result.Pending {
			continue
		}
		delay := time.Second
		if err == nil {
			expected, delay = result, 250*time.Millisecond
		}
		retry, stop := context.WithTimeout(ctx, 2*time.Second)
		err = DeferFleetSourceWork(retry, db, expected, delay)
		stop()
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func deferFleetSourceError(err error) bool {
	return settleFleetAdmissionError(err) || errors.Is(err, ErrFleetNetwork) || errors.Is(err, ErrFleetNetworkDependency) ||
		errors.Is(err, ErrFleetNetworkRevision) || errors.Is(err, ErrFleetNetworkFunding) || errors.Is(err, controlplane.ErrStopComposition)
}
