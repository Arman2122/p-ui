package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

// TestMigrationRequirements_BackfillsClientTrafficsWithMultiDomainInbound guards the
// PostgreSQL fix where the externalProxy detection query (executed via .Scan) errored on
// json_extract and rolled back the whole transaction — including the client_traffics
// backfill at inbound.go:3093-3106, leaving clients with no traffic rows. A MultiDomain
// inbound is present so that query returns rows and the function runs to completion; both
// the backfill and the MultiDomain→ExternalProxy migration must then commit.
func TestMigrationRequirements_BackfillsClientTrafficsWithMultiDomainInbound(t *testing.T) {
	initTestDB(t)

	db := database.GetDB()

	const backfillEmail = "needsbackfill@example.com"
	const uid = "ce8d33df-3a64-4f10-8f9b-91c3a8e0c010"

	// Inbound A: a client present only in settings.clients, with no client_traffics row.
	clientInbound := &model.Inbound{
		UserId:         1,
		Tag:            "a-tag",
		Enable:         true,
		Port:           30001,
		Protocol:       model.VLESS,
		Settings:       `{"clients":[{"email":"` + backfillEmail + `","id":"` + uid + `","enable":true}]}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := db.Create(clientInbound).Error; err != nil {
		t.Fatalf("create client inbound: %v", err)
	}

	// Inbound B: a legacy MultiDomain inbound whose tag carries the 0.0.0.0: prefix.
	// Its presence makes the externalProxy query return rows, so the function does not
	// early-return and reaches the tag-cleanup statement.
	multiDomainInbound := &model.Inbound{
		UserId:         1,
		Tag:            "inbound-0.0.0.0:30002",
		Enable:         true,
		Port:           30002,
		Protocol:       model.VLESS,
		Settings:       `{"clients":[]}`,
		StreamSettings: `{"security":"tls","tlsSettings":{"settings":{"domains":[{"domain":"example.com"}]}}}`,
	}
	if err := db.Create(multiDomainInbound).Error; err != nil {
		t.Fatalf("create multidomain inbound: %v", err)
	}

	var before int64
	if err := db.Model(xray.ClientTraffic{}).Count(&before).Error; err != nil {
		t.Fatalf("count client_traffics before: %v", err)
	}
	if before != 0 {
		t.Fatalf("expected no client_traffics before migration, got %d", before)
	}

	svc := InboundService{}
	svc.MigrationRequirements()

	// The backfill must have committed: the settings-only client now owns a row.
	// Before the fix this was rolled back whenever the externalProxy detection query
	// errored (it does on Postgres via json_extract), so the MultiDomain inbound below
	// is deliberately present to make that query return rows and run to completion.
	var ct xray.ClientTraffic
	if err := db.Model(xray.ClientTraffic{}).Where("email = ?", backfillEmail).First(&ct).Error; err != nil {
		t.Fatalf("client_traffics row not backfilled for %s: %v", backfillEmail, err)
	}

	// The MultiDomain→ExternalProxy migration must have committed too: the detection
	// query ran (.Scan executes it) and the loop rewrote the inbound's streamSettings.
	var refreshed model.Inbound
	if err := db.First(&refreshed, multiDomainInbound.Id).Error; err != nil {
		t.Fatalf("reload multidomain inbound: %v", err)
	}
	if !strings.Contains(refreshed.StreamSettings, "externalProxy") {
		t.Errorf("MultiDomain migration did not commit; streamSettings = %q", refreshed.StreamSettings)
	}
}

// TestMigrationRequirements_CleansLegacyZeroAddrTag guards the legacy tag cleanup that
// strips the auto-generated "0.0.0.0:" prefix. The inbound is MultiDomain TLS so the
// externalProxy detection query returns rows and the cleanup is reached (it early-returns
// at len(externalProxy)==0 otherwise). The cleanup must use tx.Exec, not tx.Raw, which
// only builds a non-SELECT statement without running it.
func TestMigrationRequirements_CleansLegacyZeroAddrTag(t *testing.T) {
	initTestDB(t)

	db := database.GetDB()
	legacy := &model.Inbound{
		UserId:         1,
		Tag:            "inbound-0.0.0.0:30002",
		Enable:         true,
		Port:           30002,
		Protocol:       model.VLESS,
		Settings:       `{"clients":[]}`,
		StreamSettings: `{"security":"tls","tlsSettings":{"settings":{"domains":[{"domain":"example.com"}]}}}`,
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatalf("create legacy inbound: %v", err)
	}

	svc := InboundService{}
	svc.MigrationRequirements()

	var got model.Inbound
	if err := db.First(&got, legacy.Id).Error; err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if got.Tag != "inbound-30002" {
		t.Fatalf("legacy 0.0.0.0: tag not stripped: got %q, want %q", got.Tag, "inbound-30002")
	}
}

func TestMigrationRemoveOrphanedTraffics(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	clientSvc := &ClientService{}
	inboundSvc := &InboundService{}

	const attachedEmail = "attached@example.com"
	attachedClient := model.Client{Email: attachedEmail, ID: "11111111-1111-1111-1111-111111111111", SubID: attachedEmail, Enable: true}
	attachedIb := mkInbound(t, 30003, model.VLESS, clientsSettings(t, []model.Client{attachedClient}))
	if err := clientSvc.SyncInbound(nil, attachedIb.Id, []model.Client{attachedClient}); err != nil {
		t.Fatalf("seed attached client: %v", err)
	}
	mkTraffic(t, attachedIb.Id, attachedEmail, 0, 0, 0, 0, true)

	const detachedEmail = "detached@example.com"
	detachedClient := model.Client{Email: detachedEmail, ID: "22222222-2222-2222-2222-222222222222", SubID: detachedEmail, Enable: true}
	detachedIb := mkInbound(t, 30004, model.VLESS, clientsSettings(t, []model.Client{detachedClient}))
	if err := clientSvc.SyncInbound(nil, detachedIb.Id, []model.Client{detachedClient}); err != nil {
		t.Fatalf("seed detached client: %v", err)
	}
	mkTraffic(t, detachedIb.Id, detachedEmail, 123, 456, 0, 0, true)
	detachedRec := lookupClientRecord(t, detachedEmail)
	if _, err := clientSvc.Detach(inboundSvc, detachedRec.Id, []int{detachedIb.Id}); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	const jsonOnlyEmail = "jsononly@example.com"
	jsonOnlyClient := model.Client{Email: jsonOnlyEmail, ID: "33333333-3333-3333-3333-333333333333", SubID: jsonOnlyEmail, Enable: true}
	jsonOnlyIb := mkInbound(t, 30005, model.VLESS, clientsSettings(t, []model.Client{jsonOnlyClient}))
	mkTraffic(t, jsonOnlyIb.Id, jsonOnlyEmail, 0, 0, 0, 0, true)

	const trulyOrphanedEmail = "deleted@example.com"
	mkTraffic(t, attachedIb.Id, trulyOrphanedEmail, 0, 0, 0, 0, true)

	inboundSvc.MigrationRemoveOrphanedTraffics()

	cases := []struct {
		name  string
		email string
		want  int64
	}{
		{"attached, in clients table and JSON", attachedEmail, 1},
		{"detached-but-alive, in clients table only", detachedEmail, 1},
		{"seeder-skipped-but-live, in JSON only", jsonOnlyEmail, 1},
		{"truly orphaned, in neither", trulyOrphanedEmail, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got int64
			if err := db.Model(xray.ClientTraffic{}).Where("email = ?", c.email).Count(&got).Error; err != nil {
				t.Fatalf("count client_traffics for %s: %v", c.email, err)
			}
			if got != c.want {
				t.Errorf("client_traffics count for %s: got %d, want %d", c.email, got, c.want)
			}
		})
	}
}

func TestMigrationRequirements_NormalizesShareAddressFields(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()

	invalidStrategy := &model.Inbound{
		UserId:         1,
		Tag:            "invalid-share-strategy",
		Enable:         true,
		Port:           31001,
		Protocol:       model.VLESS,
		Settings:       `{"clients":[]}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	paddedStrategy := &model.Inbound{
		UserId:         1,
		Tag:            "padded-share-strategy",
		Enable:         true,
		Port:           31002,
		Protocol:       model.VLESS,
		Settings:       `{"clients":[]}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	invalidAddress := &model.Inbound{
		UserId:         1,
		Tag:            "invalid-share-address",
		Enable:         true,
		Port:           31003,
		Protocol:       model.VLESS,
		Settings:       `{"clients":[]}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := db.Create(invalidStrategy).Error; err != nil {
		t.Fatalf("create invalid strategy inbound: %v", err)
	}
	if err := db.Create(paddedStrategy).Error; err != nil {
		t.Fatalf("create padded strategy inbound: %v", err)
	}
	if err := db.Create(invalidAddress).Error; err != nil {
		t.Fatalf("create invalid address inbound: %v", err)
	}
	if err := db.Model(&model.Inbound{}).Where("id = ?", invalidStrategy.Id).Updates(map[string]any{
		"share_addr_strategy": " auto ",
		"share_addr":          "  edge.example.com  ",
	}).Error; err != nil {
		t.Fatalf("seed invalid share fields: %v", err)
	}
	if err := db.Model(&model.Inbound{}).Where("id = ?", paddedStrategy.Id).Updates(map[string]any{
		"share_addr_strategy": " listen ",
		"share_addr":          "  10.0.0.1  ",
	}).Error; err != nil {
		t.Fatalf("seed padded share fields: %v", err)
	}
	if err := db.Model(&model.Inbound{}).Where("id = ?", invalidAddress.Id).Updates(map[string]any{
		"share_addr_strategy": "custom",
		"share_addr":          "edge.example.com:8443",
	}).Error; err != nil {
		t.Fatalf("seed invalid address share fields: %v", err)
	}

	svc := InboundService{}
	svc.MigrationRequirements()

	var gotInvalid model.Inbound
	if err := db.First(&gotInvalid, invalidStrategy.Id).Error; err != nil {
		t.Fatalf("reload invalid strategy inbound: %v", err)
	}
	if gotInvalid.ShareAddrStrategy != "node" || gotInvalid.ShareAddr != "edge.example.com" {
		t.Fatalf("invalid share fields = (%q, %q), want (node, edge.example.com)", gotInvalid.ShareAddrStrategy, gotInvalid.ShareAddr)
	}

	var gotPadded model.Inbound
	if err := db.First(&gotPadded, paddedStrategy.Id).Error; err != nil {
		t.Fatalf("reload padded strategy inbound: %v", err)
	}
	if gotPadded.ShareAddrStrategy != "listen" || gotPadded.ShareAddr != "10.0.0.1" {
		t.Fatalf("padded share fields = (%q, %q), want (listen, 10.0.0.1)", gotPadded.ShareAddrStrategy, gotPadded.ShareAddr)
	}

	var gotInvalidAddress model.Inbound
	if err := db.First(&gotInvalidAddress, invalidAddress.Id).Error; err != nil {
		t.Fatalf("reload invalid address inbound: %v", err)
	}
	if gotInvalidAddress.ShareAddrStrategy != "node" || gotInvalidAddress.ShareAddr != "" {
		t.Fatalf("invalid address share fields = (%q, %q), want (node, empty)", gotInvalidAddress.ShareAddrStrategy, gotInvalidAddress.ShareAddr)
	}
}

/*
A junk element in settings.clients must not stop the panel from migrating.

`clients[i].(map[string]any)` was a single-value assertion, so a null or scalar
element panicked MigrationRequirements. That runs from `p-ui migrate`, which
install.sh:364 and update.sh:772 both gate on: one such row and the operator's
UPGRADE fails, on a panel that was serving traffic a moment earlier. The same
call runs after a database restore, where the dump being restored is exactly
the kind of foreign data that carries a shape this panel never wrote.

Dropped rather than passed through, because the element is fatal further down:
ParseInboundSettingsClients unmarshals into []model.Client and a scalar hard-
errors it, so preserving it would break every client read of that inbound AND
abort the rest of MigrationRequirements at the GetClients call in this same
loop -- trading a panic for a silent whole-migration no-op. The drop is logged
with the inbound id and index. F383.
*/
func TestMigrationRequirementsSurvivesANonObjectClientElement(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	const good = "survivor@example.com"
	junk := &model.Inbound{
		UserId: 1, Tag: "junk-clients", Enable: true, Port: 30401, Protocol: model.VLESS,
		Settings: `{"clients":[null,"not-an-object",42,{"email":"` + good +
			`","id":"ce8d33df-3a64-4f10-8f9b-91c3a8e0c9ff","enable":true}]}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := db.Create(junk).Error; err != nil {
		t.Fatalf("create the inbound: %v", err)
	}

	// The premise: this row is unreadable until the migration repairs it.
	if _, err := ParseInboundSettingsClients(junk.Settings); err == nil {
		t.Fatal("the scalar element no longer breaks ParseInboundSettingsClients; this test cannot see the bug")
	}

	(&InboundService{}).MigrationRequirements()

	var after model.Inbound
	if err := db.First(&after, junk.Id).Error; err != nil {
		t.Fatalf("re-read the inbound: %v", err)
	}

	// The repair: the inbound's clients can be read at all now.
	parsed, err := ParseInboundSettingsClients(after.Settings)
	if err != nil {
		t.Fatalf("the inbound is still unreadable after the migration, so every client read of it fails: %v", err)
	}
	if len(parsed) != 1 || parsed[0].Email != good {
		t.Fatalf("want the one readable client kept, got %d: %+v", len(parsed), parsed)
	}

	// And it really was migrated, not merely left alone -- proving the loop ran
	// past the junk instead of the whole function returning early.
	settings := map[string]any{}
	if err := json.Unmarshal([]byte(after.Settings), &settings); err != nil {
		t.Fatalf("the migration left unparseable settings: %v", err)
	}
	clients, ok := settings["clients"].([]any)
	if !ok {
		t.Fatalf("clients is no longer an array: %T", settings["clients"])
	}
	if len(clients) != 1 {
		t.Fatalf("want the three junk elements dropped and the one client kept, got %d: %v", len(clients), clients)
	}
	real, ok := clients[0].(map[string]any)
	if !ok {
		t.Fatalf("the surviving client is not an object: %T", clients[0])
	}
	if _, stamped := real["updated_at"]; !stamped {
		t.Error("the surviving client was not migrated; MigrationRequirements returned before tx.Save")
	}
}

/*
A migration that gives up must not commit what it had already done.

MigrationRequirements decides commit-or-rollback from one function-scoped err in
a defer. Two of its early returns declared their own with `:=`, so the outer one
stayed nil and the defer COMMITTED — a set of inbounds half-migrated, with the
column drops and the share-address normalisation applied and the rest of the
loop never run. The next start then migrated from that half-state.

Driven through the share-address normalisation, which is an UPDATE the function
performs before the loop it dies in: after a rollback the row must still hold
what the test wrote. F382.
*/
func TestMigrationRequirementsRollsBackWhenItGivesUp(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	// A client the settings rewrite happily walks, and ParseInboundSettingsClients
	// then refuses: limitIp is an int on model.Client.
	unreadable := &model.Inbound{
		UserId:         1,
		Tag:            "rollback-me",
		Enable:         true,
		Port:           30301,
		Protocol:       model.VLESS,
		Settings:       `{"clients":[{"email":"x@rollback","limitIp":"not-a-number"}]}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := db.Create(unreadable).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	// Written straight to the column: the normalisation is what this observes, so
	// it has to start out un-normalised.
	if err := db.Model(&model.Inbound{}).Where("id = ?", unreadable.Id).
		Update("share_addr_strategy", "bogus").Error; err != nil {
		t.Fatalf("seed the un-normalised column: %v", err)
	}

	(&InboundService{}).MigrationRequirements()

	var strategy string
	if err := db.Model(&model.Inbound{}).Where("id = ?", unreadable.Id).
		Select("share_addr_strategy").Scan(&strategy).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strategy != "bogus" {
		t.Fatalf("share_addr_strategy = %q: the migration gave up part-way and committed anyway, leaving the panel half-migrated", strategy)
	}
}

/*
Running the migration twice must not restamp every client.

MigrationRequirements runs on every panel start, not only on `p-ui migrate`.
created_at was backfilled only when absent; updated_at was assigned
unconditionally, so every restart rewrote the settings JSON of every
vmess/vless/trojan/shadowsocks/hysteria inbound and told the panel each of their
clients had just been modified.

That is not only churn. applyClientRecordMerge decides which side of a node sync
wins by comparing this exact field, so a restart could make the panel's copy
look newer than a node's real edit. F386.
*/
func TestMigrationRequirementsDoesNotRestampAClientItAlreadySaw(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	inbound := &model.Inbound{
		UserId:         1,
		Tag:            "stamped",
		Enable:         true,
		Port:           30311,
		Protocol:       model.VLESS,
		Settings:       `{"clients":[{"email":"stamp@e","id":"ce8d33df-3a64-4f10-8f9b-91c3a8e0c0aa","enable":true}]}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	svc := &InboundService{}
	svc.MigrationRequirements()
	first := clientUpdatedAt(t, inbound.Id)
	if first == 0 {
		t.Fatal("the first run did not stamp updated_at at all, so this proves nothing")
	}

	// A second start, far enough apart that a re-stamp would be a different value.
	time.Sleep(1100 * time.Millisecond)
	svc.MigrationRequirements()

	if second := clientUpdatedAt(t, inbound.Id); second != first {
		t.Fatalf("updated_at moved from %d to %d on a restart that changed nothing; every node sync now reads the panel's copy as the newer one", first, second)
	}
}

func clientUpdatedAt(t *testing.T, inboundId int) int64 {
	t.Helper()
	var settings string
	if err := database.GetDB().Model(&model.Inbound{}).Where("id = ?", inboundId).
		Select("settings").Scan(&settings).Error; err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var parsed struct {
		Clients []struct {
			UpdatedAt int64 `json:"updated_at"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	if len(parsed.Clients) != 1 {
		t.Fatalf("want one client, got %d", len(parsed.Clients))
	}
	return parsed.Clients[0].UpdatedAt
}
