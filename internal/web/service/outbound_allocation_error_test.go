package service

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestOutboundSubscriptionCreatePropagatesAllocationDatabaseFailures(t *testing.T) {
	setupSettingTestDB(t)
	db := database.GetDB()
	const callback = "test:fail_outbound_subscription_query"
	errInjected := errors.New("injected outbound subscription query failure")
	if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "outbound_subscriptions" {
			tx.AddError(errInjected)
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callback); err != nil {
			t.Errorf("remove query callback: %v", err)
		}
	})

	for _, tc := range []struct {
		name      string
		tagPrefix string
		operation string
	}{
		{name: "default prefix query", tagPrefix: "", operation: "prefix allocation"},
		{name: "priority count query", tagPrefix: "custom-", operation: "priority allocation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created, err := (&OutboundSubscriptionService{}).Create("test", "https://1.1.1.1/sub", tc.tagPrefix, true, 600, false, false, false)
			if !errors.Is(err, errInjected) {
				t.Fatalf("Create error = %v, want injected %s query failure", err, tc.operation)
			}
			if created != nil {
				t.Fatalf("Create returned row %+v after %s query failure", created, tc.operation)
			}
		})
	}
}

func TestOutboundSubscriptionUpdatePropagatesPrefixQueryFailureWithoutMutation(t *testing.T) {
	setupSettingTestDB(t)
	db := database.GetDB()
	original := &model.OutboundSubscription{
		Remark: "before", Url: "https://1.1.1.1/original", TagPrefix: "custom-",
		Enabled: true, UpdateInterval: 600,
	}
	if err := db.Create(original).Error; err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	errInjected := errors.New("injected update prefix query failure")
	queryCount := 0
	const callback = "test:fail_update_prefix_query"
	if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Table != "outbound_subscriptions" {
			return
		}
		queryCount++
		if queryCount == 2 {
			tx.AddError(errInjected)
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callback); err != nil {
			t.Errorf("remove query callback: %v", err)
		}
	})

	err := (&OutboundSubscriptionService{}).Update(
		original.Id, "after", "https://1.1.1.1/changed", "", false, 1200, false, false, false,
	)
	if !errors.Is(err, errInjected) {
		t.Fatalf("Update error = %v, want injected prefix query failure", err)
	}
	if queryCount != 2 {
		t.Fatalf("outbound subscription queries = %d, want Get plus prefix allocation", queryCount)
	}

	var got model.OutboundSubscription
	if err := db.First(&got, original.Id).Error; err != nil {
		t.Fatalf("reload subscription: %v", err)
	}
	if got.Remark != original.Remark || got.Url != original.Url || got.TagPrefix != original.TagPrefix ||
		got.Enabled != original.Enabled || got.UpdateInterval != original.UpdateInterval {
		t.Fatalf("subscription changed after failed allocation: got %+v, want %+v", got, *original)
	}
}
