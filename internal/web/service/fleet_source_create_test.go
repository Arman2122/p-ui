package service

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func fleetCreateFixture(t *testing.T) (*gorm.DB, model.User, model.Inbound) {
	t.Helper()
	initTestDB(t)
	db := database.GetDB()
	var actor model.User
	if err := db.First(&actor).Error; err != nil {
		t.Fatal(err)
	}
	inbound := model.Inbound{UserId: actor.Id, Protocol: model.VLESS, Tag: uuid.NewString(), Port: 24331, Settings: `{"decryption":"none","clients":[]}`, StreamSettings: `{}`, Sniffing: `{}`}
	if err := db.Create(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	return db, actor, inbound
}

func fleetCreateRequest(inboundID int) entity.ClientCreatePayload {
	return entity.ClientCreatePayload{OperationID: uuid.NewString(), Client: model.Client{Email: "owned-create", TotalGB: 2048, Enable: false}, InboundIds: []int{inboundID}}
}

func requireCreateCount(t *testing.T, db *gorm.DB, row any, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(row).Count(&count).Error; err != nil || count != want {
		t.Fatalf("%T rows=%d want=%d err=%v", row, count, want, err)
	}
}

func TestFleetSourceCreateAtomicDisabledReplayPostgres(t *testing.T) {
	db, actor, inbound := fleetCreateFixture(t)
	request := fleetCreateRequest(inbound.Id)
	request.ExternalLinks = []entity.ExternalLinkInput{{Kind: "subscription", Value: "https://example.invalid/private-subscription", Remark: "external"}}
	before, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	created, err := CreateFleetClientTx(t.Context(), db, &actor, request)
	if err != nil || !created.Pending || created.Revision != 1 || created.Replayed || !fleetID(created.ClientID) {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	var record model.ClientRecord
	if err := db.First(&record, created.PanelClientID).Error; err != nil || record.Enable || record.TotalGB != 2048 || !fleetID(record.UUID) || !fleetID(record.SubID) {
		t.Fatalf("disabled creation changed source flags or lost identity: enabled=%v err=%v", record.Enable, err)
	}
	var owner model.FleetClient
	if err := db.First(&owner, "id = ?", created.ClientID).Error; err != nil || !owner.Owned || owner.CurrentTermID != nil || owner.PendingTermID != nil {
		t.Fatalf("create prematurely activated billing: %+v %v", owner, err)
	}
	for _, row := range []any{&model.ClientRecord{}, &model.ClientInbound{}, &model.ClientExternalLink{}, &model.FleetClient{}, &model.FleetSourceCredentialClaim{}, &model.FleetOperation{}, &model.FleetPlacementFanout{}} {
		requireCreateCount(t, db, row, 1)
	}
	for _, row := range []any{&model.ClientPolicy{}, &model.FleetTerm{}, &model.FleetGrant{}, &model.FleetPlacement{}, &model.FleetDeployment{}} {
		requireCreateCount(t, db, row, 0)
	}
	replayed, err := CreateFleetClientTx(t.Context(), db, &actor, request)
	if err != nil || !replayed.Replayed || replayed.ClientID != created.ClientID || replayed.PanelClientID != created.PanelClientID {
		t.Fatalf("retry did not recover original create: %+v %v", replayed, err)
	}
	after, err := json.Marshal(request)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("creation mutated submitted request")
	}
	request.Client.TotalGB++
	if _, err := CreateFleetClientTx(t.Context(), db, &actor, request); !errors.Is(err, controlplane.ErrConflict) {
		t.Fatalf("changed retry accepted: %v", err)
	}
	if err := db.First(&inbound, inbound.Id).Error; err != nil || inbound.Settings != `{"decryption":"none","clients":[]}` {
		t.Fatalf("source creation rewrote local core configuration: %v", err)
	}
}

func TestFleetSourceCreateRollsBackPlanLinksClaimsAndReceiptPostgres(t *testing.T) {
	db, actor, inbound := fleetCreateFixture(t)
	quota := int64(8192)
	plan := model.Policy{Name: "create-plan", QuotaBytes: &quota, Tiers: `[]`, OnDepletion: "disable", Rev: 4}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.PolicyInbound{PolicyId: plan.Id, InboundId: inbound.Id}).Error; err != nil {
		t.Fatal(err)
	}
	request := fleetCreateRequest(inbound.Id)
	request.InboundIds, request.PolicyID = nil, &plan.Id
	request.Client.Group = "created-group"
	request.ExternalLinks = []entity.ExternalLinkInput{{Kind: "subscription", Value: "https://example.invalid/sub"}}
	if err := db.Exec(`CREATE FUNCTION reject_create_fanout() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'intent rejected'; END $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER reject_create_fanout BEFORE INSERT ON fleet_placement_fanouts FOR EACH ROW EXECUTE FUNCTION reject_create_fanout()`).Error; err != nil {
		t.Fatal(err)
	}
	_, err := CreateFleetClientTx(t.Context(), db, &actor, request)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "P0001" {
		t.Fatalf("expected durable intent failure, got %v", err)
	}
	for _, row := range []any{&model.ClientRecord{}, &model.ClientPolicy{}, &model.ClientGroup{}, &model.ClientInbound{}, &model.ClientCredential{}, &model.ClientExternalLink{}, &model.FleetClient{}, &model.FleetSourceCredentialClaim{}, &model.FleetOperation{}, &model.FleetPlacementFanout{}} {
		requireCreateCount(t, db, row, 0)
	}
	if err := db.Exec(`DROP TRIGGER reject_create_fanout ON fleet_placement_fanouts`).Error; err != nil {
		t.Fatal(err)
	}
	created, err := CreateFleetClientTx(t.Context(), db, &actor, request)
	if err != nil {
		t.Fatal(err)
	}
	var record model.ClientRecord
	if err := db.First(&record, created.PanelClientID).Error; err != nil || record.TotalGB != quota {
		t.Fatalf("plan was not applied: %v", err)
	}
	requireCreateCount(t, db, &model.ClientInbound{}, 0)
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := fleetSnapshot(tx, record.Id, []int{inbound.Id}, "xray")
		return err
	}); err != nil {
		t.Fatalf("inherited plan template unavailable to compiler: %v", err)
	}
	if err := db.Model(&plan).Update("archived", true).Error; err != nil {
		t.Fatal(err)
	}
	replayed, err := CreateFleetClientTx(t.Context(), db, &actor, request)
	if err != nil || !replayed.Replayed || replayed.ClientID != created.ClientID {
		t.Fatalf("retry re-resolved changed plan: %+v %v", replayed, err)
	}
}

