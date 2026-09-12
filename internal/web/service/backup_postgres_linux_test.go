//go:build linux

package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestBackupEncryptedPostgresRoundTrip(t *testing.T) {
	if os.Getenv("PUI_BACKUP_E2E") != "1" || os.Getenv("PUI_BACKUP_E2E_PRIVATE_SPOOL") != "1" {
		t.Skip("requires an isolated whole PostgreSQL database and private /var/tmp for real restore")
	}
	for _, binary := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	if err := database.InitDB(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })
	const marker = "backup-e2e-core-history"
	const passphrase = "offline random words for backup e2e"
	const coreSettings = `{"listen":"0.0.0.0","port":9443,"congestion_control":"bbr"}`
	t.Cleanup(func() {
		_ = database.GetDB().Where("key = ?", marker).Delete(&model.Setting{}).Error
		_ = database.GetDB().Where("tag = ?", marker).Delete(&model.Inbound{}).Error
		_ = database.GetDB().Where("email = ?", marker).Delete(&model.ClientTrafficSample{}).Error
		_ = database.GetDB().Where("email = ?", marker).Delete(&model.ClientRecord{}).Error
	})
	if err := database.GetDB().Create(&model.ClientRecord{Email: marker}).Error; err != nil {
		t.Fatal(err)
	}
	settings := &SettingService{}
	if err := settings.saveSetting(marker, "before snapshot"); err != nil {
		t.Fatal(err)
	}
	inbound := model.Inbound{Protocol: "tuic", Tag: marker, Remark: marker, Settings: coreSettings, Port: 9443, Enable: false}
	if err := database.GetDB().Create(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	history := model.ClientTrafficSample{Email: marker, T: time.Now().Unix(), Up: 123456, Down: 789012}
	if err := database.GetDB().Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	s := &BackupService{Directory: t.TempDir()}
	row, err := s.CreateWithOptions(context.Background(), "manual", BackupCreateOptions{Encrypt: true, Passphrase: passphrase})
	if err != nil {
		t.Fatal(err)
	}
	if !row.Encrypted || !row.Available || !strings.HasSuffix(row.Filename, ".dump.pui") {
		t.Fatalf("encrypted snapshot = %+v", row)
	}
	unencrypted, err := filepath.Glob(filepath.Join(s.Directory, "*.dump"))
	if err != nil || len(unencrypted) != 0 {
		t.Fatalf("plaintext archives retained: %v, %v", unencrypted, err)
	}
	if err := settings.saveSetting(marker, "after snapshot"); err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Model(&inbound).Update("settings", `{"changed":true}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Model(&history).Update("down", 999).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreWithPassphrase(row.ID, "a wrong secret passphrase"); !errors.Is(err, ErrBackupAuthentication) {
		t.Fatalf("wrong password did not fail before restore: %v", err)
	}
	unchanged, err := settings.getSetting(marker)
	if err != nil || unchanged.Value != "after snapshot" {
		t.Fatalf("authentication failure changed DB: %+v, %v", unchanged, err)
	}
	result, err := s.RestoreWithPassphrase(row.ID, passphrase)
	if err != nil && !strings.HasPrefix(err.Error(), "Restored DB but failed to start Xray:") {
		t.Fatalf("real pg_restore failed: %v", err)
	}
	if err != nil {
		t.Log("PostgreSQL restore completed; isolated fixture has no Xray binary, correctly reported as a failed core restart")
	}
	restored, err := settings.getSetting(marker)
	if err != nil || restored.Value != "before snapshot" {
		t.Fatalf("settings not restored: %+v, %v", restored, err)
	}
	var gotInbound model.Inbound
	if err := database.GetDB().First(&gotInbound, inbound.Id).Error; err != nil || gotInbound.Settings != coreSettings {
		t.Fatalf("managed core inputs not restored: %+v, %v", gotInbound, err)
	}
	var gotHistory model.ClientTrafficSample
	if err := database.GetDB().Where("email = ? AND t = ?", history.Email, history.T).First(&gotHistory).Error; err != nil || gotHistory.Up != 123456 || gotHistory.Down != 789012 {
		t.Fatalf("traffic history not restored: %+v, %v", gotHistory, err)
	}
	manifest, err := os.ReadFile(filepath.Join(s.Directory, result.ID+".json"))
	if err != nil || bytes.Contains(manifest, []byte(passphrase)) {
		t.Fatalf("restore history missing or contains secret: %v", err)
	}
	if result.ID == row.ID {
		t.Fatal("restore history was overwritten by database restore")
	}
}
