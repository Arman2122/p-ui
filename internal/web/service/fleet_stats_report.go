package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

var (
	ErrFleetStatsCapacity = errors.New("fleet statistics exceed bounded report capacity; no report was sent")
	ErrFleetStatsDB       = errors.New("fleet statistics database handle is no longer current")
	ErrFleetStatsTx       = errors.New("fleet statistics require an independent read-only transaction")
)

const (
	fleetStatsCandidateLimit = 4096
	fleetStatsNodeLimit      = 1024
)

type FleetStatsClient struct {
	Traffic          core.ClientTraffic
	TelegramID       int64
	Attached         bool
	EnabledInbound   bool
	TelegramEligible bool
}

type FleetStatsNode struct {
	ID, Name, State                                     string
	DesiredRevision, AppliedRevision, LastSeenUnixMilli int64
	ReceivedUnixMilli, ObservedUnixMilli                int64
	Runtimes                                            []FleetStatsRuntime
}

type FleetStatsRuntime struct {
	ID, State                            string
	ReceivedUnixMilli, ObservedUnixMilli int64
}

type FleetStatsSnapshot struct {
	GeneratedAt                             time.Time
	Schedule                                string
	Backup                                  bool
	TrafficThreshold, ExpiryThresholdMillis int64
	Clients                                 []FleetStatsClient
	Nodes                                   []FleetStatsNode
	ClientCount, DisabledClients            int64
	MeasuredBytes, OpeningBytes             int64
}

func ValidateFleetStatsDB(ctx context.Context, db *gorm.DB) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if db == nil || db.Statement == nil {
		return ErrFleetStatsDB
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return ErrFleetStatsTx
	}
	current := database.GetDB()
	if current == nil {
		return ErrFleetStatsDB
	}
	boundPool, err := db.DB()
	if err != nil {
		return err
	}
	currentPool, err := current.DB()
	if err != nil {
		return err
	}
	if boundPool != currentPool {
		return ErrFleetStatsDB
	}
	return nil
}