func TestFleetSourceCreateClaimsCanonicalIdentityAndDisabledOwnersPostgres(t *testing.T) {
	db, actor, inbound := fleetCreateFixture(t)
	request := fleetCreateRequest(inbound.Id)
	request.Client.ID = "01234567-89ab-4cde-8123-456789abcdef"
	if _, err := CreateFleetClientTx(t.Context(), db, &actor, request); err != nil {
		t.Fatal(err)
	}
	request.OperationID, request.Client.Email = uuid.NewString(), "second-owner"
	request.Client.ID = "01234567-89ab-4fff-8123-456789abcdef"
	if _, err := CreateFleetClientTx(t.Context(), db, &actor, request); !errors.Is(err, ErrFleetCredentialClaim) {
		t.Fatalf("VLESS canonical lookup alias escaped reserved disabled-owner claim: %v", err)
	}
	requireCreateCount(t, db, &model.ClientRecord{}, 1)
	requireCreateCount(t, db, &model.FleetOperation{}, 1)
	other := inbound
	other.Id, other.Port, other.Tag = 0, 24332, uuid.NewString()
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	request.InboundIds = []int{other.Id}
	if _, err := CreateFleetClientTx(t.Context(), db, &actor, request); err != nil {
		t.Fatalf("independent template incorrectly shared auth namespace: %v", err)
	}
}

