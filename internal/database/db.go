package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/util/random"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

const (
	defaultUsername = "admin"
	defaultPassword = "admin"
)

func allModels() []any {
	return []any{
		&model.User{},
		&model.Inbound{},
		&model.OutboundTraffics{},
		&model.Setting{},
		&model.InboundClientIps{},
		&core.ClientTraffic{},
		&model.HistoryOfSeeders{},
		&model.Node{},
		&model.ApiToken{},
		&model.ClientRecord{},
		&model.ClientInbound{},
		&model.ClientCredential{},
		&model.ClientExternalLink{},
		&model.ClientGroup{},
		&model.InboundFallback{},
		&model.Host{},
		&model.NodeClientTraffic{},
		&model.NodeClientIp{},
		&model.ClientGlobalTraffic{},
		&model.OutboundSubscription{},
		&model.Egress{},
		&model.Policy{},
		&model.ClientPolicy{},
	}
}

func initModels() error {
	// Runs before AutoMigrate so the decided composite key is the one that ships.
	if err := migrateClientCredentialsTable(); err != nil {
		return err
	}
	models := allModels()
	for _, mdl := range models {
		if postgresModelSettled(mdl) {
			continue
		}
		if err := db.AutoMigrate(mdl); err != nil {
			if isIgnorableDuplicateColumnErr(db, err, mdl) {
				log.Printf("Ignoring duplicate column during auto migration for %T: %v", mdl, err)
				continue
			}
			log.Printf("Error auto migrating model: %v", err)
			return err
		}
	}
	if err := migrateHostVerifyPeerCertByNameColumn(); err != nil {
		return err
	}
	if err := migratePolicyAssignmentForeignKey(); err != nil {
		return err
	}
	if err := normalizeApiTokenCreatedAtSeconds(); err != nil {
		return err
	}
	if err := dropLegacyForeignKeys(); err != nil {
		return err
	}
	if err := pruneOrphanedClientInbounds(); err != nil {
		return err
	}
	if err := pruneOrphanedHosts(); err != nil {
		return err
	}
	if err := normalizeInboundSubSortIndex(); err != nil {
		return err
	}
	if err := repairOverflowedTrafficCounters(); err != nil {
		return err
	}
	if err := dedupeInboundSettingsClients(); err != nil {
		return err
	}
	if err := migrateLegacySocksInboundsToMixed(); err != nil {
		return err
	}
	if err := migrateShadowsocksRemovedCiphers(); err != nil {
		return err
	}
	if err := migrateVmessRemovedSecurities(); err != nil {
		return err
	}
	if err := migrateTgIDIndex(); err != nil {
		return err
	}
	if err := migrateDepletedEmailIndex(); err != nil {
		return err
	}
	if err := migrateSyncOrphanColumns(); err != nil {
		return err
	}
	if err := migrateEgressConstraints(); err != nil {
		return err
	}
	if err := resyncPostgresSequences(db, models); err != nil {
		log.Printf("Error resyncing postgres sequences: %v", err)
		return err
	}
	return nil
}

// resyncPostgresSequences sets each model's id sequence to MAX(id); idempotent. Id-less
// composite-PK tables are skipped — Postgres rejects MAX(id) at parse time and logs it (#5665).
/*
sequenceMustNotRewind are the tables whose id must never be handed out twice.

An egress id is not a label: it names host-global kernel state (routing table
30000+id, ip rule priority 31000+id, device peg<id>). Deleting the newest egress
and rebooting would otherwise set the sequence back to MAX(id) and hand the same
id — and whatever of that state still exists — to the next egress created.
*/
var sequenceMustNotRewind = map[string]bool{"egresses": true}

func resyncPostgresSequences(gdb *gorm.DB, models []any) error {
	for _, m := range models {
		t, ok := tableWithIdColumn(gdb, m)
		if !ok || sequenceMustNotRewind[t] {
			continue
		}
		// t comes from the trusted model set parsed by GORM, not user input, so
		// interpolating it as an identifier is safe. We ignore errors per-table.
		_ = gdb.Exec(
			`SELECT setval(pg_get_serial_sequence(?, 'id'), COALESCE((SELECT MAX(id) FROM "`+t+`"), 1), true)
			 WHERE pg_get_serial_sequence(?, 'id') IS NOT NULL`,
			t, t,
		).Error
	}
	return nil
}

// tableWithIdColumn resolves a model's table name and reports whether its GORM
// schema maps an "id" database column.
func tableWithIdColumn(gdb *gorm.DB, m any) (string, bool) {
	stmt := &gorm.Statement{DB: gdb}
	if err := stmt.Parse(m); err != nil {
		return "", false
	}
	if stmt.Schema == nil || stmt.Schema.LookUpField("id") == nil {
		return "", false
	}
	return stmt.Table, true
}

func postgresModelSettled(mdl any) bool {
	migrator := db.Migrator()
	if !migrator.HasTable(mdl) {
		return false
	}
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(mdl); err != nil || stmt.Schema == nil {
		return false
	}
	for _, dbName := range stmt.Schema.DBNames {
		if !migrator.HasColumn(mdl, dbName) {
			return false
		}
	}
	for _, idx := range stmt.Schema.ParseIndexes() {
		if !migrator.HasIndex(mdl, idx.Name) {
			return false
		}
	}
	return true
}

func dropLegacyForeignKeys() error {
	if err := db.Exec("ALTER TABLE client_traffics DROP CONSTRAINT IF EXISTS fk_inbounds_client_stats").Error; err != nil {
		log.Printf("Error dropping legacy foreign key fk_inbounds_client_stats: %v", err)
		return err
	}
	return nil
}

const policyAssignmentFK = "fk_client_policies_policy"

/*
migratePolicyAssignmentForeignKey adds the constraint AutoMigrate cannot express.

ON DELETE SET NULL and not CASCADE: deleting a plan must leave every assignment
row behind, visible and unresolved, so the UI can report which clients lost their
plan. A cascade would vaporise the evidence and the operator would only learn
from a customer that the throttle stopped.
*/
func migratePolicyAssignmentForeignKey() error {
	if !db.Migrator().HasTable(&model.ClientPolicy{}) || !db.Migrator().HasTable(&model.Policy{}) {
		return nil
	}
	var present int64
	err := db.Raw(`SELECT COUNT(*) FROM pg_constraint WHERE conname = ?`, policyAssignmentFK).Scan(&present).Error
	if err != nil {
		return err
	}
	if present > 0 {
		return nil
	}
	// A row naming a plan that never existed would refuse the constraint; clearing
	// it leaves the assignment unresolved, which is what the FK itself would do.
	if err := db.Exec(`UPDATE client_policies SET policy_id = NULL
		WHERE policy_id IS NOT NULL AND policy_id NOT IN (SELECT id FROM policies)`).Error; err != nil {
		return err
	}
	return db.Exec(`ALTER TABLE client_policies ADD CONSTRAINT ` + policyAssignmentFK +
		` FOREIGN KEY (policy_id) REFERENCES policies(id) ON DELETE SET NULL`).Error
}

// AutoMigrate adds the column; this only backfills the NULLs the ALTER TABLE on
// an already populated table leaves behind, so the reaper's predicate never
// compares to NULL.
func migrateSyncOrphanColumns() error {
	if !db.Migrator().HasColumn(&model.ClientRecord{}, "sync_orphaned_at") {
		return nil
	}
	return db.Exec("UPDATE clients SET sync_orphaned_at = 0 WHERE sync_orphaned_at IS NULL").Error
}