// The caller owns this database handle and replaces the job when the database is restored.
func ReadFleetStats(ctx context.Context, db *gorm.DB, now time.Time) (FleetStatsSnapshot, error) {
	result := FleetStatsSnapshot{GeneratedAt: now}
	if db == nil || now.IsZero() {
		return result, errors.New("fleet statistics require a database and snapshot time")
	}
	if err := ValidateFleetStatsDB(ctx, db); err != nil {
		return result, err
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	err := db.WithContext(readCtx).Transaction(func(tx *gorm.DB) error {
		if err := readFleetStatsSettings(tx, &result); err != nil {
			return err
		}
		if err := readFleetStatsClients(tx, &result); err != nil {
			return err
		}
		return readFleetStatsNodes(tx, &result)
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err == nil {
		err = ValidateFleetStatsDB(ctx, db)
	}
	return result, err
}

func readFleetStatsSettings(tx *gorm.DB, result *FleetStatsSnapshot) error {
	keys := []string{"tgRunTime", "tgBotBackup", "trafficDiff", "expireDiff"}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = defaultValueMap[key]
	}
	var settings []model.Setting
	if err := tx.Where("key IN ?", keys).Find(&settings).Error; err != nil {
		return err
	}
	for _, setting := range settings {
		values[setting.Key] = setting.Value
	}
	var err error
	result.Schedule = values["tgRunTime"]
	result.Backup, err = strconv.ParseBool(values["tgBotBackup"])
	if err != nil {
		return err
	}
	for _, threshold := range []struct {
		key        string
		multiplier int64
		target     *int64
	}{{"trafficDiff", 1 << 30, &result.TrafficThreshold}, {"expireDiff", 86400000, &result.ExpiryThresholdMillis}} {
		value, err := strconv.ParseInt(values[threshold.key], 10, 64)
		if err != nil || value > math.MaxInt64/threshold.multiplier {
			return errors.New("invalid fleet notification threshold")
		}
		*threshold.target = max(0, value) * threshold.multiplier
	}
	return nil
}

func readFleetStatsClients(tx *gorm.DB, result *FleetStatsSnapshot) error {
	var after int
	for {
		var clients []struct {
			ID               int
			Email            string
			TgID             int64
			Attached         bool
			EnabledInbound   bool
			TelegramEligible bool
		}
		if err := tx.Table("clients c").Select(`c.id, c.email, c.tg_id,
EXISTS(SELECT 1 FROM client_inbounds ci WHERE ci.client_id = c.id) AS attached,
EXISTS(SELECT 1 FROM client_inbounds ci JOIN inbounds i ON i.id = ci.inbound_id WHERE ci.client_id = c.id AND i.enable) AS enabled_inbound,
EXISTS(SELECT 1 FROM clients peer JOIN fleet_clients pf ON pf.panel_client_id = peer.id AND pf.owned JOIN client_inbounds pi ON pi.client_id = peer.id JOIN inbounds ib ON ib.id = pi.inbound_id WHERE c.tg_id <> 0 AND peer.tg_id = c.tg_id AND ib.enable) AS telegram_eligible`).
			Joins("JOIN fleet_clients fc ON fc.panel_client_id = c.id AND fc.owned").Where("c.id > ?", after).Order("c.id").Limit(128).Scan(&clients).Error; err != nil {
			return err
		}
		if len(clients) == 0 {
			return nil
		}
		emails := make([]string, len(clients))
		for i, client := range clients {
			emails[i] = client.Email
		}
		traffic := make(map[string]*core.ClientTraffic, len(clients))
		if err := projectFleetTraffic(tx, emails, traffic); err != nil {
			return err
		}
		for _, client := range clients {
			row := traffic[client.Email]
			if row == nil || row.Accounting == nil || row.Accounting.Source != "fleet" {
				return ErrFleetUsage
			}
			candidate := FleetStatsClient{Traffic: *row, TelegramID: client.TgID, Attached: client.Attached, EnabledInbound: client.EnabledInbound, TelegramEligible: client.TelegramEligible}
			if err := foldFleetStatsClient(result, candidate); err != nil {
				return err
			}
		}
		after = clients[len(clients)-1].ID
	}
}

func FleetStatsNearLimit(traffic core.ClientTraffic, snapshot FleetStatsSnapshot) bool {
	if traffic.Accounting == nil || traffic.Accounting.TermID == "" {
		return false
	}
	return traffic.ExpiryTime > 0 && traffic.ExpiryTime-snapshot.GeneratedAt.UnixMilli() < snapshot.ExpiryThresholdMillis ||
		traffic.Total > 0 && traffic.Total-traffic.Up-traffic.Down < snapshot.TrafficThreshold
}

func foldFleetStatsClient(result *FleetStatsSnapshot, client FleetStatsClient) error {
	accounting := client.Traffic.Accounting
	for _, pair := range []struct {
		total *int64
		value int64
	}{{&result.MeasuredBytes, accounting.MeasuredUploaded}, {&result.MeasuredBytes, accounting.MeasuredDownloaded}, {&result.OpeningBytes, accounting.OpeningUploaded}, {&result.OpeningBytes, accounting.OpeningDownloaded}} {
		if pair.value < 0 || *pair.total > math.MaxInt64-pair.value {
			return ErrFleetUsage
		}
		*pair.total += pair.value
	}
	result.ClientCount++
	if client.EnabledInbound && !client.Traffic.Enable {
		result.DisabledClients++
	}
	near := client.Traffic.Enable && FleetStatsNearLimit(client.Traffic, *result)
	userCandidate := client.TelegramID != 0 && client.Attached && client.TelegramEligible && (near || !client.Traffic.Enable)
	if !userCandidate && (!near || !client.EnabledInbound) {
		return nil
	}
	if len(result.Clients) >= fleetStatsCandidateLimit {
		return ErrFleetStatsCapacity
	}
	result.Clients = append(result.Clients, client)
	return nil
}

func readFleetStatsNodes(tx *gorm.DB, result *FleetStatsSnapshot) error {
	var after string
	for {
		var nodes []model.FleetNode
		query := tx.Order("id").Limit(128)
		if after != "" {
			query = query.Where("id > ?", after)
		}
		if err := query.Find(&nodes).Error; err != nil {
			return err
		}
		if len(nodes) == 0 {
			return nil
		}
		ids := make([]string, len(nodes))
		for i, node := range nodes {
			ids[i] = node.ID
		}
		var observations []model.FleetNodeObservation
		var runtimes []model.FleetRuntimeJournal
		if err := tx.Where("node_id IN ?", ids).Find(&observations).Error; err != nil {
			return err
		}
		if err := tx.Where("node_id IN ?", ids).Order("node_id,runtime_id").Find(&runtimes).Error; err != nil {
			return err
		}
		byNode := make(map[string]model.FleetNodeObservation, len(nodes))
		byRuntime := make(map[string][]model.FleetRuntimeJournal, len(nodes))
		for _, row := range observations {
			byNode[row.NodeID] = row
		}
		for _, row := range runtimes {
			byRuntime[row.NodeID] = append(byRuntime[row.NodeID], row)
		}
		for _, node := range nodes {
			if len(result.Nodes) >= fleetStatsNodeLimit {
				return ErrFleetStatsCapacity
			}
			view, err := fleetStatsNode(node, byNode[node.ID], byRuntime[node.ID], result.GeneratedAt)
			if err != nil {
				return err
			}
			result.Nodes = append(result.Nodes, view)
		}
		after = nodes[len(nodes)-1].ID
	}
}

func fleetStatsFresh(timestamp int64, now time.Time) bool {
	return timestamp > 0 && timestamp >= now.Add(-45*time.Second).UnixMilli() && timestamp <= now.Add(5*time.Second).UnixMilli()
}

func fleetStatsNode(node model.FleetNode, observation model.FleetNodeObservation, runtimes []model.FleetRuntimeJournal, now time.Time) (FleetStatsNode, error) {
	view := FleetStatsNode{
		ID: node.ID, Name: node.Name, State: "unknown", DesiredRevision: node.DesiredRevision, AppliedRevision: node.AppliedRevision,
		LastSeenUnixMilli: node.LastSeenUnixMilli, ReceivedUnixMilli: observation.ReceivedUnixMilli, ObservedUnixMilli: observation.ObservedUnixMilli,
	}
	fresh := node.Enabled && fleetStatsFresh(node.LastSeenUnixMilli, now)
	if !node.Enabled {
		view.State = "disabled"
	} else if node.LastSeenUnixMilli > 0 && !fresh {
		view.State = "disconnected"
	} else if observation.ReceivedUnixMilli > 0 {
		view.State = "stale"
		if fresh && observation.RuntimeGeneration == node.AuthorityGeneration && fleetStatsFresh(observation.ReceivedUnixMilli, now) && fleetStatsFresh(observation.ObservedUnixMilli, now) {
			view.State = "unknown"
			var conditions []struct{ Ready bool }
			if err := json.Unmarshal([]byte(observation.ConditionsJSON), &conditions); err != nil {
				return view, err
			}
			if len(conditions) > 0 && observation.ReportedAppliedRevision == node.AppliedRevision && node.AppliedRevision > 0 {
				view.State = "ready"
				for _, condition := range conditions {
					if !condition.Ready {
						view.State = "degraded"
					}
				}
			}
		}
	}
	for _, runtime := range runtimes {
		state := "unknown"
		if runtime.ReceivedUnixMilli > 0 {
			state = "stale"
			if fresh && runtime.InstallationID == node.InstallationID && fleetStatsFresh(runtime.ReceivedUnixMilli, now) && fleetStatsFresh(runtime.ObservedUnixMilli, now) {
				state = "degraded"
				if runtime.Ready {
					state = "ready"
				}
			}
		}
		view.Runtimes = append(view.Runtimes, FleetStatsRuntime{ID: runtime.RuntimeID, State: state, ReceivedUnixMilli: runtime.ReceivedUnixMilli, ObservedUnixMilli: runtime.ObservedUnixMilli})
	}
	return view, nil
}
