package service

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"

	"gorm.io/gorm"
)

// commitTickBetweenReadAndWrite parks the serial writer, lets op read the
// inbound and queue its transaction, then commits tick ahead of that transaction.
func commitTickBetweenReadAndWrite(t *testing.T, tick func(tx *gorm.DB) error, op func()) {
	t.Helper()
	resetTrafficWriterForTest(t)
	StartTrafficWriter()

	parked := make(chan struct{})
	release := make(chan struct{})
	tickErr := make(chan error, 1)
	go func() {
		tickErr <- submitTrafficWrite(func() error {
			close(parked)
			<-release
			return database.GetDB().Transaction(tick)
		})
	}()
	<-parked

	opDone := make(chan struct{})
	go func() {
		defer close(opDone)
		op()
	}()
	waitTrafficWriterQueued(t)
	close(release)
	if err := <-tickErr; err != nil {
		t.Fatalf("tick: %v", err)
	}
	<-opDone
}

// seedRenewableNeighbour builds an inbound holding a healthy client X and a
// quota-disabled client Y whose auto-renew is due, as the traffic job sees them.
func seedRenewableNeighbour(t *testing.T, port int, nodeID *int) *model.Inbound {
	t.Helper()
	past := time.Now().Add(-time.Hour).UnixMilli()
	future := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	clients := []model.Client{
		{Email: "x@stale", ID: "aaaaaaaa-0000-0000-0000-00000000000a", SubID: "sub-x", Enable: true, ExpiryTime: future},
		{Email: "y@stale", ID: "aaaaaaaa-0000-0000-0000-00000000000b", SubID: "sub-y", Enable: false, Reset: 30, ExpiryTime: past, TotalGB: 1000},
	}
	settings, err := json.Marshal(map[string]any{"clients": clients, "decryption": "none"})
	if err != nil {
		t.Fatal(err)
	}
	ib := &model.Inbound{
		Tag: "stale-" + strconv.Itoa(port), Enable: true, Port: port, Protocol: model.VLESS,
		Settings: string(settings), NodeID: nodeID,
	}
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	if err := (&ClientService{}).SyncInbound(nil, ib.Id, clients); err != nil {
		t.Fatalf("SyncInbound: %v", err)
	}
	rows := []xray.ClientTraffic{
		{InboundId: ib.Id, Email: "x@stale", Enable: true, ExpiryTime: future},
		{InboundId: ib.Id, Email: "y@stale", Enable: false, Up: 600, Down: 400, Total: 1000, Reset: 30, ExpiryTime: past},
	}
	if err := database.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("seed client_traffics: %v", err)
	}
	return ib
}

func autoRenewTick(tx *gorm.DB) error {
	_, _, _, err := (&InboundService{}).autoRenewClients(tx)
	return err
}

func settingsClient(t *testing.T, inboundId int, email string) (model.Client, bool) {
	t.Helper()
	ib, err := (&InboundService{}).GetInbound(inboundId)
	if err != nil {
		t.Fatalf("GetInbound: %v", err)
	}
	clients, err := (&InboundService{}).GetClients(ib)
	if err != nil {
		t.Fatalf("GetClients: %v", err)
	}
	for _, c := range clients {
		if c.Email == email {
			return c, true
		}
	}
	return model.Client{}, false
}

func requireNeighbourRenewed(t *testing.T, inboundId int) model.Client {
	t.Helper()
	y, ok := settingsClient(t, inboundId, "y@stale")
	if !ok {
		t.Fatal("neighbour y@stale missing from settings")
	}
	if now := time.Now().UnixMilli(); !y.Enable || y.ExpiryTime <= now {
		t.Fatalf("renewed neighbour rolled back in settings: enable=%v expiryTime=%d (now %d)", y.Enable, y.ExpiryTime, now)
	}
	return y
}

type staleClientOp struct {
	name string
	run  func(t *testing.T, ib *model.Inbound) error
}