/*
migrateEgressConstraints pins the two egress invariants the service layer checks
but, being two un-transacted statements apart, cannot enforce by itself.

The FK is what closes a delete racing an attach: without it the inbound is left
pointing at a row that is gone, desired() emits no rule for an id it never read,
and that inbound egresses with the server's own identity while the panel still
reports it attached. RESTRICT rather than SET NULL because a silent detach is
that same leak with the database agreeing to it.
*/
func migrateEgressConstraints() error {
	if !db.Migrator().HasTable(&model.Egress{}) || !db.Migrator().HasColumn(&model.Inbound{}, "egress_id") {
		return nil
	}
	// A reference to a row that is gone already converges to no rule, so clearing
	// it makes the column say what the kernel has been doing — as pruneOrphanedHosts does.
	err := db.Exec(
		`UPDATE inbounds SET egress_id = NULL WHERE egress_id IS NOT NULL AND egress_id NOT IN (SELECT id FROM egresses)`,
	).Error
	if err != nil {
		return err
	}
	if err := addConstraintOnce("inbounds", "fk_inbounds_egress",
		`FOREIGN KEY (egress_id) REFERENCES egresses(id) ON DELETE RESTRICT`); err != nil {
		return err
	}
	// NOT VALID so an out-of-band row that somehow reached the table cannot turn
	// this into a panel that refuses to boot; every write after it is still checked.
	return addConstraintOnce("egresses", "ck_egresses_id_band", `CHECK (id BETWEEN 1 AND 999) NOT VALID`)
}

// addConstraintOnce is the idempotent half: pg_constraint is asked by table and
// name rather than GORM's migrator, which guesses the name from the model. The
// namespace filter is load-bearing — every test runs in a schema of its own.
func addConstraintOnce(table, name, definition string) error {
	var count int64
	err := db.Raw(
		`SELECT count(*) FROM pg_constraint c
		 JOIN pg_class t ON t.oid = c.conrelid
		 JOIN pg_namespace n ON n.oid = t.relnamespace
		 WHERE n.nspname = current_schema() AND t.relname = ? AND c.conname = ?`,
		table, name,
	).Scan(&count).Error
	if err != nil || count > 0 {
		return err
	}
	// table, name and definition are literals from this file, never user input.
	return db.Exec(`ALTER TABLE ` + table + ` ADD CONSTRAINT ` + name + ` ` + definition).Error
}

func migrateHostVerifyPeerCertByNameColumn() error {
	if !db.Migrator().HasColumn(&model.Host{}, "verify_peer_cert_by_name") {
		return nil
	}
	var dataType string
	if err := db.Raw(
		`SELECT data_type FROM information_schema.columns WHERE table_name = 'hosts' AND column_name = 'verify_peer_cert_by_name'`,
	).Scan(&dataType).Error; err != nil {
		return err
	}
	if dataType != "boolean" {
		return nil
	}
	if err := db.Exec(`ALTER TABLE hosts ALTER COLUMN verify_peer_cert_by_name DROP DEFAULT`).Error; err != nil {
		return err
	}
	return db.Exec(`ALTER TABLE hosts ALTER COLUMN verify_peer_cert_by_name TYPE text USING ''`).Error
}

func seedHostsFromExternalProxy() error {
	var history []string
	if err := db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &history).Error; err != nil {
		return err
	}
	if slices.Contains(history, "HostsFromExternalProxy") {
		return nil
	}

	var inbounds []model.Inbound
	if err := db.Find(&inbounds).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, inbound := range inbounds {
			if _, err := CreateHostsFromExternalProxy(tx, inbound.Id, inbound.StreamSettings); err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "HostsFromExternalProxy"}).Error
	})
}

func seedWireguardPeersToClients() error {
	var history []string
	if err := db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &history).Error; err != nil {
		return err
	}
	if slices.Contains(history, "WireguardPeersToClients") {
		return nil
	}

	var inbounds []model.Inbound
	if err := db.Where("protocol = ?", string(model.WireGuard)).Find(&inbounds).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		usedEmails := map[string]struct{}{}
		var existingEmails []string
		if err := tx.Model(&model.ClientRecord{}).Pluck("email", &existingEmails).Error; err != nil {
			return err
		}
		for _, e := range existingEmails {
			usedEmails[e] = struct{}{}
		}

		for _, inbound := range inbounds {
			if strings.TrimSpace(inbound.Settings) == "" {
				continue
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
				log.Printf("WireguardPeersToClients: skip inbound %d (invalid settings json): %v", inbound.Id, err)
				continue
			}
			peers, ok := settings["peers"].([]any)
			if !ok || len(peers) == 0 {
				continue
			}

			var linkCount int64
			if err := tx.Model(&model.ClientInbound{}).Where("inbound_id = ?", inbound.Id).Count(&linkCount).Error; err != nil {
				return err
			}
			if linkCount > 0 {
				continue
			}

			clientObjs := make([]any, 0, len(peers))
			for i, raw := range peers {
				obj, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				email := wireguardPeerEmail(inbound.Remark, obj, i, usedEmails)
				usedEmails[email] = struct{}{}
				obj["email"] = email
				if sub, _ := obj["subId"].(string); strings.TrimSpace(sub) == "" {
					obj["subId"] = random.NumLower(16)
				}
				if _, ok := obj["enable"]; !ok {
					obj["enable"] = true
				}

				blob, err := json.Marshal(obj)
				if err != nil {
					continue
				}
				var c model.Client
				if err := json.Unmarshal(blob, &c); err != nil {
					log.Printf("WireguardPeersToClients: skip peer in inbound %d: %v", inbound.Id, err)
					continue
				}
				c.Email = email

				incoming := c.ToRecord()
				var row model.ClientRecord
				err = tx.Where("email = ?", email).First(&row).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					if err := tx.Create(incoming).Error; err != nil {
						return err
					}
					row = *incoming
				} else if err != nil {
					return err
				} else {
					model.MergeClientRecord(&row, incoming)
					if err := tx.Save(&row).Error; err != nil {
						return err
					}
				}

				link := model.ClientInbound{ClientId: row.Id, InboundId: inbound.Id}
				if err := tx.Where("client_id = ? AND inbound_id = ?", row.Id, inbound.Id).
					FirstOrCreate(&link).Error; err != nil {
					return err
				}

				clientObjs = append(clientObjs, obj)
			}

			delete(settings, "peers")
			settings["clients"] = clientObjs
			newSettings, err := json.Marshal(settings)
			if err != nil {
				return err
			}
			if err := tx.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
				Update("settings", string(newSettings)).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "WireguardPeersToClients"}).Error
	})
}

func wireguardPeerEmail(remark string, peer map[string]any, index int, used map[string]struct{}) string {
	base := strings.TrimSpace(remark)
	if base == "" {
		base = "wg"
	}
	suffix := strconv.Itoa(index + 1)
	if c, ok := peer["comment"].(string); ok && strings.TrimSpace(c) != "" {
		suffix = strings.TrimSpace(c)
	}
	email := strings.ReplaceAll(base+"-"+suffix, " ", "-")
	candidate := email
	for n := 2; ; n++ {
		if _, taken := used[candidate]; !taken {
			return candidate
		}
		candidate = email + "-" + strconv.Itoa(n)
	}
}

