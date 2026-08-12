package service

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	puilogger "github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/xray"

	"github.com/op/go-logging"
	"gorm.io/gorm"
)

// setupScaleDB initializes the PostgreSQL database for a scale benchmark and
// registers cleanup. Skips the test when PUI_DB_DSN is not configured.
func setupScaleDB(t *testing.T) {
	t.Helper()
	puilogger.InitLogger(logging.ERROR)

	if strings.TrimSpace(os.Getenv("PUI_DB_DSN")) == "" {
		t.Skip("set PUI_DB_DSN to run the scale benchmark")
	}
	if err := database.InitDB(); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })
}

// scaleSizes returns the default size ladder unless PUI_SCALE_SIZES overrides
// it with a comma-separated list (e.g. "500000" or "10000,100000,500000").
func scaleSizes(t *testing.T, def ...int) []int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("PUI_SCALE_SIZES"))
	if raw == "" {
		return def
	}
	var out []int
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			t.Fatalf("PUI_SCALE_SIZES: invalid size %q", part)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return def
	}
	return out
}

type scaleDataset struct {
	inboundIds []int
	tags       []string
	emails     []string
	perInbound [][]model.Client
}

// seedScaleDataset seeds n healthy clients (future expiry, unfilled quota)
// spread across numInbounds inbounds, writing inbounds, clients,
// client_inbounds and client_traffics directly in one transaction — orders of
// magnitude faster than SyncInbound and one fsync instead of thousands.
func seedScaleDataset(t *testing.T, n, numInbounds int) scaleDataset {
	t.Helper()
	db := database.GetDB()
	// The per-client tables a measured pass WRITES belong here too, or its own row
	// count is checked against whatever an earlier run left behind.
	resetScaleTables(t, db, "inbounds", "clients", "client_inbounds", "client_traffics",
		"inbound_client_ips", "node_client_ips", "client_policies", "policies")

	clients := makeScaleClients(n)
	exp := time.Now().AddDate(1, 0, 0).UnixMilli()
	for i := range clients {
		clients[i].ExpiryTime = exp
		clients[i].TotalGB = 100 << 30
	}

	ds := scaleDataset{emails: emailsOf(clients)}
	start := time.Now()
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin seed tx: %v", tx.Error)
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	per := n / numInbounds
	for i := range numInbounds {
		lo, hi := i*per, (i+1)*per
		if i == numInbounds-1 {
			hi = n
		}
		chunk := clients[lo:hi]
		ib := &model.Inbound{
			UserId:   1,
			Tag:      fmt.Sprintf("scale-%d-%d", n, i),
			Enable:   true,
			Port:     41000 + i,
			Protocol: model.VLESS,
			Settings: clientsSettings(t, chunk),
		}
		if err := tx.Create(ib).Error; err != nil {
			t.Fatalf("seed inbound %d: %v", i, err)
		}

		records := make([]*model.ClientRecord, len(chunk))
		for j := range chunk {
			records[j] = chunk[j].ToRecord()
		}
		if err := tx.CreateInBatches(records, 500).Error; err != nil {
			t.Fatalf("seed clients %d: %v", i, err)
		}

		links := make([]model.ClientInbound, len(records))
		for j := range records {
			links[j] = model.ClientInbound{ClientId: records[j].Id, InboundId: ib.Id}
		}
		if err := tx.CreateInBatches(links, 1000).Error; err != nil {
			t.Fatalf("seed client_inbounds %d: %v", i, err)
		}

		traffics := make([]xray.ClientTraffic, len(chunk))
		for j := range chunk {
			traffics[j] = xray.ClientTraffic{
				InboundId:  ib.Id,
				Email:      chunk[j].Email,
				Enable:     true,
				Total:      chunk[j].TotalGB,
				ExpiryTime: chunk[j].ExpiryTime,
			}
		}
		if err := tx.CreateInBatches(traffics, 1000).Error; err != nil {
			t.Fatalf("seed client_traffics %d: %v", i, err)
		}

		ds.inboundIds = append(ds.inboundIds, ib.Id)
		ds.tags = append(ds.tags, ib.Tag)
		ds.perInbound = append(ds.perInbound, chunk)
	}

	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}
	committed = true
	db.Exec("ANALYZE")
	t.Logf("seeded N=%d across %d inbound(s) in %v", n, numInbounds, time.Since(start).Round(time.Millisecond))
	return ds
}

// sampleEmails picks k evenly spaced emails so active clients span the id range.
func sampleEmails(emails []string, k int) []string {
	if k >= len(emails) {
		return emails
	}
	out := make([]string, 0, k)
	step := len(emails) / k
	for i := 0; i < len(emails) && len(out) < k; i += step {
		out = append(out, emails[i])
	}
	return out
}

// resetScaleTables empties the given tables between sub-sizes with a single
// TRUNCATE ... CASCADE so ids restart from 1.
func resetScaleTables(t *testing.T, db *gorm.DB, tables ...string) {
	t.Helper()
	stmt := "TRUNCATE TABLE " + strings.Join(tables, ", ") + " RESTART IDENTITY CASCADE"
	if err := db.Exec(stmt).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
