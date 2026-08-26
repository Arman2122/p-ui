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

	/*
		Dropped through runtime.Runtime, which resolves the core that serves each
		inbound.

		This used to call s.xrayApi.DelInbound(tag) for every depleted local
		inbound, with no filter on protocol — so a depleted MTProto or kernel
		WireGuard inbound had its tag sent to Xray, which has never heard of it.
		The call failed, needRestart went true, and the inbound was never actually
		torn down in the core that serves it: a restart every poll, and traffic
		still flowing on a quota that had run out.

		The audit filed this as a multi-NODE defect. It is not — the query has
		always been node-scoped. It is multi-CORE.
	*/
	var doomed []*model.Inbound
	if err := tx.Model(model.Inbound{}).
		Where("((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?)) and enable = ? and node_id IS NULL", now, true).
		Find(&doomed).Error; err != nil {
		return false, 0, err
	}
	for _, ib := range doomed {
		rt, rtErr := s.runtimeFor(ib)
		if rtErr != nil {
			logger.Debug("Error resolving the runtime for inbound:", ib.Tag, rtErr)
			needRestart = true
			continue
		}
		if dropErr := rt.DelInbound(context.Background(), ib); dropErr != nil {
			logger.Debug("Error in disabling inbound by api:", dropErr)
			needRestart = true
			continue
		}
		logger.Debug("Inbound disabled by api:", ib.Tag)
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

func (s *InboundService) disableInvalidClients(tx *gorm.DB) (bool, int64, pendingClientDisable, error) {
	var pending pendingClientDisable
	needRestart := false
	cond, condArgs := depletedCond(tx)

	var depletedRows []xray.ClientTraffic
	err := tx.Model(xray.ClientTraffic{}).
		Where(cond+" AND enable = ?", append(condArgs, true)...).
		Find(&depletedRows).Error
	if err != nil {
		return false, 0, pending, err
	}
	if len(depletedRows) == 0 {
		return false, 0, pending, nil
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
			return false, 0, pending, err
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

	/*
		Revoked through runtime.Runtime, for the same reason the inbound teardown
		above is: s.xrayApi.RemoveUser(tag, email) went to Xray whatever core owned
		the inbound, so a depleted MTProto or kernel WireGuard client was struck
		from the database and left connected in the core that actually serves them.

		The local/remote split above is untouched and is why this is safe: remote
		targets never reach here, they go through disableRemoteClients below.
	*/
	for _, t := range localTargets {
		ib, getErr := s.GetInbound(t.InboundID)
		if getErr != nil {
			logger.Debug("Error loading the inbound to revoke on:", t.InboundID, getErr)
			needRestart = true
			continue
		}
		rt, rtErr := s.runtimeFor(ib)
		if rtErr != nil {
			logger.Debug("Error resolving the runtime for inbound:", ib.Tag, rtErr)
			needRestart = true
			continue
		}
		err1 := rt.RemoveUser(context.Background(), ib, t.Email)
		if err1 == nil {
			logger.Debug("Client disabled by api:", t.Email)
		} else if restartCannotFix(err1, t.Email) {
			logger.Debug("Nothing for xray to remove:", err1)
		} else {
			logger.Debug("Error in disabling client by api:", err1)
			needRestart = true
		}
	}

	/*
		The settings-JSON writes do NOT happen here. They are handed to the caller,
		which performs them after this transaction commits, under lockInbound.

		Doing them here was a read-modify-write on inbounds.settings with no lock,
		racing every admin mutation: an add that had read the blob before this poll
		saved its own copy afterwards and restored the depleted client's
		"enable": true. Permanently — the add's SyncInbound flips clients.enable back
		too, and the depleted predicate only ever selects rows already enabled, so no
		later poll re-detects it. A client with an exhausted quota served until
		someone resets its traffic by hand.

		Taking lockInbound here instead would DEADLOCK the panel: an admin mutation
		holds that lock while waiting for the serial writer, and the writer is what
		executes this pass. Neither side can advance and no timeout fires. The lock
		has to be taken in the same order every other writer takes it — lock first,
		writer second — which is only possible once this transaction is done.
	*/
	pending.local = localByInbound

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
			return needRestart, count, pending, result.Error
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

	// Likewise deferred. It is a node push, and runSerializedTx's own doc comment
	// asks for network I/O to stay out of the writer.
	pending.remote = make(map[int]map[string]struct{}, len(remoteByInbound))
	for inboundID, group := range remoteByInbound {
		emails := make(map[string]struct{}, len(group))
		for _, t := range group {
			emails[t.Email] = struct{}{}
		}
		pending.remote[inboundID] = emails
	}

	return needRestart, count, pending, nil
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