// seedMtprotoSecretsToClients converts each legacy single-secret mtproto inbound
// into a one-client inbound so MTProto joins the shared multi-client model: the
// inbound-level secret becomes the first client's FakeTLS secret, and a
// ClientRecord + client_inbounds link are created so per-client traffic, limits,
// and share links work exactly like every other protocol. One-time, self-gated
// on the "MtprotoSecretsToClients" seeder row. Mirrors seedWireguardPeersToClients.
func seedMtprotoSecretsToClients() error {
	var history []string
	if err := db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &history).Error; err != nil {
		return err
	}
	if slices.Contains(history, "MtprotoSecretsToClients") {
		return nil
	}

	var inbounds []model.Inbound
	if err := db.Where("protocol = ?", string(model.MTProto)).Find(&inbounds).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		usedEmails := map[string]struct{}{}
		var existingEmails []string
		if err := tx.Model(&model.ClientRecord{}).Pluck("email", &existingEmails).Error; err != nil {
			return err
		}
		for _, e := range existingEmails {
			usedEmails[e] = struct{}{}
		}

		for _, inbound := range inbounds {
			if strings.TrimSpace(inbound.Settings) == "" {
				continue
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
				log.Printf("MtprotoSecretsToClients: skip inbound %d (invalid settings json): %v", inbound.Id, err)
				continue
			}
			if clients, ok := settings["clients"].([]any); ok && len(clients) > 0 {
				continue
			}

			var linkCount int64
			if err := tx.Model(&model.ClientInbound{}).Where("inbound_id = ?", inbound.Id).Count(&linkCount).Error; err != nil {
				return err
			}
			if linkCount > 0 {
				continue
			}

			secret, _ := settings["secret"].(string)
			secret = strings.TrimSpace(secret)
			if secret == "" {
				domain, _ := settings["fakeTlsDomain"].(string)
				secret = model.GenerateFakeTLSSecret(strings.TrimSpace(domain))
			}

			email := mtprotoInboundClientEmail(inbound.Remark, usedEmails)
			usedEmails[email] = struct{}{}

			obj := map[string]any{
				"email":  email,
				"secret": secret,
				"enable": true,
				"subId":  random.NumLower(16),
			}
			c := model.Client{Email: email, Secret: secret, Enable: true, SubID: obj["subId"].(string)}

			incoming := c.ToRecord()
			var row model.ClientRecord
			err := tx.Where("email = ?", email).First(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(incoming).Error; err != nil {
					return err
				}
				row = *incoming
			} else if err != nil {
				return err
			} else {
				model.MergeClientRecord(&row, incoming)
				if err := tx.Save(&row).Error; err != nil {
					return err
				}
			}

			link := model.ClientInbound{ClientId: row.Id, InboundId: inbound.Id}
			if err := tx.Where("client_id = ? AND inbound_id = ?", row.Id, inbound.Id).
				FirstOrCreate(&link).Error; err != nil {
				return err
			}

			delete(settings, "secret")
			settings["clients"] = []any{obj}
			newSettings, err := json.Marshal(settings)
			if err != nil {
				return err
			}
			if err := tx.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
				Update("settings", string(newSettings)).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "MtprotoSecretsToClients"}).Error
	})
}

// stripMtprotoInboundSecrets removes the vestigial inbound-level `secret` from
// every mtproto inbound. seedMtprotoSecretsToClients already drops it while
// converting legacy single-secret inbounds, but inbounds that already had clients
// kept the dead field, and the old HealMtprotoSecret regenerated it on every
// save. mtg and every share link read only per-client secrets, so the
// inbound-level value is dead data that once leaked into stale, unusable links.
// One-time, self-gated on the "StripMtprotoInboundSecrets" seeder row.
func stripMtprotoInboundSecrets() error {
	var history []string
	if err := db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &history).Error; err != nil {
		return err
	}
	if slices.Contains(history, "StripMtprotoInboundSecrets") {
		return nil
	}

	var inbounds []model.Inbound
	if err := db.Where("protocol = ?", string(model.MTProto)).Find(&inbounds).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, inbound := range inbounds {
			stripped, ok := model.StripMtprotoInboundSecret(inbound.Settings)
			if !ok {
				continue
			}
			if err := tx.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
				Update("settings", stripped).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "StripMtprotoInboundSecrets"}).Error
	})
}

func migrateClientCredentialsTable() error {
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS client_credentials (
		client_id  integer NOT NULL,
		inbound_id integer NOT NULL,
		key        text    NOT NULL,
		value      text    NOT NULL,
		PRIMARY KEY (client_id, inbound_id, key)
	)`).Error; err != nil {
		return err
	}
	return db.Exec(
		`CREATE INDEX IF NOT EXISTS idx_client_credentials_inbound_id ON client_credentials (inbound_id)`,
	).Error
}

// legacyClientCredentialColumn returns a SELECT expression for a clients column
// this build no longer maps, or an empty literal on installs that never had it.
func legacyClientCredentialColumn(column string) string {
	if !db.Migrator().HasColumn(&model.ClientRecord{}, column) {
		return "''"
	}
	return "COALESCE(clients." + column + ", '')"
}

// settingsClientCredentials indexes an inbound's settings clients array as
// email → key → value. Unparseable settings yield nothing rather than aborting.
func settingsClientCredentials(settings string) map[string]map[string]string {
	out := map[string]map[string]string{}
	if strings.TrimSpace(settings) == "" {
		return out
	}
	var parsed struct {
		Clients []struct {
			Email  string `json:"email"`
			Secret string `json:"secret"`
			AdTag  string `json:"adTag"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		log.Printf("ClientCredentials: skip unparseable inbound settings: %v", err)
		return out
	}
	for _, c := range parsed.Clients {
		if c.Email == "" {
			continue
		}
		out[c.Email] = map[string]string{
			model.CredentialSecret: c.Secret,
			model.CredentialAdTag:  c.AdTag,
		}
	}
	return out
}

// backfillInboundClientCredentials writes one inbound's rows. The settings blob
// wins over the shared columns: a FakeTLS secret embeds this inbound's domain.
func backfillInboundClientCredentials(tx *gorm.DB, inboundId int, secretCol, adTagCol string) error {
	var settings string
	if err := tx.Model(&model.Inbound{}).Where("id = ?", inboundId).
		Pluck("settings", &settings).Error; err != nil {
		return err
	}
	fromSettings := settingsClientCredentials(settings)

	type linkedClient struct {
		ClientId int
		Email    string
		Secret   string
		AdTag    string
	}
	var linked []linkedClient
	if err := tx.Table("client_inbounds ci").
		Select("ci.client_id AS client_id, clients.email AS email, "+
			secretCol+" AS secret, "+adTagCol+" AS ad_tag").
		Joins("JOIN clients ON clients.id = ci.client_id").
		Where("ci.inbound_id = ?", inboundId).
		Scan(&linked).Error; err != nil {
		return err
	}

	creds := make([]model.ClientCredential, 0, len(linked))
	for _, l := range linked {
		for _, pair := range [][2]string{
			{model.CredentialSecret, l.Secret},
			{model.CredentialAdTag, l.AdTag},
		} {
			value := fromSettings[l.Email][pair[0]]
			if value == "" {
				value = pair[1]
			}
			if value == "" {
				continue
			}
			creds = append(creds, model.ClientCredential{
				ClientId: l.ClientId, InboundId: inboundId, Key: pair[0], Value: value,
			})
		}
	}
	if len(creds) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(creds, 500).Error
}

// backfillClientCredentials copies MTProto's shipped per-client secret and ad-tag
// onto client_credentials. One-time, self-gated on the "ClientCredentials" row.
func backfillClientCredentials() error {
	var history []string
	if err := db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &history).Error; err != nil {
		return err
	}
	if slices.Contains(history, "ClientCredentials") {
		return nil
	}

	// MTProto only: these are the credentials that moved, and the legacy columns
	// are shared, so any other protocol would copy a secret it cannot use.
	var inboundIds []int
	if err := db.Model(&model.Inbound{}).
		Where("protocol = ?", model.MTProto).
		Pluck("id", &inboundIds).Error; err != nil {
		return err
	}
	secretCol := legacyClientCredentialColumn("secret")
	adTagCol := legacyClientCredentialColumn("ad_tag")

	return db.Transaction(func(tx *gorm.DB) error {
		for _, inboundId := range inboundIds {
			if err := backfillInboundClientCredentials(tx, inboundId, secretCol, adTagCol); err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "ClientCredentials"}).Error
	})
}

// mtprotoInboundClientEmail derives a stable, unique client email for a migrated
// mtproto inbound from its remark.
func mtprotoInboundClientEmail(remark string, used map[string]struct{}) string {
	base := strings.TrimSpace(remark)
	if base == "" {
		base = "mtproto"
	}
	email := strings.ReplaceAll(base, " ", "-")
	candidate := email
	for n := 2; ; n++ {
		if _, taken := used[candidate]; !taken {
			return candidate
		}
		candidate = email + "-" + strconv.Itoa(n)
	}
}

