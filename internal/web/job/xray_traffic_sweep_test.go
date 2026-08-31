package job

import (
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
An expired client is cut off on a panel that does not run Xray.

The traffic job returned early when no core had reported bytes AND Xray was not
running -- and disableInvalidClients, the only thing that flips
client_traffics.enable false for an over-quota or expired client, lives inside
the AddTraffic that early return skipped.

Xray's state says nothing about the cores Xray does not serve. A kernel
WireGuard peer is served by the kernel: its client expired weeks ago, no poll
ever reached the sweep, the peer stayed in the device and stayed connectable.
The sweep is a database predicate and needs no core at all. F260.
*/
func TestAnExpiredClientIsDisabledWithNoCoreReporting(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	expired := &xray.ClientTraffic{
		Email:      "expired@wg",
		Enable:     true,
		ExpiryTime: time.Now().Add(-24 * time.Hour).UnixMilli(),
	}
	if err := db.Create(expired).Error; err != nil {
		t.Fatalf("seed the expired client: %v", err)
	}
	if err := db.Create(&model.ClientRecord{Email: "expired@wg", Enable: true}).Error; err != nil {
		t.Fatalf("seed the client record: %v", err)
	}

	// No cores registered, so this poll reports nothing, and no Xray is running.
	NewXrayTrafficJob().Run()

	var after xray.ClientTraffic
	if err := db.Where("email = ?", "expired@wg").First(&after).Error; err != nil {
		t.Fatalf("reload the client: %v", err)
	}
	if after.Enable {
		t.Fatal("a client whose expiry passed is still enabled after a poll; on a panel with no Xray no poll ever reaches the sweep, so its WireGuard peer stays connectable indefinitely")
	}
}