func TestFleetSourceCreateConcurrentRetriesAndSubscriptionIdentityPostgres(t *testing.T) {
	db, actor, inbound := fleetCreateFixture(t)
	request := fleetCreateRequest(inbound.Id)
	const workers = 6
	results := make([]entity.FleetClientCreateResult, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() { results[i], errs[i] = CreateFleetClientTx(t.Context(), db, &actor, request) })
	}
	wg.Wait()
	fresh := 0
	for i, result := range results {
		if errs[i] != nil || result.ClientID != results[0].ClientID || result.PanelClientID != results[0].PanelClientID {
			t.Fatalf("concurrent retry %d diverged: %+v %v", i, result, errs[i])
		}
		if !result.Replayed {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh operations=%d, want exactly one", fresh)
	}
	for i := range workers {
		wg.Go(func() {
			r := fleetCreateRequest(inbound.Id)
			r.Client.Email, r.Client.SubID = fmt.Sprintf("concurrent-sub-%d", i), "same-subscription"
			results[i], errs[i] = CreateFleetClientTx(t.Context(), db, &actor, r)
		})
	}
	wg.Wait()
	fresh = 0
	for _, err := range errs {
		if err == nil {
			fresh++
		} else if !errors.Is(err, controlplane.ErrConflict) {
			t.Fatalf("subscription uniqueness failed unexpectedly: %v", err)
		}
	}
	if fresh != 1 {
		t.Fatalf("same subscription created %d identities", fresh)
	}
	requireCreateCount(t, db, &model.ClientRecord{}, 2)
}