// CreateHostsFromExternalProxy parses a legacy streamSettings.externalProxy array
// and inserts one Host row per entry on tx, returning the number of rows created.
// It is the shared core of both the one-time seedHostsFromExternalProxy startup
// migration and the inbound-import path: an inbound exported from a build that
// predated the hosts table carries its external proxies inline in
// streamSettings.externalProxy, and the startup migration is gated off after its
// first run, so a freshly imported inbound must be converted here instead. Blank
// or malformed streamSettings, or one without externalProxy entries, is a no-op.
func CreateHostsFromExternalProxy(tx *gorm.DB, inboundId int, streamSettings string) (int, error) {
	if strings.TrimSpace(streamSettings) == "" {
		return 0, nil
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(streamSettings), &stream); err != nil {
		return 0, nil
	}
	eps, ok := stream["externalProxy"].([]any)
	if !ok || len(eps) == 0 {
		return 0, nil
	}
	created := 0
	for i, raw := range eps {
		ep, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if err := tx.Create(externalProxyEntryToHost(inboundId, i, ep)).Error; err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func externalProxyEntryToHost(inboundId, index int, ep map[string]any) *model.Host {
	security, _ := ep["forceTls"].(string)
	switch security {
	case "same", "tls", "none":
	default:
		security = "same"
	}
	dest, _ := ep["dest"].(string)
	port := 0
	if p, ok := ep["port"].(float64); ok {
		port = int(p)
	}
	remark, _ := ep["remark"].(string)
	if strings.TrimSpace(remark) == "" {
		remark = "imported " + strconv.Itoa(index+1)
	}
	if len(remark) > 256 {
		remark = remark[:256]
	}
	sni, _ := ep["sni"].(string)
	fingerprint, _ := ep["fingerprint"].(string)
	ech, _ := ep["echConfigList"].(string)
	return &model.Host{
		GroupId:              random.NumLower(16),
		InboundId:            inboundId,
		SortOrder:            index,
		Remark:               remark,
		Address:              dest,
		Port:                 port,
		Security:             security,
		Sni:                  sni,
		Fingerprint:          fingerprint,
		Alpn:                 anyToNonEmptyStrings(ep["alpn"]),
		PinnedPeerCertSha256: anyToNonEmptyStrings(ep["pinnedPeerCertSha256"]),
		EchConfigList:        ech,
	}
}

func anyToNonEmptyStrings(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(t))
		for _, s := range t {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func pruneOrphanedHosts() error {
	res := db.Exec("DELETE FROM hosts WHERE inbound_id NOT IN (SELECT id FROM inbounds)")
	if res.Error != nil {
		log.Printf("Error pruning orphaned hosts rows: %v", res.Error)
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Printf("Pruned %d orphaned hosts row(s)", res.RowsAffected)
	}
	return nil
}

func pruneOrphanedClientInbounds() error {
	res := db.Exec("DELETE FROM client_inbounds WHERE inbound_id NOT IN (SELECT id FROM inbounds)")
	if res.Error != nil {
		log.Printf("Error pruning orphaned client_inbounds rows: %v", res.Error)
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Printf("Pruned %d orphaned client_inbounds row(s)", res.RowsAffected)
	}
	return nil
}

// migrateLegacySocksInboundsToMixed renames legacy socks inbounds to mixed.
// The protocol enum dropped socks in favor of mixed (identical settings shape,
// same behavior plus HTTP on the shared port), so rows predating the rename
// fail model validation — most visibly when pushed to a node, where one legacy
// inbound stalled the entire node's config and traffic sync (#5685).
func migrateLegacySocksInboundsToMixed() error {
	res := db.Exec("UPDATE inbounds SET protocol = 'mixed' WHERE protocol = 'socks'")
	if res.Error != nil {
		log.Printf("Error migrating legacy socks inbounds to mixed: %v", res.Error)
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Printf("Migrated %d legacy socks inbound(s) to mixed", res.RowsAffected)
	}
	return nil
}

// migrateShadowsocksRemovedCiphers rewrites shadowsocks inbounds still using
// the "none"/"plain" ciphers that xray-core v26.7.11 removed; one such row
// makes the whole generated config unbuildable and keeps xray from starting.
func migrateShadowsocksRemovedCiphers() error {
	var inbounds []model.Inbound
	if err := db.Where("protocol = ?", model.Shadowsocks).Find(&inbounds).Error; err != nil {
		return err
	}
	migrated := int64(0)
	for _, inbound := range inbounds {
		if strings.TrimSpace(inbound.Settings) == "" {
			continue
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			continue
		}
		changed := false
		if method, _ := settings["method"].(string); method != "" {
			if replacement, removed := model.ReplaceRemovedShadowsocksCipher(method); removed {
				settings["method"] = replacement
				changed = true
			}
		}
		if clients, ok := settings["clients"].([]any); ok {
			for i := range clients {
				cm, ok := clients[i].(map[string]any)
				if !ok {
					continue
				}
				method, _ := cm["method"].(string)
				if replacement, removed := model.ReplaceRemovedShadowsocksCipher(method); removed {
					cm["method"] = replacement
					clients[i] = cm
					changed = true
				}
			}
		}
		if !changed {
			continue
		}
		newSettings, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			log.Printf("migrateShadowsocksRemovedCiphers: skip inbound %d (marshal failed): %v", inbound.Id, err)
			continue
		}
		if err := db.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
			Update("settings", string(newSettings)).Error; err != nil {
			return err
		}
		migrated++
	}
	if migrated > 0 {
		log.Printf("Rewrote removed shadowsocks cipher on %d inbound(s)", migrated)
	}
	return nil
}

// migrateVmessRemovedSecurities rewrites the vmess "none"/"zero" security
// values that xray-core v26.7.11 removed to "auto" (what the core now treats
// them as), on both the clients column and each vmess inbound's settings.
func migrateVmessRemovedSecurities() error {
	res := db.Exec("UPDATE clients SET security = 'auto' WHERE security IN ('none', 'zero')")
	if res.Error != nil {
		log.Printf("Error migrating removed vmess security values on clients: %v", res.Error)
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Printf("Migrated %d client(s) off removed vmess security values", res.RowsAffected)
	}
	var inbounds []model.Inbound
	if err := db.Where("protocol = ?", model.VMESS).Find(&inbounds).Error; err != nil {
		return err
	}
	migrated := int64(0)
	for _, inbound := range inbounds {
		if strings.TrimSpace(inbound.Settings) == "" {
			continue
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			continue
		}
		clients, ok := settings["clients"].([]any)
		if !ok {
			continue
		}
		changed := false
		for i := range clients {
			cm, ok := clients[i].(map[string]any)
			if !ok {
				continue
			}
			if security, _ := cm["security"].(string); security == "none" || security == "zero" {
				cm["security"] = "auto"
				clients[i] = cm
				changed = true
			}
		}
		if !changed {
			continue
		}
		newSettings, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			log.Printf("migrateVmessRemovedSecurities: skip inbound %d (marshal failed): %v", inbound.Id, err)
			continue
		}
		if err := db.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
			Update("settings", string(newSettings)).Error; err != nil {
			return err
		}
		migrated++
	}
	if migrated > 0 {
		log.Printf("Rewrote removed vmess security values on %d inbound(s)", migrated)
	}
	return nil
}

// migrateTgIDIndex creates an index on the clients.tg_id column so that
// lookups by Telegram ID do not require a full table scan. The index tag
// on the struct field already causes AutoMigrate to create it on new
// installations; the explicit migration ensures existing databases get it.
func migrateTgIDIndex() error {
	if db.Migrator().HasIndex(&model.ClientRecord{}, "idx_clients_tg_id") {
		return nil
	}
	return db.Migrator().CreateIndex(&model.ClientRecord{}, "TgID")
}

/*
migrateDepletedEmailIndex indexes only the rows a quota or expiry disabled.

"Is this client depleted" is a panel-wide question — client_traffics holds one
row per email across every core — so the lookup can no longer be narrowed by
inbound_id, and every inbound render asks it. Unindexed that is a sequential
scan per inbound; partial, the index covers the query and holds only the small
disabled minority. GORM tags cannot express a partial index, hence raw SQL.
*/
func migrateDepletedEmailIndex() error {
	return db.Exec(`CREATE INDEX IF NOT EXISTS idx_client_traffics_depleted ` +
		`ON client_traffics (email) WHERE enable = false`).Error
}

// normalizeInboundSubSortIndex lifts sub_sort_index values below the 1-based
// minimum (rows written by builds that defaulted the column to 0, or by nodes
// predating the field) so they cannot sort ahead of explicitly ranked inbounds.
func normalizeInboundSubSortIndex() error {
	res := db.Exec("UPDATE inbounds SET sub_sort_index = 1 WHERE sub_sort_index < 1")
	if res.Error != nil {
		log.Printf("Error normalizing inbound sub_sort_index: %v", res.Error)
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Printf("Normalized sub_sort_index on %d inbound(s)", res.RowsAffected)
	}
	return nil
}

// repairOverflowedTrafficCounters heals traffic counters that historic
// compounding bugs pushed out of range (#5762): every value is clamped into
// [0, TrafficMax] so the next delta cannot overflow int64 again.
func repairOverflowedTrafficCounters() error {
	targets := []struct {
		table   string
		columns []string
	}{
		{"client_traffics", []string{"up", "down"}},
		{"inbounds", []string{"up", "down"}},
		{"outbound_traffics", []string{"up", "down", "total"}},
		{"node_client_traffics", []string{"up", "down"}},
	}
	for _, target := range targets {
		for _, col := range target.columns {
			statements := []string{
				fmt.Sprintf("UPDATE %s SET %s = %d WHERE %s > %d", target.table, col, TrafficMax, col, TrafficMax),
				fmt.Sprintf("UPDATE %s SET %s = 0 WHERE %s < 0", target.table, col, col),
			}
			var repaired int64
			for _, statement := range statements {
				res := db.Exec(statement)
				if res.Error != nil {
					log.Printf("Error repairing %s.%s: %v", target.table, col, res.Error)
					return res.Error
				}
				repaired += res.RowsAffected
			}
			if repaired > 0 {
				log.Printf("Repaired %d overflowed %s.%s value(s)", repaired, target.table, col)
			}
		}
	}
	return nil
}

// dedupeInboundSettingsClients collapses duplicate same-email entries inside
// every inbound's settings.clients array, keeping the first occurrence.
// Retried or raced multi-node client adds on older builds appended the same
// client several times (#5770), which the client lists then rendered as
// phantom duplicates. Runs on every start (idempotent, writes only changed
// rows) because a restored backup or a not-yet-upgraded node's snapshot can
// reintroduce duplicates.
func dedupeInboundSettingsClients() error {
	var inbounds []model.Inbound
	if err := db.Find(&inbounds).Error; err != nil {
		return err
	}
	repaired := int64(0)
	for _, inbound := range inbounds {
		if strings.TrimSpace(inbound.Settings) == "" {
			continue
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			continue
		}
		clients, _ := settings["clients"].([]any)
		if len(clients) < 2 {
			continue
		}
		seen := make(map[string]struct{}, len(clients))
		kept := make([]any, 0, len(clients))
		for _, c := range clients {
			if cm, ok := c.(map[string]any); ok {
				if email, _ := cm["email"].(string); email != "" {
					key := strings.ToLower(email)
					if _, dup := seen[key]; dup {
						continue
					}
					seen[key] = struct{}{}
				}
			}
			kept = append(kept, c)
		}
		if len(kept) == len(clients) {
			continue
		}
		settings["clients"] = kept
		newSettings, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			log.Printf("dedupeInboundSettingsClients: skip inbound %d (marshal failed): %v", inbound.Id, err)
			continue
		}
		if err := db.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
			Update("settings", string(newSettings)).Error; err != nil {
			return err
		}
		repaired++
	}
	if repaired > 0 {
		log.Printf("Removed duplicate client entries from %d inbound(s)", repaired)
	}
	return nil
}

