package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/xray"

	"gorm.io/gorm"
)

func (s *InboundService) MigrationRemoveOrphanedTraffics() {
	db := database.GetDB()
	query := fmt.Sprintf(
		"DELETE FROM client_traffics WHERE email NOT IN (SELECT email FROM clients) AND email NOT IN (SELECT %s %s)",
		database.JSONFieldText("client.value", "email"),
		database.JSONClientsFromInbound(),
	)
	result := db.Exec(query)
	if result.Error != nil {
		logger.Warning("MigrationRemoveOrphanedTraffics failed:", result.Error)
		return
	}
	if result.RowsAffected > 0 {
		logger.Infof("MigrationRemoveOrphanedTraffics: removed %d orphaned client_traffics row(s)", result.RowsAffected)
	}
}

func (s *InboundService) MigrationRequirements() {
	db := database.GetDB()
	tx := db.Begin()
	var err error
	defer func() {
		if err == nil {
			tx.Commit()
		} else {
			tx.Rollback()
		}
	}()

	if tx.Migrator().HasColumn(&model.Inbound{}, "all_time") {
		if err = tx.Migrator().DropColumn(&model.Inbound{}, "all_time"); err != nil {
			return
		}
	}
	if tx.Migrator().HasColumn(&xray.ClientTraffic{}, "all_time") {
		if err = tx.Migrator().DropColumn(&xray.ClientTraffic{}, "all_time"); err != nil {
			return
		}
	}
	if err = normalizeInboundShareAddressColumns(tx); err != nil {
		return
	}

	// Normalize "enable" columns to boolean. Partial migrations or mixed write
	// paths (public API inbound updates that flow through UpdateClientStat +
	// client syncs, plus node traffic merge deltas) can leave the column as
	// integer or with mixed interpretation. This prevents type problems in the
	// node traffic sync merge (SetRemoteTraffic) and makes the sync robust even
	// when inbounds are updated via the public API (incl. ones carrying
	// externalProxy in streamSettings).
	// Use DO block so it is idempotent and doesn't fail if already boolean.
	normalizeBool := func(table, col string) {
		tx.Exec(fmt.Sprintf(`
			DO $$
			BEGIN
				IF EXISTS (
					SELECT 1 FROM information_schema.columns
					WHERE table_name = '%s' AND column_name = '%s'
					  AND data_type <> 'boolean'
				) THEN
					ALTER TABLE %s ALTER COLUMN %s
						TYPE boolean USING (CASE WHEN %s::text IN ('1','true','t','yes') THEN true ELSE false END);
				END IF;
			END $$;`, table, col, table, col, col))
	}
	normalizeBool("inbounds", "enable")
	normalizeBool("client_traffics", "enable")
	normalizeBool("nodes", "enable")
	normalizeBool("clients", "enable")
	normalizeBool("api_tokens", "enabled")
	normalizeBool("outbound_subscriptions", "enabled")

	// Fix inbounds based problems
	var inbounds []*model.Inbound
	err = tx.Model(model.Inbound{}).Where("protocol IN (?)", []string{"vmess", "vless", "trojan", "shadowsocks", "hysteria"}).Find(&inbounds).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return
	}
	for inbound_index := range inbounds {
		settings := map[string]any{}
		_ = json.Unmarshal([]byte(inbounds[inbound_index].Settings), &settings)
		if raw, exists := settings["clients"]; exists && raw == nil {
			settings["clients"] = []any{}
		}
		clients, ok := settings["clients"].([]any)
		if ok {
			// Fix Client configuration problems
			newClients := make([]any, 0, len(clients))
			hasVisionFlow := false
			for client_index := range clients {
				// Dropped, and said out loud. Keeping it re-breaks the row for
				// ParseInboundSettingsClients, which every client read goes through.
				c, isObject := clients[client_index].(map[string]any)
				if !isObject {
					logger.Warningf("MigrationRequirements: inbound %d clients[%d] is %T, not an object; dropping it so the inbound's clients can be read at all",
						inbounds[inbound_index].Id, client_index, clients[client_index])
					continue
				}

				// Add email='' if it is not exists
				if _, ok := c["email"]; !ok {
					c["email"] = ""
				}

				// Convert string tgId to int64
				if _, ok := c["tgId"]; ok {
					tgId := c["tgId"]
					if tgIdStr, ok2 := tgId.(string); ok2 {
						tgIdInt64, err := strconv.ParseInt(strings.ReplaceAll(tgIdStr, " ", ""), 10, 64)
						if err == nil {
							c["tgId"] = tgIdInt64
						}
					}
				}

				// Remove "flow": "xtls-rprx-direct"
				if _, ok := c["flow"]; ok {
					if c["flow"] == "xtls-rprx-direct" {
						c["flow"] = ""
					}
				}
				if flow, _ := c["flow"].(string); flow == "xtls-rprx-vision" {
					hasVisionFlow = true
				}
				// Backfill created_at and updated_at — BACKFILL, both of them.
				// updated_at was assigned unconditionally, so every panel start
				// restamped every client of every vmess/vless/trojan/shadowsocks/
				// hysteria inbound as just-modified: the settings JSON churned on
				// each restart, and applyClientRecordMerge decides which side wins
				// a node sync by comparing exactly this field. F386.
				if _, ok := c["created_at"]; !ok {
					c["created_at"] = time.Now().Unix() * 1000
				}
				if _, ok := c["updated_at"]; !ok {
					c["updated_at"] = time.Now().Unix() * 1000
				}
				newClients = append(newClients, any(c))
			}
			settings["clients"] = newClients

			// Drop orphaned testseed: VLESS-only field, only meaningful when at least
			// one client uses the exact xtls-rprx-vision flow. Older versions saved it
			// for any non-empty flow (including the UDP variant) or kept it after the
			// flow was cleared from the client modal — clean those up here.
			if inbounds[inbound_index].Protocol == model.VLESS && !hasVisionFlow {
				delete(settings, "testseed")
			}

			// Assigned, not declared: the deferred commit/rollback reads the
			// function-scoped err, so a := here returns with it still nil and
			// COMMITS a half-migrated set of inbounds. F382.
			var modifiedSettings []byte
			modifiedSettings, err = json.MarshalIndent(settings, "", "  ")
			if err != nil {
				return
			}

			inbounds[inbound_index].Settings = string(modifiedSettings)
		}

		// Add client traffic row for all clients which has email
		var modelClients []model.Client
		modelClients, err = s.GetClients(inbounds[inbound_index])
		if err != nil {
			return
		}
		for _, modelClient := range modelClients {
			if len(modelClient.Email) > 0 {
				var count int64
				tx.Model(xray.ClientTraffic{}).Where("email = ?", modelClient.Email).Count(&count)
				if count == 0 {
					_ = s.AddClientStat(tx, inbounds[inbound_index].Id, &modelClient)
				}
			}
		}

		// Heal clients table for installs where the one-shot seeder
		// skipped clients due to a tgId-string unmarshal error.
		if syncErr := s.clientService.SyncInbound(tx, inbounds[inbound_index].Id, modelClients); syncErr != nil {
			logger.Warning("MigrationRequirements sync clients failed:", syncErr)
		}
	}
	// Checked, and only when there is something to save: GORM refuses an empty
	// slice, and rolling the whole migration back because a panel has no inbound
	// of these five protocols would be worse than the bug this closes. F386.
	if len(inbounds) > 0 {
		if err = tx.Save(inbounds).Error; err != nil {
			return
		}
	}

	// Remove orphaned traffics
	tx.Where("inbound_id = 0").Delete(xray.ClientTraffic{})

	// Migrate old MultiDomain to External Proxy
	var externalProxy []struct {
		Id             int
		Port           int
		StreamSettings string // text column; safer than []byte for the raw scan
	}
	externalProxyQuery := `select id, port, stream_settings
	from inbounds
	WHERE protocol in ('vmess','vless','trojan')
	  AND NULLIF(stream_settings, '')::jsonb #>> '{security}' = 'tls'
	  AND NULLIF(stream_settings, '')::jsonb #> '{tlsSettings,settings,domains}' IS NOT NULL`
	err = tx.Raw(externalProxyQuery).Scan(&externalProxy).Error
	if err != nil {
		return
	}

	for _, ep := range externalProxy {
		var reverses any
		var stream map[string]any
		_ = json.Unmarshal([]byte(ep.StreamSettings), &stream)
		if tlsSettings, ok := stream["tlsSettings"].(map[string]any); ok {
			if settings, ok := tlsSettings["settings"].(map[string]any); ok {
				if domains, ok := settings["domains"].([]any); ok {
					for _, domain := range domains {
						if domainMap, ok := domain.(map[string]any); ok {
							domainMap["forceTls"] = "same"
							domainMap["port"] = ep.Port
							domainMap["dest"] = domainMap["domain"].(string)
							delete(domainMap, "domain")
						}
					}
				}
				reverses = settings["domains"]
				delete(settings, "domains")
			}
		}
		stream["externalProxy"] = reverses
		newStream, _ := json.MarshalIndent(stream, " ", "  ")
		tx.Model(model.Inbound{}).Where("id = ?", ep.Id).Update("stream_settings", newStream)
	}

	// Legacy tag cleanup for old auto-generated tags (e.g. "0.0.0.0:443-...").
	tagCleanup := `UPDATE inbounds
		SET tag = REPLACE(tag, '0.0.0.0:', '')
		WHERE position('0.0.0.0:' in tag) > 0;`
	err = tx.Exec(tagCleanup).Error
	if err != nil {
		return
	}
}