func TestFleetSourceCreateRuntimeRequiresTransactionAndRollsBackWithCallerPostgres(t *testing.T) {
	db, actor, inbound := fleetCreateFixture(t)
	dispatch, err := NewFleetRuntime(db)
	if err != nil {
		t.Fatal(err)
	}
	create := dispatch.(webruntime.SourceCreateRuntime)
	request := fleetCreateRequest(inbound.Id)
	if _, err := create.CreateClientTx(t.Context(), db, &actor, request); !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("nontransactional dispatch accepted: %v", err)
	}
	rollback := errors.New("caller rejected transaction")
	err = db.Transaction(func(tx *gorm.DB) error {
		if _, err := create.CreateClientTx(t.Context(), tx, &actor, request); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	requireCreateCount(t, db, &model.ClientRecord{}, 0)
	requireCreateCount(t, db, &model.FleetOperation{}, 0)
	requireCreateCount(t, db, &model.FleetSourceCredentialClaim{}, 0)
}

func TestFleetSourceCreateRequiresClaimsBeforeNewInheritedPlacementPostgres(t *testing.T) {
	db, actor, inbound := fleetCreateFixture(t)
	group := model.ClientGroup{Name: "source-group"}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	request := fleetCreateRequest(inbound.Id)
	request.Client.Group = group.Name
	created, err := CreateFleetClientTx(t.Context(), db, &actor, request)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func(inboundID int) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := fleetSnapshot(tx, created.PanelClientID, []int{inboundID}, "xray")
			return err
		})
	}
	if err := snapshot(inbound.Id); err != nil {
		t.Fatalf("created credential claim was not usable: %v", err)
	}
	other := inbound
	other.Id, other.Port, other.Tag = 0, 24332, uuid.NewString()
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.GroupInbound{Name: group.Name, InboundID: other.Id}).Error; err != nil {
		t.Fatal(err)
	}
	if err := snapshot(other.Id); !errors.Is(err, ErrFleetCredentialClaim) {
		t.Fatalf("unprepared inherited authentication identity became admissible: %v", err)
	}
	if err := db.Where("client_id = ?", created.ClientID).Delete(&model.FleetSourceCredentialClaim{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := snapshot(inbound.Id); !errors.Is(err, ErrFleetCredentialClaim) {
		t.Fatalf("lost claims were silently reconstructed by materialization: %v", err)
	}
}

func TestFleetSourceCreateRefusesLegacyWritesBeforePlanAssignmentPostgres(t *testing.T) {
	db, _, inbound := fleetCreateFixture(t)
	dispatch, err := NewFleetRuntime(db)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := webruntime.NewFleetManager(dispatch)
	if err != nil {
		t.Fatal(err)
	}
	previous := webruntime.GetManager()
	t.Cleanup(func() { webruntime.SetManager(previous) })
	webruntime.SetManager(manager)
	plan := model.Policy{Name: "guarded-source-plan", Tiers: `[]`, OnDepletion: "disable"}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	for _, planned := range []bool{false, true} {
		request := fleetCreateRequest(inbound.Id)
		request.OperationID = ""
		if planned {
			request.PolicyID = &plan.Id
		}
		if restart, err := (&ClientService{}).Create(&InboundService{}, &request); !errors.Is(err, ErrFleetRuntimeCommand) || restart {
			t.Fatalf("legacy path did not refuse owned creation before writes: planned=%v restart=%v err=%v", planned, restart, err)
		}
	}
	webruntime.SetManager(nil)
	request := fleetCreateRequest(inbound.Id)
	if restart, err := (&ClientService{}).Create(&InboundService{}, &request); !errors.Is(err, ErrFleetRuntimeCommand) || restart {
		t.Fatalf("legacy path silently ignored creation operation: restart=%v err=%v", restart, err)
	}
	requireCreateCount(t, db, &model.ClientRecord{}, 0)
	requireCreateCount(t, db, &model.ClientPolicy{}, 0)
}

func TestFleetSourceCreateNativeCredentialsStayPendingWithoutHostGenerationPostgres(t *testing.T) {
	db, actor, inbound := fleetCreateFixture(t)
	if err := db.Model(&inbound).Updates(map[string]any{"protocol": "openvpn", "settings": `{"tlsCryptV2ServerKey":"source-server-material"}`}).Error; err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	blocked := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{"TMPDIR", "TMP", "TEMP", "PUI_BIN_FOLDER"} {
		t.Setenv(variable, blocked)
	}
	created, err := CreateFleetClientTx(t.Context(), db, &actor, fleetCreateRequest(inbound.Id))
	if err != nil || len(created.NodeDerived) != 1 || created.NodeDerived[0].InboundID != inbound.Id || len(created.NodeDerived[0].Fields) != 1 || created.NodeDerived[0].Fields[0] != core.CredTLSCryptV2 {
		t.Fatalf("native creation invoked host dependencies or lost pending resource: %+v %v", created, err)
	}
	var rows []model.ClientCredential
	if err := db.Where("client_id = ?", created.PanelClientID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("native credentials missing or host key invented: rows=%d", len(rows))
	}
	for _, row := range rows {
		if row.Value == "" || row.Key != core.CredUsername && row.Key != core.CredPassword {
			t.Fatal("native source persisted unqualified material")
		}
	}
	requireCreateCount(t, db, &model.FleetTerm{}, 0)
	requireCreateCount(t, db, &model.FleetGrant{}, 0)
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "not-a-directory" {
		t.Fatal("headless source creation wrote master host material")
	}
}

func TestFleetSourceCreateAppliesExplicitPlanOverridesAtomicallyPostgres(t *testing.T) {
	db, actor, inbound := fleetCreateFixture(t)
	other := inbound
	other.Id, other.Port, other.Tag = 0, 24332, uuid.NewString()
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	quota, days, ipLimit := int64(8192), 7, 2
	plan := model.Policy{Name: "overridden-source-plan", Tiers: `[]`, OnDepletion: "disable", ResetCycle: "weekly", QuotaBytes: &quota, DurationDays: &days, LimitIP: &ipLimit, StartOnFirstUse: true, Rev: 3}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.PolicyInbound{PolicyId: plan.Id, InboundId: inbound.Id}).Error; err != nil {
		t.Fatal(err)
	}
	request := fleetCreateRequest(other.Id)
	request.PolicyID = &plan.Id
	request.Client.TotalGB, request.Client.ExpiryTime, request.Client.LimitIP, request.Client.Reset = 10000, -172800000, 4, 5
	if _, err := CreateFleetClientTx(t.Context(), db, &actor, request); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("explicit template escaped managed plan: %v", err)
	}
	requireCreateCount(t, db, &model.ClientRecord{}, 0)
	requireCreateCount(t, db, &model.FleetOperation{}, 0)
	enabled := true
	request.OperationID = uuid.NewString()
	request.PlanOverrides = &entity.PlanOverrides{Quota: &enabled, Expiry: &enabled, LimitIP: &enabled, Inbounds: &enabled}
	created, err := CreateFleetClientTx(t.Context(), db, &actor, request)
	if err != nil {
		t.Fatal(err)
	}
	var record model.ClientRecord
	if err := db.First(&record, created.PanelClientID).Error; err != nil || record.TotalGB != request.Client.TotalGB || record.ExpiryTime != request.Client.ExpiryTime || record.LimitIP != request.Client.LimitIP || record.Reset != request.Client.Reset {
		t.Fatalf("explicit managed fields were lost: %v", err)
	}
	var assignment model.ClientPolicy
	if err := db.First(&assignment, "email = ?", record.Email).Error; err != nil || assignment.AppliedRev != plan.Rev || !assignment.OverrideQuota || !assignment.OverrideExpiry || !assignment.OverrideLimitIP || !assignment.OverrideInbounds {
		t.Fatalf("creation invalidated applied overrides: %+v %v", assignment, err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := fleetSnapshot(tx, record.Id, []int{other.Id}, "xray")
		return err
	}); err != nil {
		t.Fatalf("valid override unavailable to owned compiler: %v", err)
	}
	var count int64
	if err := db.Model(&model.ClientInbound{}).Where("client_id = ? AND inbound_id = ?", record.Id, inbound.Id).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("overridden plan leaked an inherited binding: %d %v", count, err)
	}
}