func isIgnorableDuplicateColumnErr(gdb *gorm.DB, err error, mdl any) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "already exists") && strings.Contains(errMsg, "column ") {
		if _, after, ok := strings.Cut(errMsg, "column \""); ok {
			rest := after
			if e := strings.Index(rest, "\""); e > 0 {
				col := rest[:e]
				return col != "" && gdb != nil && gdb.Migrator().HasColumn(mdl, col)
			}
		}
	}
	return false
}

func initUser() error {
	empty, err := isTableEmpty("users")
	if err != nil {
		log.Printf("Error checking if users table is empty: %v", err)
		return err
	}
	if empty {
		hashedPassword, err := crypto.HashPasswordAsBcrypt(defaultPassword)
		if err != nil {
			log.Printf("Error hashing default password: %v", err)
			return err
		}

		user := &model.User{
			Username: defaultUsername,
			Password: hashedPassword,
		}
		return db.Create(user).Error
	}
	return nil
}

func runSeeders(isUsersEmpty bool) error {
	empty, err := isTableEmpty("history_of_seeders")
	if err != nil {
		log.Printf("Error checking if users table is empty: %v", err)
		return err
	}

	if empty && isUsersEmpty {
		seeders := []string{"UserPasswordHash", "ClientsTable", "InboundClientsArrayFix", "InboundClientTgIdFix2", "InboundClientSubIdFix", "FreedomFinalRulesReverseFix", "FreedomFinalRulesPrivateEgressBlock", "InboundRealityFinalmaskTcpStrip", "ApiTokensHash", "LegacyProxySettingsCleanup", "WireguardPeersToClients", "MtprotoSecretsToClients", "NodeInboundsAdopted", "ResetIpLimitNoFail2ban"}
		for _, name := range seeders {
			if err := db.Create(&model.HistoryOfSeeders{SeederName: name}).Error; err != nil {
				return err
			}
		}
		return seedApiTokens()
	}

	var seedersHistory []string
	if err := db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &seedersHistory).Error; err != nil {
		log.Printf("Error fetching seeder history: %v", err)
		return err
	}

	if !slices.Contains(seedersHistory, "UserPasswordHash") && !isUsersEmpty {
		var users []model.User
		if err := db.Find(&users).Error; err != nil {
			log.Printf("Error fetching users for password migration: %v", err)
			return err
		}

		for _, user := range users {
			if crypto.IsHashed(user.Password) {
				continue
			}
			hashedPassword, err := crypto.HashPasswordAsBcrypt(user.Password)
			if err != nil {
				log.Printf("Error hashing password for user '%s': %v", user.Username, err)
				return err
			}
			if err := db.Model(&user).Update("password", hashedPassword).Error; err != nil {
				log.Printf("Error updating password for user '%s': %v", user.Username, err)
				return err
			}
		}

		hashSeeder := &model.HistoryOfSeeders{
			SeederName: "UserPasswordHash",
		}
		if err := db.Create(hashSeeder).Error; err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "ApiTokensTable") {
		if err := seedApiTokens(); err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "ApiTokensHash") {
		if err := hashExistingApiTokens(); err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "ClientsTable") {
		if err := seedClientsFromInboundJSON(); err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "InboundClientsArrayFix") {
		if err := normalizeInboundClientsArray(); err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "InboundClientTgIdFix2") {
		if err := normalizeInboundClientTgId(); err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "InboundClientSubIdFix") {
		if err := normalizeInboundClientSubId(); err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "FreedomFinalRulesReverseFix") {
		if err := normalizeFreedomFinalRules(); err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "FreedomFinalRulesPrivateEgressBlock") {
		if err := hardenFreedomFinalRules(); err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "InboundRealityFinalmaskTcpStrip") {
		if err := stripRealityFinalmaskTcp(); err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "LegacyProxySettingsCleanup") {
		if err := clearLegacyProxySettings(); err != nil {
			return err
		}
	}

	if !slices.Contains(seedersHistory, "NodeInboundsAdopted") {
		if err := seedNodeInboundsAdopted(); err != nil {
			return err
		}
	}

	if err := seedHostsFromExternalProxy(); err != nil {
		return err
	}

	if err := resetIpLimitsWithoutFail2ban(); err != nil {
		return err
	}

	if err := seedWireguardPeersToClients(); err != nil {
		return err
	}

	if err := backfillEmptyHostGroupIds(); err != nil {
		return err
	}

	// Self-gated on the "MtprotoSecretsToClients" row.
	if err := seedMtprotoSecretsToClients(); err != nil {
		return err
	}

	// Self-gated on the "StripMtprotoInboundSecrets" row. Must run after the
	// seeder above so legacy single-secret inbounds are first converted to a
	// client (which preserves the secret) before the inbound-level copy is
	// dropped from every mtproto inbound.
	if err := stripMtprotoInboundSecrets(); err != nil {
		return err
	}

	// Self-gated on the "ClientCredentials" row. Must run after the seeder above
	// so a converted legacy inbound already has the client link it reads.
	if err := backfillClientCredentials(); err != nil {
		return err
	}

	// Idempotent, not seeder-gated: bad values can re-enter via a restored
	// backup, so re-check on every start.
	return normalizeSettingPaths()
}

