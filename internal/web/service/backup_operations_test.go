package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestBackupSchedulePersistsAndRejectsInvalidChanges(t *testing.T) {
	initTestDB(t)
	s := &SettingService{}
	want := BackupSchedule{Enabled: true, Cadence: "weekly", Keep: 3}
	if err := s.SetBackupSchedule(want); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBackupSchedule(BackupSchedule{Cadence: "hourly", Keep: 0}); err == nil || err.Error() != "backup retention must be between 1 and 365" {
		t.Fatalf("invalid retention: %v", err)
	}
	got, err := (&SettingService{}).GetBackupSchedule()
	if err != nil || got != want {
		t.Fatalf("schedule = %+v, %v; want %+v", got, err, want)
	}
	for _, tc := range []struct{ cadence, now, want string }{
		{"hourly", "2026-09-12T12:59:59Z", "2026-09-12T13:00:00Z"},
		{"daily", "2026-09-12T03:00:00Z", "2026-09-13T03:00:00Z"},
		{"weekly", "2026-09-13T03:00:00Z", "2026-09-20T03:00:00Z"},
		{"daily", "2026-09-12T06:00:00+03:30", "2026-09-12T03:00:00Z"},
	} {
		t.Run(tc.cadence+tc.now, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			if got := nextBackupRun(now, tc.cadence).Format(time.RFC3339); got != tc.want {
				t.Fatalf("next = %s, want %s", got, tc.want)
			}
		})
	}
	t.Setenv("PATH", t.TempDir())
	if err := s.SetBackupSchedule(BackupSchedule{Enabled: true, Cadence: "hourly", Keep: 2}); err != nil {
		t.Fatal(err)
	}
	backups := &BackupService{Directory: t.TempDir()}
	due := time.Now().UTC().Truncate(time.Hour)
	if err := backups.RunScheduled(due); err == nil {
		t.Fatal("scheduled backup hid the missing pg_dump executable")
	}
	if err := backups.RunScheduled(due); err != nil {
		t.Fatalf("retried failed scheduled slot: %v", err)
	}
	status, err := backups.Status()
	if err != nil || len(status.Records) != 1 || status.Records[0].Status != "failed" || status.Records[0].Source != "scheduled" {
		t.Fatalf("scheduled history = %+v, %v", status, err)
	}
}

func TestBackupRestoreFailureIsDurableAndKeepsDatabase(t *testing.T) {
	initTestDB(t)
	if err := (&SettingService{}).saveSetting("restore-sentinel", "untouched"); err != nil {
		t.Fatal(err)
	}
	s := &BackupService{Directory: t.TempDir()}
	file, err := os.CreateTemp(t.TempDir(), "bad-*.dump")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("not a dump"); err != nil {
		t.Fatal(err)
	}
	row, err := s.RestoreUpload(file, "bad.dump")
	if err == nil || row.Status != "failed" || row.Operation != "restore" {
		t.Fatalf("restore = %+v, %v", row, err)
	}
	status, err := (&BackupService{Directory: s.Directory}).Status()
	if err != nil || len(status.Records) != 1 {
		t.Fatalf("history = %+v, %v", status, err)
	}
	if got := status.Records[0]; got.ID != row.ID || got.Error != row.Error || got.FinishedAt == 0 {
		t.Fatalf("persisted result = %+v, want %+v", got, row)
	}
	var setting model.Setting
	if err := database.GetDB().Where("key = ?", "restore-sentinel").First(&setting).Error; err != nil || setting.Value != "untouched" {
		t.Fatalf("database changed: %+v, %v", setting, err)
	}
}

func TestBackupHistoryRecoversInterruptedOperationAndPrunesArchives(t *testing.T) {
	initTestDB(t)
	if err := (&SettingService{}).SetBackupSchedule(BackupSchedule{Cadence: "daily", Keep: 1}); err != nil {
		t.Fatal(err)
	}
	s := &BackupService{Directory: t.TempDir()}
	for i, id := range []string{"20260910T030000.000000000", "20260911T030000.000000000", "20260912T030000.000000000"} {
		row := BackupRecordView{ID: id, Operation: "backup", Status: "completed", StartedAt: int64(i + 1)}
		if i == 2 {
			row.Status = "running"
		}
		if err := s.writeRecord(row); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if err := os.WriteFile(filepath.Join(s.Directory, id+".dump"), []byte("PGDMP"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.prune(); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Records) != 3 || status.Records[0].Status != "interrupted" || !status.Records[1].Available || status.Records[2].Available {
		t.Fatalf("history = %+v", status.Records)
	}
	if _, err := s.Open("../outside"); err == nil || err.Error() != "invalid backup id" {
		t.Fatalf("traversal = %v", err)
	}
}

func TestBackupOperationsExcludeConcurrentRestore(t *testing.T) {
	s := &BackupService{Directory: t.TempDir()}
	var concurrentErr error
	_, err := s.perform("backup", "manual", "", func(_ string) (int64, error) {
		_, concurrentErr = s.Restore("20260912T030000.000000000")
		return 0, errors.New("dump failed")
	})
	if err == nil || err.Error() != "dump failed" {
		t.Fatalf("backup = %v", err)
	}
	if concurrentErr == nil || concurrentErr.Error() != "a backup or restore is already running" {
		t.Fatalf("concurrent restore = %v", concurrentErr)
	}
}

func TestBackupOpenRefusesAnInterruptedArchive(t *testing.T) {
	s := &BackupService{Directory: t.TempDir()}
	const id = "20260912T030000.000000000"
	if err := s.writeRecord(BackupRecordView{ID: id, Operation: "backup", Status: "interrupted"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Directory, id+".dump"), []byte("partial archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := s.Open(id)
	if file != nil {
		_ = file.Close()
	}
	if err == nil || err.Error() != "backup did not complete successfully" {
		t.Fatalf("partial archive = %v", err)
	}
}
