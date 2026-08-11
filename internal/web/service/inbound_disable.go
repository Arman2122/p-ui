package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/xray"

	"gorm.io/gorm"
)

func (s *InboundService) disableInvalidInbounds(tx *gorm.DB) (bool, int64, error) {
	now := time.Now().Unix() * 1000
	needRestart := false

	if process := currentXrayProcess(); process != nil {
		var tags []string
		err := tx.Table("inbounds").
			Select("inbounds.tag").
			Where("((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?)) and enable = ? and node_id IS NULL", now, true).
			Scan(&tags).Error
		if err != nil {
			return false, 0, err
		}
		_ = s.xrayApi.Init(process.GetAPIPort())
		for _, tag := range tags {
			err1 := s.xrayApi.DelInbound(tag)
			if err1 == nil {
				logger.Debug("Inbound disabled by api:", tag)
			} else {
				logger.Debug("Error in disabling inbound by api:", err1)
				needRestart = true
			}
		}
		s.xrayApi.Close()
	}

	result := tx.Model(model.Inbound{}).
		Where("((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?)) and enable = ? and node_id IS NULL", now, true).
		Update("enable", false)
	err := result.Error
	count := result.RowsAffected
	return needRestart, count, err
}

const globalTrafficFreshWindow = 24 * time.Hour

func globalTrafficFreshSince() int64 {
	return time.Now().Add(-globalTrafficFreshWindow).UnixMilli()
}

// usedBytesLocal is a client's usage as this panel's own counters hold it.
// Columns are qualified because the tier pass reads the same expression from a
// query that joins other tables.
const usedBytesLocal = `(client_traffics.up + client_traffics.down)`

// usedBytesCrossPanel raises that to the highest combined figure any master
// still refreshes, which is what makes one quota count across panels. Only rows
// a master refreshed recently are folded in. Placeholders: freshSince.
const usedBytesCrossPanel = `GREATEST(client_traffics.up + client_traffics.down, COALESCE((
		SELECT MAX(g.up + g.down) FROM client_global_traffics g
		WHERE g.email = client_traffics.email
			AND g.updated_at >= ?
	), 0))`

/*
UsedBytesExpr is the ONE SQL definition of how much a client has used, together
with the arguments it binds.

Depletion and tier evaluation both read it, so a client cannot be over quota by
one definition and under a threshold by another — which is exactly what happens
when the cross-panel rows are folded into only one of them, and it is invisible
until a customer on a node a master pushes to complains.
*/
func UsedBytesExpr(tx *gorm.DB) (string, []any) {
	if crossPanelTrafficIsLive(tx) {
		return usedBytesCrossPanel, []any{globalTrafficFreshSince()}
	}
	return usedBytesLocal, nil
}

// crossPanelTrafficIsLive reports whether a master still pushes usage here. The
// cross-panel expression is a correlated subquery that turns every poll into a
// full client_traffics scan, and on a panel with no master it can never match (#5392).
func crossPanelTrafficIsLive(tx *gorm.DB) bool {
	var probe int64
	err := tx.Model(&model.ClientGlobalTraffic{}).
		Where("updated_at >= ?", globalTrafficFreshSince()).
		Limit(1).Count(&probe).Error
	return err == nil && probe > 0
}

// depletedCondFrom wraps one usage expression in the quota and expiry test, so
// the predicate cannot carry a second opinion about what a client has used.
// Placeholders: whatever used binds, then now.
func depletedCondFrom(used string) string {
	return `((total > 0 AND ` + used + ` >= total)
	OR (expiry_time > 0 AND expiry_time <= ?))`
}

// The two depletion predicates, each built from the matching usage expression.
var (
	depletedClientsCond      = depletedCondFrom(usedBytesCrossPanel)
	depletedClientsCondLocal = depletedCondFrom(usedBytesLocal)
)

// depletedCond returns the predicate matching depleted clients together with the
// arguments it binds, around whichever usage expression this panel is using.
func depletedCond(tx *gorm.DB) (string, []any) {
	used, args := UsedBytesExpr(tx)
	return depletedCondFrom(used), append(args, time.Now().UnixMilli())
}