func TestFleetSourceCreateBoundsExpandedCredentialsBeforePersistencePostgres(t *testing.T) {
	db, actor, _ := fleetCreateFixture(t)
	wrapped := string(pem.EncodeToMemory(&pem.Block{Type: "OpenVPN tls-crypt-v2 client key", Bytes: bytes.Repeat([]byte{1}, 20000)}))
	count := maxFleetSourceCredentialBytes/len(wrapped) + 1
	if count > 4096 || len(wrapped) > 32768 {
		t.Fatal("expanded credential fixture no longer fits accepted request bounds")
	}
	inbounds := make([]model.Inbound, count)
	for i := range inbounds {
		inbounds[i] = model.Inbound{UserId: actor.Id, Protocol: model.Protocol("openvpn"), Tag: uuid.NewString(), Port: 30000 + i, Settings: `{}`, StreamSettings: `{}`, Sniffing: `{}`}
	}
	if err := db.CreateInBatches(&inbounds, 100).Error; err != nil {
		t.Fatal(err)
	}
	request := fleetCreateRequest(inbounds[0].Id)
	request.Client.TLSCryptV2 = wrapped
	if _, err := prepareFleetSourceCredentials(request.Client, inbounds[:1], nil); err != nil {
		t.Fatalf("fixture must pass credential validation before testing expansion: %v", err)
	}
	request.InboundIds = make([]int, len(inbounds))
	for i, inbound := range inbounds {
		request.InboundIds[i] = inbound.Id
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) >= 512<<10 {
		t.Fatalf("fixture exceeds input bound instead of exercising expansion: bytes=%d err=%v", len(encoded), err)
	}
	if _, err := CreateFleetClientTx(t.Context(), db, &actor, request); !errors.Is(err, ErrFleetSourceCredentials) {
		t.Fatalf("expanded credentials were stored from a small request: %v", err)
	}
	for _, row := range []any{&model.ClientRecord{}, &model.ClientCredential{}, &model.FleetClient{}, &model.FleetOperation{}} {
		requireCreateCount(t, db, row, 0)
	}
}

func TestFleetSourceCreatePreservesRequestedFlowForExplicitBindingPostgres(t *testing.T) {
	db, actor, inbound := fleetCreateFixture(t)
	if err := db.Model(&inbound).Update("stream_settings", `{"network":"tcp","security":"tls"}`).Error; err != nil {
		t.Fatal(err)
	}
	request := fleetCreateRequest(inbound.Id)
	request.Client.Flow = "xtls-rprx-vision"
	created, err := CreateFleetClientTx(t.Context(), db, &actor, request)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		snapshot, err := fleetSnapshot(tx, created.PanelClientID, []int{inbound.Id}, "xray")
		if err != nil {
			return err
		}
		user, err := fleetNetworkUser(snapshot, inbound.Id)
		if err != nil {
			return err
		}
		if actual := core.CredString(user.Credentials, "flow"); actual != request.Client.Flow {
			t.Errorf("explicit binding discarded requested flow: got=%q want=%q", actual, request.Client.Flow)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