// seedNodeInboundsAdopted keeps the pre-existing reconcile behavior for nodes
// that were already syncing before the inbounds_adopted_at gate was introduced.
func seedNodeInboundsAdopted() error {
	if err := db.Model(&model.Node{}).
		Where("inbounds_adopted_at = 0").
		Update("inbounds_adopted_at", time.Now().Unix()).Error; err != nil {
		return err
	}
	return db.Create(&model.HistoryOfSeeders{SeederName: "NodeInboundsAdopted"}).Error
}

// backfillEmptyHostGroupIds is idempotent and not seeder-gated: builds that
// predate group ids on the inbound-import path (and restored backups) can
// re-introduce hosts rows with an empty group_id, and such rows render as a
// synthetic fallback_<id> group the update/delete API cannot address, so
// re-check on every start.
func backfillEmptyHostGroupIds() error {
	var hosts []*model.Host
	if err := db.Where("group_id = '' OR group_id IS NULL").Find(&hosts).Error; err != nil {
		return err
	}
	if len(hosts) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, h := range hosts {
			if err := tx.Model(h).Update("group_id", random.NumLower(16)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func resetIpLimitsWithoutFail2ban() error {
	var history []string
	if err := db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &history).Error; err != nil {
		return err
	}
	if slices.Contains(history, "ResetIpLimitNoFail2ban") {
		return nil
	}

	if fail2banCanEnforce() {
		return db.Create(&model.HistoryOfSeeders{SeederName: "ResetIpLimitNoFail2ban"}).Error
	}

	var inbounds []model.Inbound
	if err := db.Find(&inbounds).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, inbound := range inbounds {
			if strings.TrimSpace(inbound.Settings) == "" {
				continue
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
				log.Printf("ResetIpLimitNoFail2ban: skip inbound %d (invalid settings json): %v", inbound.Id, err)
				continue
			}
			clients, ok := settings["clients"].([]any)
			if !ok {
				continue
			}
			mutated := false
			for i, raw := range clients {
				obj, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				v, present := obj["limitIp"]
				if !present {
					continue
				}
				if n, isNum := v.(float64); isNum && n == 0 {
					continue
				}
				obj["limitIp"] = 0
				clients[i] = obj
				mutated = true
			}
			if !mutated {
				continue
			}
			settings["clients"] = clients
			newSettings, err := json.MarshalIndent(settings, "", "  ")
			if err != nil {
				log.Printf("ResetIpLimitNoFail2ban: skip inbound %d (marshal failed): %v", inbound.Id, err)
				continue
			}
			if err := tx.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
				Update("settings", string(newSettings)).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.ClientRecord{}).Where("limit_ip <> ?", 0).
			Update("limit_ip", 0).Error; err != nil {
			return err
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "ResetIpLimitNoFail2ban"}).Error
	})
}

func fail2banCanEnforce() bool {
	if v, ok := os.LookupEnv("PUI_ENABLE_FAIL2BAN"); ok && v != "true" {
		return false
	}
	return exec.CommandContext(context.Background(), "fail2ban-client", "-h").Run() == nil
}

func clearLegacyProxySettings() error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("key IN ?", []string{"panelProxy", "tgBotProxy"}).
			Delete(&model.Setting{}).Error; err != nil {
			return err
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "LegacyProxySettingsCleanup"}).Error
	})
}

func normalizeSettingPaths() error {
	pathKeys := []string{"webBasePath", "subPath", "subJsonPath", "subClashPath"}
	var rows []model.Setting
	if err := db.Where("key IN ?", pathKeys).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		fixed := row.Value
		if !strings.HasPrefix(fixed, "/") {
			fixed = "/" + fixed
		}
		if !strings.HasSuffix(fixed, "/") {
			fixed += "/"
		}
		if fixed == row.Value {
			continue
		}
		if err := db.Model(&model.Setting{}).Where("id = ?", row.Id).
			Update("value", fixed).Error; err != nil {
			return err
		}
	}
	return nil
}

func normalizeInboundClientTgId() error {
	var inbounds []model.Inbound
	if err := db.Find(&inbounds).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, inbound := range inbounds {
			if strings.TrimSpace(inbound.Settings) == "" {
				continue
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
				log.Printf("InboundClientTgIdFix: skip inbound %d (invalid settings json): %v", inbound.Id, err)
				continue
			}
			clients, ok := settings["clients"].([]any)
			if !ok {
				continue
			}
			mutated := false
			for i, raw := range clients {
				obj, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				tgRaw, present := obj["tgId"]
				if !present {
					continue
				}
				v, isFloat := tgRaw.(float64)
				if isFloat && !math.IsNaN(v) && !math.IsInf(v, 0) && v == math.Trunc(v) {
					continue
				}
				obj["tgId"] = int64(0)
				if s, isStr := tgRaw.(string); isStr {
					if id, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(s), " ", ""), 10, 64); err == nil {
						obj["tgId"] = id
					}
				}
				clients[i] = obj
				mutated = true
			}
			if !mutated {
				continue
			}
			settings["clients"] = clients
			newSettings, err := json.MarshalIndent(settings, "", "  ")
			if err != nil {
				log.Printf("InboundClientTgIdFix: skip inbound %d (marshal failed): %v", inbound.Id, err)
				continue
			}
			if err := tx.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
				Update("settings", string(newSettings)).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "InboundClientTgIdFix2"}).Error
	})
}

func normalizeInboundClientSubId() error {
	var inbounds []model.Inbound
	if err := db.Find(&inbounds).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, inbound := range inbounds {
			if strings.TrimSpace(inbound.Settings) == "" {
				continue
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
				log.Printf("InboundClientSubIdFix: skip inbound %d (invalid settings json): %v", inbound.Id, err)
				continue
			}
			clients, ok := settings["clients"].([]any)
			if !ok {
				continue
			}
			mutated := false
			for i, raw := range clients {
				obj, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				existing, _ := obj["subId"].(string)
				if strings.TrimSpace(existing) != "" {
					continue
				}
				obj["subId"] = random.NumLower(16)
				clients[i] = obj
				mutated = true
			}
			if !mutated {
				continue
			}
			settings["clients"] = clients
			newSettings, err := json.MarshalIndent(settings, "", "  ")
			if err != nil {
				log.Printf("InboundClientSubIdFix: skip inbound %d (marshal failed): %v", inbound.Id, err)
				continue
			}
			if err := tx.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
				Update("settings", string(newSettings)).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "InboundClientSubIdFix"}).Error
	})
}

