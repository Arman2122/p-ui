package sub

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestAggregateTrafficByEmails_FallsBackToClientLimits(t *testing.T) {
	initSubDB(t)

	const email = "node-client@example.com"
	const totalBytes = int64(300) * 1024 * 1024 * 1024
	const expiry = int64(1893456000000)

	db := database.GetDB()
	if err := db.Create(&model.ClientRecord{
		Email:      email,
		TotalGB:    totalBytes,
		ExpiryTime: expiry,
		Enable:     true,
	}).Error; err != nil {
		t.Fatalf("seed client record: %v", err)
	}
	if err := db.Create(&xray.ClientTraffic{
		Email:      email,
		Up:         111,
		Down:       222,
		Total:      0,
		ExpiryTime: 0,
		Enable:     true,
	}).Error; err != nil {
		t.Fatalf("seed client traffic: %v", err)
	}

	var s SubService
	agg, _ := s.AggregateTrafficByEmails([]string{email})

	if agg.Up != 111 || agg.Down != 222 {
		t.Errorf("usage = up %d/down %d, want 111/222", agg.Up, agg.Down)
	}
	if agg.Total != totalBytes {
		t.Errorf("total = %d, want %d (fallback to clients table)", agg.Total, totalBytes)
	}
	if agg.ExpiryTime != expiry {
		t.Errorf("expiry = %d, want %d (fallback to clients table)", agg.ExpiryTime, expiry)
	}
}

/*
Two clients with different deadlines must not read as "never expires".

Subscription-Userinfo's expire=0 is the wire's way of saying unlimited, so
zeroing the aggregate the moment two clients disagreed told every app the
subscription runs forever. An operator watching the countdown in their client
saw no deadline at all, right up until the first client's access stopped.

The nearest deadline is the honest one: it is the first moment the subscription
materially changes. A client that genuinely never expires still wins, which is
the same "most permissive" fold Total already uses one line above. F131.
*/
func TestAggregateTrafficByEmails_FoldsToTheNearestExpiry(t *testing.T) {
	initSubDB(t)

	const soon = int64(1893456000000)  // 2030-01-01
	const later = int64(1988150400000) // 2033-01-01

	db := database.GetDB()
	for email, expiry := range map[string]int64{
		"later@example.com": later,
		"soon@example.com":  soon,
	} {
		if err := db.Create(&xray.ClientTraffic{
			Email:      email,
			Total:      1024,
			ExpiryTime: expiry,
			Enable:     true,
		}).Error; err != nil {
			t.Fatalf("seed %s: %v", email, err)
		}
	}

	var s SubService
	agg, _ := s.AggregateTrafficByEmails([]string{"later@example.com", "soon@example.com"})

	if agg.ExpiryTime != soon {
		t.Errorf("expiry = %d, want %d (the nearer of the two); 0 would tell the app the subscription never expires", agg.ExpiryTime, soon)
	}
}

// A client that never expires keeps the whole subscription unlimited, matching
// how Total folds an unlimited client one line above.
func TestAggregateTrafficByEmails_AnUnlimitedClientKeepsTheSubscriptionUnlimited(t *testing.T) {
	initSubDB(t)

	db := database.GetDB()
	for email, expiry := range map[string]int64{
		"dated@example.com":     int64(1893456000000),
		"unlimited@example.com": 0,
	} {
		if err := db.Create(&xray.ClientTraffic{
			Email:      email,
			Total:      1024,
			ExpiryTime: expiry,
			Enable:     true,
		}).Error; err != nil {
			t.Fatalf("seed %s: %v", email, err)
		}
	}

	var s SubService
	agg, _ := s.AggregateTrafficByEmails([]string{"dated@example.com", "unlimited@example.com"})

	if agg.ExpiryTime != 0 {
		t.Errorf("expiry = %d, want 0: one client never expires, so the subscription does not either", agg.ExpiryTime)
	}
}