var staleClientOps = []staleClientOp{
	{"edit", func(t *testing.T, ib *model.Inbound) error {
		rec := lookupClientRecord(t, "x@stale")
		edited := rec.ToClient()
		edited.Comment = "edited"
		_, err := (&ClientService{}).UpdateInboundClient(&InboundService{}, &model.Inbound{
			Id: ib.Id, Settings: clientsSettings(t, []model.Client{*edited}),
		}, "x@stale")
		return err
	}},
	{"add", func(t *testing.T, ib *model.Inbound) error {
		_, err := (&ClientService{}).AddInboundClient(&InboundService{}, &model.Inbound{
			Id: ib.Id, Settings: clientsSettings(t, []model.Client{{Email: "z@stale", ID: "aaaaaaaa-0000-0000-0000-00000000000c", Enable: true}}),
		})
		return err
	}},
	{"delete", func(t *testing.T, ib *model.Inbound) error {
		_, err := (&ClientService{}).DelInboundClientByEmail(&InboundService{}, ib.Id, "x@stale", false, true)
		return err
	}},
	{"bulk detach", func(t *testing.T, ib *model.Inbound) error {
		_, _, err := (&ClientService{}).BulkDetach(&InboundService{}, []string{"x@stale"}, []int{ib.Id})
		return err
	}},
	{"bulk adjust", func(t *testing.T, ib *model.Inbound) error {
		_, _, err := (&ClientService{}).BulkAdjust(&InboundService{}, []string{"x@stale"}, 1, 0, "", "")
		return err
	}},
	{"bulk delete", func(t *testing.T, ib *model.Inbound) error {
		_, _, err := (&ClientService{}).BulkDelete(&InboundService{}, []string{"x@stale"}, false)
		return err
	}},
	{"bulk set enable", func(t *testing.T, ib *model.Inbound) error {
		_, _, err := (&ClientService{}).BulkSetEnable(&InboundService{}, []string{"x@stale"}, false)
		return err
	}},
}

// Each client op reads the inbound before queueing its write; a renewal the
// traffic writer commits in between must not be reverted to enable=false.
func TestClientOpsKeepNeighbourRenewedMidOp(t *testing.T) {
	for i, op := range staleClientOps {
		t.Run(op.name, func(t *testing.T) {
			setupBulkDB(t)
			ib := seedRenewableNeighbour(t, 23101+i, nil)
			commitTickBetweenReadAndWrite(t, autoRenewTick, func() {
				if err := op.run(t, ib); err != nil {
					t.Errorf("%s: %v", op.name, err)
				}
			})
			requireNeighbourRenewed(t, ib.Id)
		})
	}
}

// An op on the renewed client itself keeps the fields it did not change.
func TestBulkAdjustOnRenewedClientKeepsRenewal(t *testing.T) {
	setupBulkDB(t)
	ib := seedRenewableNeighbour(t, 23120, nil)
	commitTickBetweenReadAndWrite(t, autoRenewTick, func() {
		if _, _, err := (&ClientService{}).BulkAdjust(&InboundService{}, []string{"y@stale"}, 0, 500, "", ""); err != nil {
			t.Errorf("BulkAdjust: %v", err)
		}
	})
	if y := requireNeighbourRenewed(t, ib.Id); y.TotalGB != 1500 {
		t.Fatalf("y@stale totalGB = %d, want 1500 (the adjust itself was lost)", y.TotalGB)
	}
}

func TestClientEditKeepsConcurrentRenewalInTraffic(t *testing.T) {
	for _, email := range []string{"y@stale", "renamed@stale"} {
		t.Run(email, func(t *testing.T) { testClientEditKeepsConcurrentRenewal(t, email) })
	}
}

func testClientEditKeepsConcurrentRenewal(t *testing.T, email string) {
	t.Helper()
	setupBulkDB(t)
	ib := seedRenewableNeighbour(t, 23204, nil)
	record := lookupClientRecord(t, "y@stale")
	client := record.ToClient()
	client.Comment = "edited"
	client.Email = email
	commitTickBetweenReadAndWrite(t, autoRenewTick, func() {
		_, err := (&ClientService{}).UpdateInboundClient(&InboundService{}, &model.Inbound{
			Id: ib.Id, Settings: clientsSettings(t, []model.Client{*client}),
		}, "y@stale")
		if err != nil {
			t.Errorf("UpdateInboundClient: %v", err)
		}
	})
	y, found := settingsClient(t, ib.Id, email)
	if !found || !y.Enable || y.ExpiryTime <= time.Now().UnixMilli() {
		t.Fatalf("edited client found=%v enable=%v expiry=%d", found, y.Enable, y.ExpiryTime)
	}
	var traffic xray.ClientTraffic
	if err := database.GetDB().Where("email = ?", y.Email).First(&traffic).Error; err != nil {
		t.Fatal(err)
	}
	if !traffic.Enable || traffic.ExpiryTime != y.ExpiryTime {
		t.Fatalf("traffic enable=%v expiry=%d, want true/%d", traffic.Enable, traffic.ExpiryTime, y.ExpiryTime)
	}
}