func (s *InboundService) MigrateDB() {
	s.MigrationRequirements()
	s.MigrationRemoveOrphanedTraffics()
	s.MigrationRegateVisionFlow()
}

// Re-gates XTLS Vision on existing inbounds both ways: restores it where the
// inbound became eligible, clears it where it never was. Idempotent either way.
func (s *InboundService) MigrationRegateVisionFlow() {
	db := database.GetDB()
	// Every protocol, not only VLESS: the restore half answers no for the
	// others itself, and the strip half is the one that has to see a VMess
	// inbound still carrying a flow no transport of its can use. F387.
	var inbounds []*model.Inbound
	if err := db.Model(&model.Inbound{}).Find(&inbounds).Error; err != nil {
		logger.Warning("MigrationRegateVisionFlow: load inbounds failed:", err)
		return
	}
	for _, ib := range inbounds {
		action := "restored"
		restored, changed := s.restoreVisionFlowForEligibleInbound(nil, ib.Settings, ib.StreamSettings, ib.Protocol)
		if !changed {
			// An inbound stored with a flow its transport cannot carry hands out a
			// link that cannot connect, and nothing repaired those before this.
			restored, changed = stripVisionFlowForIneligibleInbound(ib.Settings, ib.StreamSettings, ib.Protocol)
			action = "cleared"
		}
		if !changed {
			continue
		}
		clients, err := s.GetClients(&model.Inbound{Settings: restored})
		if err != nil {
			logger.Warning("MigrationRegateVisionFlow: parse clients for inbound", ib.Id, "failed:", err)
			continue
		}
		err = db.Transaction(func(tx *gorm.DB) error {
			if e := tx.Model(&model.Inbound{}).Where("id = ?", ib.Id).Update("settings", restored).Error; e != nil {
				return e
			}
			return s.clientService.SyncInbound(tx, ib.Id, clients)
		})
		if err != nil {
			logger.Warning("MigrationRegateVisionFlow: update inbound", ib.Id, "failed:", err)
			continue
		}
		logger.Info("MigrationRegateVisionFlow:", action, "XTLS Vision flow on inbound", ib.Id)
	}
}