func normalizeInboundClientsArray() error {
	var inbounds []model.Inbound
	if err := db.Find(&inbounds).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, inbound := range inbounds {
			if strings.TrimSpace(inbound.Settings) == "" {
				continue
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
				log.Printf("InboundClientsArrayFix: skip inbound %d (invalid settings json): %v", inbound.Id, err)
				continue
			}
			raw, exists := settings["clients"]
			if !exists || raw != nil {
				continue
			}
			settings["clients"] = []any{}
			newSettings, err := json.MarshalIndent(settings, "", "  ")
			if err != nil {
				log.Printf("InboundClientsArrayFix: skip inbound %d (marshal failed): %v", inbound.Id, err)
				continue
			}
			if err := tx.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
				Update("settings", string(newSettings)).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "InboundClientsArrayFix"}).Error
	})
}

func normalizeFreedomFinalRules() error {
	var setting model.Setting
	err := db.Model(model.Setting{}).Where("key = ?", "xrayTemplateConfig").First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&model.HistoryOfSeeders{SeederName: "FreedomFinalRulesReverseFix"}).Error
	}
	if err != nil {
		return err
	}

	updated, changed, rErr := rewriteFreedomFinalRules(setting.Value)
	if rErr != nil {
		log.Printf("FreedomFinalRulesReverseFix: skip (invalid xrayTemplateConfig json): %v", rErr)
		return db.Create(&model.HistoryOfSeeders{SeederName: "FreedomFinalRulesReverseFix"}).Error
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if changed {
			if err := tx.Model(&model.Setting{}).Where("key = ?", "xrayTemplateConfig").
				Update("value", updated).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "FreedomFinalRulesReverseFix"}).Error
	})
}

func rewriteFreedomFinalRules(raw string) (string, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return raw, false, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return raw, false, err
	}
	outbounds, ok := cfg["outbounds"].([]any)
	if !ok {
		return raw, false, nil
	}
	changed := false
	for _, ob := range outbounds {
		obj, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if proto, _ := obj["protocol"].(string); proto != "freedom" {
			continue
		}
		settings, ok := obj["settings"].(map[string]any)
		if !ok {
			continue
		}
		if !isLegacyPrivateOnlyFinalRules(settings["finalRules"]) {
			continue
		}
		settings["finalRules"] = []any{map[string]any{"action": "allow"}}
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return raw, false, err
	}
	return string(out), true, nil
}

func isLegacyPrivateOnlyFinalRules(v any) bool {
	rules, ok := v.([]any)
	if !ok || len(rules) != 1 {
		return false
	}
	rule, ok := rules[0].(map[string]any)
	if !ok {
		return false
	}
	if action, _ := rule["action"].(string); action != "allow" {
		return false
	}
	ips, ok := rule["ip"].([]any)
	if !ok || len(ips) != 1 {
		return false
	}
	if s, _ := ips[0].(string); s != "geoip:private" {
		return false
	}
	for k := range rule {
		if k != "action" && k != "ip" {
			return false
		}
	}
	return true
}

func hardenFreedomFinalRules() error {
	var setting model.Setting
	err := db.Model(model.Setting{}).Where("key = ?", "xrayTemplateConfig").First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&model.HistoryOfSeeders{SeederName: "FreedomFinalRulesPrivateEgressBlock"}).Error
	}
	if err != nil {
		return err
	}

	updated, changed, rErr := rewriteFreedomFinalRulesPrivateEgress(setting.Value)
	if rErr != nil {
		log.Printf("FreedomFinalRulesPrivateEgressBlock: skip (invalid xrayTemplateConfig json): %v", rErr)
		return db.Create(&model.HistoryOfSeeders{SeederName: "FreedomFinalRulesPrivateEgressBlock"}).Error
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if changed {
			if err := tx.Model(&model.Setting{}).Where("key = ?", "xrayTemplateConfig").
				Update("value", updated).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "FreedomFinalRulesPrivateEgressBlock"}).Error
	})
}

func rewriteFreedomFinalRulesPrivateEgress(raw string) (string, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return raw, false, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return raw, false, err
	}
	outbounds, ok := cfg["outbounds"].([]any)
	if !ok {
		return raw, false, nil
	}
	changed := false
	for _, ob := range outbounds {
		obj, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if proto, _ := obj["protocol"].(string); proto != "freedom" {
			continue
		}
		settings, ok := obj["settings"].(map[string]any)
		if !ok {
			continue
		}
		if !isAllowOnlyFinalRules(settings["finalRules"]) && !isLegacyPrivateOnlyFinalRules(settings["finalRules"]) {
			continue
		}
		settings["finalRules"] = []any{
			map[string]any{"action": "block", "ip": []any{"geoip:private"}},
			map[string]any{"action": "allow"},
		}
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return raw, false, err
	}
	return string(out), true, nil
}

func stripRealityFinalmaskTcp() error {
	var inbounds []model.Inbound
	if err := db.Find(&inbounds).Error; err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for i := range inbounds {
			updated, changed := stripRealityFinalmaskTcpFromStream(inbounds[i].StreamSettings)
			if !changed {
				continue
			}
			if err := tx.Model(&model.Inbound{}).Where("id = ?", inbounds[i].Id).
				Update("stream_settings", updated).Error; err != nil {
				return err
			}
			log.Printf("InboundRealityFinalmaskTcpStrip: removed finalmask.tcp from REALITY inbound %d (%s)", inbounds[i].Id, inbounds[i].Tag)
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: "InboundRealityFinalmaskTcpStrip"}).Error
	})
}

func stripRealityFinalmaskTcpFromStream(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return raw, false
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(raw), &stream); err != nil {
		return raw, false
	}
	if sec, _ := stream["security"].(string); sec != "reality" {
		return raw, false
	}
	finalmask, ok := stream["finalmask"].(map[string]any)
	if !ok {
		return raw, false
	}
	if tcp, _ := finalmask["tcp"].([]any); len(tcp) == 0 {
		return raw, false
	}
	delete(finalmask, "tcp")
	if len(finalmask) == 0 {
		delete(stream, "finalmask")
	}
	out, err := json.Marshal(stream)
	if err != nil {
		return raw, false
	}
	return string(out), true
}

func isAllowOnlyFinalRules(v any) bool {
	rules, ok := v.([]any)
	if !ok || len(rules) != 1 {
		return false
	}
	rule, ok := rules[0].(map[string]any)
	if !ok {
		return false
	}
	if action, _ := rule["action"].(string); action != "allow" {
		return false
	}
	for k := range rule {
		if k != "action" {
			return false
		}
	}
	return true
}

func normalizeClientJSONFields(obj map[string]any) {
	normalizeInt := func(key string) {
		raw, exists := obj[key]
		if !exists {
			return
		}
		s, ok := raw.(string)
		if !ok {
			return
		}
		trimmed := strings.ReplaceAll(strings.TrimSpace(s), " ", "")
		if trimmed == "" {
			delete(obj, key)
			return
		}
		if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			obj[key] = n
		} else {
			delete(obj, key)
		}
	}
	for _, k := range []string{"tgId", "limitIp", "totalGB", "expiryTime", "reset", "created_at", "updated_at"} {
		normalizeInt(k)
	}
}

