package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetLegacyGroupQuotaRejectsDeletedParentPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	group := model.ClientGroup{Name: "deleted-before-guard"}
	if err := f.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.GroupQuota{Name: group.Name, QuotaBytes: 100}).Error; err != nil {
		t.Fatal(err)
	}
	callback := "legacy-group-deleted-" + uuid.NewString()
	deleted := false
	quotaWrites := 0
	var deleteErr error
	if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		lock, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if deleted || tx.Statement.Table != "client_groups" || !ok || lock.Strength != "UPDATE" {
			return
		}
		deleted = true
		deleteErr = f.db.Delete(&group).Error
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
	if err := f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "group_quota" {
			quotaWrites++
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Create().Remove(callback) })
	err := (&ClientService{}).SetGroupQuota(GroupQuotaRequest{Name: group.Name, QuotaBytes: 200})
	if !deleted || deleteErr != nil || !errors.Is(err, gorm.ErrRecordNotFound) || quotaWrites != 0 {
		t.Fatalf("deleted group was not rejected before quota write: deleted=%v delete=%v writes=%d edit=%v", deleted, deleteErr, quotaWrites, err)
	}
	var count int64
	if err := f.db.Model(&model.GroupQuota{}).Where("name = ?", group.Name).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("deleted-parent refusal recreated quota: %d %v", count, err)
	}
}

func TestFleetLegacyDerivedGroupSerializesParentNamePostgres(t *testing.T) {
	f := fleetFixture(t, false)
	name := "derived-group-without-parent"
	if err := f.db.Model(&f.client).Update("group_name", name).Error; err != nil {
		t.Fatal(err)
	}
	tx := f.db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	if err := lockLegacyGroupMutationTx(tx, nil, []string{name}, ""); err != nil {
		t.Fatal(err)
	}
	probeErr := f.db.Transaction(func(probe *gorm.DB) error {
		if err := probe.Exec("SET LOCAL lock_timeout='100ms'").Error; err != nil {
			return err
		}
		return probe.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ClientGroup{Name: name}).Error
	})
	var state interface{ SQLState() string }
	if !errors.As(probeErr, &state) || state.SQLState() != "55P03" {
		t.Fatalf("derived group did not serialize its parent name: %v", probeErr)
	}
	var group model.ClientGroup
	if err := tx.First(&group, "name = ?", name).Error; err != nil || group.ResetUp != 0 || group.ResetDown != 0 {
		t.Fatalf("derived serialization parent changed baseline: %v", err)
	}
}