func (s *InboundService) disableInvalidClients(tx *gorm.DB) (bool, int64, error) {
	needRestart := false
	cond, condArgs := depletedCond(tx)

	var depletedRows []xray.ClientTraffic
	err := tx.Model(xray.ClientTraffic{}).
		Where(cond+" AND enable = ?", append(condArgs, true)...).
		Find(&depletedRows).Error
	if err != nil {
		return false, 0, err
	}
	if len(depletedRows) == 0 {
		return false, 0, nil
	}

	depletedEmails := make([]string, 0, len(depletedRows))
	for i := range depletedRows {
		if depletedRows[i].Email == "" {
			continue
		}
		depletedEmails = append(depletedEmails, depletedRows[i].Email)
	}

	type target struct {
		InboundID int  `gorm:"column:inbound_id"`
		NodeID    *int `gorm:"column:node_id"`
		Tag       string
		Email     string
	}
	var targets []target
	if len(depletedEmails) > 0 {
		err = tx.Raw(`
			SELECT inbounds.id AS inbound_id, inbounds.node_id AS node_id,
			       inbounds.tag AS tag, clients.email AS email
			FROM clients
			JOIN client_inbounds ON client_inbounds.client_id = clients.id
			JOIN inbounds        ON inbounds.id = client_inbounds.inbound_id
			WHERE clients.email IN ?
		`, depletedEmails).Scan(&targets).Error
		if err != nil {
			return false, 0, err
		}
	}

	var localTargets []target
	localByInbound := make(map[int]map[string]struct{})
	remoteByInbound := make(map[int][]target)
	for _, t := range targets {
		if t.NodeID == nil {
			localTargets = append(localTargets, t)
			if localByInbound[t.InboundID] == nil {
				localByInbound[t.InboundID] = make(map[string]struct{})
			}
			localByInbound[t.InboundID][t.Email] = struct{}{}
		} else {
			remoteByInbound[t.InboundID] = append(remoteByInbound[t.InboundID], t)
		}
	}

	if process := currentXrayProcess(); process != nil && len(localTargets) > 0 {
		_ = s.xrayApi.Init(process.GetAPIPort())
		for _, t := range localTargets {
			err1 := s.xrayApi.RemoveUser(t.Tag, t.Email)
			if err1 == nil {
				logger.Debug("Client disabled by api:", t.Email)
			} else if restartCannotFix(err1, t.Email) {
				logger.Debug("Nothing for xray to remove:", err1)
			} else {
				logger.Debug("Error in disabling client by api:", err1)
				needRestart = true
			}
		}
		s.xrayApi.Close()
	}

	for inboundID, emails := range localByInbound {
		if _, _, mErr := s.markClientsDisabledInSettings(tx, inboundID, emails); mErr != nil {
			logger.Warning("disableInvalidClients: settings.JSON sync failed for inbound", inboundID, ":", mErr)
		}
	}

	// Flip the rows already collected above by primary key instead of
	// re-evaluating the depleted predicate, which was a second full scan of
	// client_traffics on every poll. Sorted ids keep the lock order stable.
	ids := make([]int, 0, len(depletedRows))
	for i := range depletedRows {
		ids = append(ids, depletedRows[i].Id)
	}
	slices.Sort(ids)
	var count int64
	for _, batch := range chunkInts(ids, sqlInChunk) {
		result := tx.Model(xray.ClientTraffic{}).
			Where("id IN ? AND enable = ?", batch, true).
			Update("enable", false)
		if result.Error != nil {
			return needRestart, count, result.Error
		}
		count += result.RowsAffected
	}

	if len(depletedEmails) > 0 {
		if err := tx.Model(&model.ClientRecord{}).
			Where("email IN ?", depletedEmails).
			Updates(map[string]any{"enable": false, "updated_at": time.Now().UnixMilli()}).Error; err != nil {
			logger.Warning("disableInvalidClients update clients.enable:", err)
		}
	}

	for inboundID, group := range remoteByInbound {
		emails := make(map[string]struct{}, len(group))
		for _, t := range group {
			emails[t.Email] = struct{}{}
		}
		if pushErr := s.disableRemoteClients(tx, inboundID, emails); pushErr != nil {
			logger.Warning("disableInvalidClients: push to remote failed for inbound", inboundID, ":", pushErr)
			needRestart = true
		}
	}

	return needRestart, count, nil
}

// markClientsDisabledInSettings flips client.enable=false in the inbound's
// stored settings JSON for the given emails and returns both the pre and
// post snapshots so a caller pushing to a remote node has the diff to hand.
func (s *InboundService) markClientsDisabledInSettings(tx *gorm.DB, inboundID int, emails map[string]struct{}) (oldIb, newIb *model.Inbound, err error) {
	var ib model.Inbound
	if err := tx.Model(&model.Inbound{}).Where("id = ?", inboundID).First(&ib).Error; err != nil {
		return nil, nil, err
	}
	snapshot := ib

	settings := map[string]any{}
	if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
		return nil, nil, err
	}
	clients, _ := settings["clients"].([]any)
	now := time.Now().Unix() * 1000
	mutated := false
	for i := range clients {
		entry, ok := clients[i].(map[string]any)
		if !ok {
			continue
		}
		email, _ := entry["email"].(string)
		if _, hit := emails[email]; !hit {
			continue
		}
		if cur, _ := entry["enable"].(bool); !cur {
			continue
		}
		entry["enable"] = false
		entry["updated_at"] = now
		clients[i] = entry
		mutated = true
	}
	if !mutated {
		return &snapshot, &ib, nil
	}
	settings["clients"] = clients
	bs, marshalErr := json.MarshalIndent(settings, "", "  ")
	if marshalErr != nil {
		return nil, nil, marshalErr
	}
	ib.Settings = string(bs)
	if err := tx.Model(&model.Inbound{}).Where("id = ?", inboundID).
		Update("settings", ib.Settings).Error; err != nil {
		return nil, nil, err
	}
	return &snapshot, &ib, nil
}

// disableRemoteClients flips the clients off in the inbound's stored settings
// and pushes the updated inbound to its node, which applies it to its own
// running Xray. That push is the whole reconcile — restarting the node's Xray
// afterwards would drop every live connection on the node for nothing (#5740).
func (s *InboundService) disableRemoteClients(tx *gorm.DB, inboundID int, emails map[string]struct{}) error {
	oldSnapshot, ib, err := s.markClientsDisabledInSettings(tx, inboundID, emails)
	if err != nil {
		return err
	}

	rt, err := s.runtimeFor(ib)
	if err != nil {
		return err
	}
	if err := rt.UpdateInbound(context.Background(), oldSnapshot, ib); err != nil {
		return err
	}
	return nil
}

/*
restartCannotFix reports a RemoveUser failure that restarting Xray would not
resolve, so cutting one client off never drops everyone else's connections.

Two cases. The user is already gone, which is the common one. Or the tag is not
Xray's at all: an mtproto inbound is served by an mtg sidecar, so Xray answers
"handler not found" for every depleted MTProto client, and no restart can
conjure a handler for another core's inbound. Such a client is cut off by its
own core — the mtproto reconcile drops the secret once client_traffics.enable
goes false.
*/
func restartCannotFix(err error, email string) bool {
	msg := err.Error()
	return strings.Contains(msg, fmt.Sprintf("User %s not found.", email)) ||
		strings.Contains(msg, "handler not found:")
}
