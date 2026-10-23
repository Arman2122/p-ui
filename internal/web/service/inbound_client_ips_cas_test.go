package service

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"

	"gorm.io/gorm"
)

func TestClientIPMergeRetainsConcurrentWriter(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "concurrent creation", true: "concurrent update"}[existing], func(t *testing.T) {
			setupSettingTestDB(t)
			db := database.GetDB()
			const email = "shared-ip@node"
			now := time.Now().Unix()
			blob := func(addresses ...string) string {
				t.Helper()
				entries := make([]clientIpEntry, 0, len(addresses))
				for _, address := range addresses {
					entries = append(entries, clientIpEntry{IP: address, Timestamp: now})
				}
				encoded, err := json.Marshal(entries)
				if err != nil {
					t.Fatal(err)
				}
				return string(encoded)
			}
			if existing {
				if err := db.Create(&model.InboundClientIps{ClientEmail: email, Ips: blob("192.0.2.1")}).Error; err != nil {
					t.Fatal(err)
				}
			}
			fired := false
			inject := func(tx *gorm.DB) {
				if fired || tx.Statement.Table != "inbound_client_ips" {
					return
				}
				fired = true
				var err error
				if existing {
					err = db.Exec("UPDATE inbound_client_ips SET ips = ? WHERE client_email = ?", blob("192.0.2.1", "192.0.2.2"), email).Error
				} else {
					err = db.Exec("INSERT INTO inbound_client_ips (client_email, ips) VALUES (?, ?)", email, blob("192.0.2.2")).Error
				}
				if err != nil {
					tx.AddError(err)
				}
			}
			const name = "test:concurrent-client-ip"
			if existing {
				if err := db.Callback().Update().Before("gorm:update").Register(name, inject); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Callback().Update().Remove(name) })
			} else {
				if err := db.Callback().Create().Before("gorm:create").Register(name, inject); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Callback().Create().Remove(name) })
			}
			if err := (&InboundService{}).MergeInboundClientIps([]model.InboundClientIps{{ClientEmail: email, Ips: blob("192.0.2.3")}}); err != nil {
				t.Fatal(err)
			}
			var row model.InboundClientIps
			if err := db.Where("client_email = ?", email).First(&row).Error; err != nil {
				t.Fatal(err)
			}
			var entries []clientIpEntry
			if err := json.Unmarshal([]byte(row.Ips), &entries); err != nil {
				t.Fatal(err)
			}
			var addresses []string
			for _, entry := range entries {
				addresses = append(addresses, entry.IP)
			}
			slices.Sort(addresses)
			want := []string{"192.0.2.2", "192.0.2.3"}
			if existing {
				want = append([]string{"192.0.2.1"}, want...)
			}
			if !fired || !slices.Equal(addresses, want) {
				t.Fatalf("injected=%v addresses=%v, want %v", fired, addresses, want)
			}
		})
	}
}