func seedClientsFromInboundJSON() error {
	var inbounds []model.Inbound
	if err := db.Find(&inbounds).Error; err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		byEmail := map[string]*model.ClientRecord{}

		var existing []model.ClientRecord
		if err := tx.Find(&existing).Error; err != nil {
			return err
		}
		for i := range existing {
			byEmail[existing[i].Email] = &existing[i]
		}

		for _, inbound := range inbounds {
			if strings.TrimSpace(inbound.Settings) == "" {
				continue
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
				log.Printf("ClientsTable seed: skip inbound %d (invalid settings json): %v", inbound.Id, err)
				continue
			}
			rawList, ok := settings["clients"].([]any)
			if !ok {
				continue
			}

			for _, raw := range rawList {
				obj, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				normalizeClientJSONFields(obj)
				blob, err := json.Marshal(obj)
				if err != nil {
					continue
				}
				var c model.Client
				if err := json.Unmarshal(blob, &c); err != nil {
					log.Printf("ClientsTable seed: skip client in inbound %d (unmarshal failed): %v; payload=%s",
						inbound.Id, err, string(blob))
					continue
				}
				email := strings.TrimSpace(c.Email)
				if email == "" {
					continue
				}
				incoming := c.ToRecord()

				row, dup := byEmail[email]
				if !dup {
					if err := tx.Create(incoming).Error; err != nil {
						return err
					}
					byEmail[email] = incoming
					row = incoming
				} else {
					conflicts := model.MergeClientRecord(row, incoming)
					for _, x := range conflicts {
						log.Printf("client merge: email=%s conflict on %s old=%v new=%v kept=%v",
							email, x.Field, x.Old, x.New, x.Kept)
					}
					if err := tx.Save(row).Error; err != nil {
						return err
					}
				}

				link := model.ClientInbound{
					ClientId:     row.Id,
					InboundId:    inbound.Id,
					FlowOverride: c.Flow,
				}
				if err := tx.Where("client_id = ? AND inbound_id = ?", row.Id, inbound.Id).
					FirstOrCreate(&link).Error; err != nil {
					return err
				}
			}
		}

		return tx.Create(&model.HistoryOfSeeders{SeederName: "ClientsTable"}).Error
	})
}

func seedApiTokens() error {
	empty, err := isTableEmpty("api_tokens")
	if err != nil {
		return err
	}
	if empty {
		var legacy model.Setting
		err := db.Model(model.Setting{}).Where("key = ?", "apiToken").First(&legacy).Error
		if err == nil && legacy.Value != "" {
			row := &model.ApiToken{
				Name:    "default",
				Token:   legacy.Value,
				Enabled: true,
			}
			if err := db.Create(row).Error; err != nil {
				log.Printf("Error migrating legacy apiToken: %v", err)
				return err
			}
		}
	}
	return db.Create(&model.HistoryOfSeeders{SeederName: "ApiTokensTable"}).Error
}

func hashExistingApiTokens() error {
	var rows []*model.ApiToken
	if err := db.Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		if crypto.IsSHA256Hex(r.Token) {
			continue
		}
		hashed := crypto.HashTokenSHA256(r.Token)
		if err := db.Model(model.ApiToken{}).Where("id = ?", r.Id).Update("token", hashed).Error; err != nil {
			log.Printf("Error hashing api token %d: %v", r.Id, err)
			return err
		}
	}
	return db.Create(&model.HistoryOfSeeders{SeederName: "ApiTokensHash"}).Error
}

func isTableEmpty(tableName string) (bool, error) {
	var count int64
	err := db.Table(tableName).Count(&count).Error
	return count == 0, err
}

// exampleDSN is shown verbatim in the startup errors below, so it must stay a
// copy-pasteable PostgreSQL connection string.
const exampleDSN = "postgres://p-ui:PASSWORD@127.0.0.1:5432/p-ui?sslmode=disable"

// ParseDSN validates a PostgreSQL connection string against the single shape
// Penhoon UI supports -- a postgres:// (or postgresql://) URL that names a
// database -- and returns it parsed. Everything that has to take the DSN apart
// goes through here: startup, and the pg_dump/pg_restore backup paths that need
// the host, port, user and database as separate PG* variables. One parser means
// a DSN the panel boots on is always a DSN Back Up and Import DB can use.
//
// libpq's "host=... dbname=..." keyword form is deliberately rejected: net/url
// cannot take it apart, so accepting it at startup would leave the backup paths
// broken. Every writer of a DSN in this repo emits the URL form.
func ParseDSN(dsn string) (*url.URL, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, errors.New("the PostgreSQL DSN is empty")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("not a valid URL: %w", err)
	}
	switch u.Scheme {
	case "postgres", "postgresql":
	case "":
		return nil, errors.New(`missing the "postgres://" scheme -- Penhoon UI takes only URL DSNs, not file paths or libpq key=value strings`)
	default:
		return nil, fmt.Errorf("unsupported scheme %q (expected postgres:// or postgresql://)", u.Scheme)
	}
	if strings.TrimPrefix(u.Path, "/") == "" {
		return nil, errors.New("no database name in the URL path")
	}
	return u, nil
}

// requireDSN resolves PUI_DB_DSN and fails fast when it is missing or is not a
// PostgreSQL URL. Catching the obviously wrong value here keeps a typo (or a
// leftover file path) from burning the whole connect-retry budget before the
// panel reports the real problem, and guarantees the running panel never holds
// a DSN its own backup tooling would choke on.
func requireDSN() (string, error) {
	dsn := config.GetDBDSN()
	if dsn == "" {
		return "", fmt.Errorf(
			"PUI_DB_DSN is not set: Penhoon UI stores all of its data in PostgreSQL and has no other backend.\n"+
				"Set it in /etc/default/p-ui and restart p-ui, for example:\n  PUI_DB_DSN=%s",
			exampleDSN,
		)
	}
	if _, err := ParseDSN(dsn); err != nil {
		return "", fmt.Errorf(
			"PUI_DB_DSN=%q is not a usable PostgreSQL URL: %v.\n"+
				"Fix it in /etc/default/p-ui and restart p-ui, for example:\n  PUI_DB_DSN=%s",
			dsn, err, exampleDSN,
		)
	}
	return dsn, nil
}

// InitDB opens the PostgreSQL database named by PUI_DB_DSN, brings the schema
// up to date and runs the seeders.
func InitDB() error {
	dsn, err := requireDSN()
	if err != nil {
		return err
	}

	var gormLogger logger.Interface
	if config.IsDebug() {
		gormLogger = logger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             time.Second,
				LogLevel:                  logger.Info,
				IgnoreRecordNotFoundError: true,
				Colorful:                  true,
			},
		)
	} else {
		gormLogger = logger.Discard
	}

	db, err = openPostgresWithRetry(dsn, &gorm.Config{Logger: gormLogger, DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxOpenConns(envInt("PUI_DB_MAX_OPEN_CONNS", 25))
	sqlDB.SetMaxIdleConns(envInt("PUI_DB_MAX_IDLE_CONNS", 25))
	sqlDB.SetConnMaxLifetime(time.Hour)
	sqlDB.SetConnMaxIdleTime(30 * time.Minute)

	if err := initModels(); err != nil {
		return err
	}

	isUsersEmpty, err := isTableEmpty("users")
	if err != nil {
		return err
	}

	if err := initUser(); err != nil {
		return err
	}
	return runSeeders(isUsersEmpty)
}

func normalizeApiTokenCreatedAtSeconds() error {
	return db.Model(&model.ApiToken{}).
		Where("created_at >= ?", model.ApiTokenUnixMillisecondsThreshold).
		UpdateColumn("created_at", gorm.Expr("created_at / ?", 1000)).Error
}

// openPostgresWithRetry retries the initial PostgreSQL connection with
// backoff so a database that starts slower than the panel (or drops out
// briefly) does not immediately kill the process and trip systemd's
// restart loop. Every failed attempt logs the real driver error, which
// used to be buried behind a generic startup failure.
func openPostgresWithRetry(dsn string, c *gorm.Config) (*gorm.DB, error) {
	delays := []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second}
	var lastErr error
	for i, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		conn, err := gorm.Open(postgres.Open(dsn), c)
		if err == nil {
			if i > 0 {
				log.Printf("postgres connection established on attempt %d/%d", i+1, len(delays))
			}
			return conn, nil
		}
		lastErr = err
		log.Printf("postgres connection attempt %d/%d failed: %v", i+1, len(delays), err)
	}
	return nil, fmt.Errorf("postgres unreachable after %d attempts: %w", len(delays), lastErr)
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func CloseDB() error {
	if db != nil {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}

func GetDB() *gorm.DB {
	return db
}

func IsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
