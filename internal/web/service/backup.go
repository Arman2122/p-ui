package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/database"
)

type BackupSchedule struct {
	Enabled        bool   `json:"enabled" form:"enabled" example:"false"`
	Cadence        string `json:"cadence" form:"cadence" example:"daily"`
	Keep           int    `json:"keep" form:"keep" example:"14"`
	Encrypt        bool   `json:"encrypt" form:"encrypt" example:"false"`
	SendToTelegram bool   `json:"sendToTelegram" form:"sendToTelegram" example:"false"`
}

type BackupCreateOptions struct {
	Encrypt        bool   `json:"encrypt" form:"encrypt" example:"true"`
	Passphrase     string `json:"passphrase" form:"passphrase" example:"four random words kept offline"`
	SendToTelegram bool   `json:"sendToTelegram" form:"sendToTelegram" example:"false"`
}

type BackupRecordView struct {
	ID             string `json:"id" example:"20260912T030000.000000000"`
	Operation      string `json:"operation" example:"backup"`
	Source         string `json:"source" example:"manual"`
	Status         string `json:"status" example:"completed"`
	StartedAt      int64  `json:"startedAt" example:"1789182000000"`
	FinishedAt     int64  `json:"finishedAt" example:"1789182010000"`
	Size           int64  `json:"size" example:"4812000"`
	Filename       string `json:"filename" example:"20260912T030000.000000000.dump"`
	Error          string `json:"error" example:""`
	Available      bool   `json:"available" example:"true"`
	Encrypted      bool   `json:"encrypted" example:"false"`
	TelegramStatus string `json:"telegramStatus" example:"not_requested"`
	TelegramError  string `json:"telegramError" example:""`
}

type BackupStatusView struct {
	Schedule           BackupSchedule     `json:"schedule"`
	Directory          string             `json:"directory" example:"/usr/local/x-ui/bin/backups"`
	NextRun            int64              `json:"nextRun" example:"1789268400000"`
	Busy               bool               `json:"busy" example:"false"`
	Records            []BackupRecordView `json:"records"`
	TelegramAvailable  bool               `json:"telegramAvailable" example:"true"`
	ManagedCoreConfigs bool               `json:"managedCoreConfigs" example:"true"`
	TrafficHistory     bool               `json:"trafficHistory" example:"true"`
}

type BackupService struct {
	Directory string
	server    ServerService
}

var (
	backupOperation sync.Mutex
	backupFiles     sync.Mutex
	backupIDPattern = regexp.MustCompile(`^\d{8}T\d{6}\.\d{9}$`)
)

func (s *SettingService) GetBackupSchedule() (BackupSchedule, error) {
	result := BackupSchedule{Cadence: "daily", Keep: 14}
	setting, err := s.getSetting("backupSchedule")
	if database.IsNotFound(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(setting.Value), &result); err != nil {
		return result, err
	}
	return result, validateBackupSchedule(result)
}

func validateBackupSchedule(value BackupSchedule) error {
	if value.Encrypt {
		return errors.New("scheduled encryption requires an external key provider; request-only passphrases cannot be saved for unattended jobs")
	}
	if value.Cadence != "hourly" && value.Cadence != "daily" && value.Cadence != "weekly" {
		return errors.New("backup cadence must be hourly, daily or weekly")
	}
	if value.Keep < 1 || value.Keep > 365 {
		return errors.New("backup retention must be between 1 and 365")
	}
	return nil
}

func (s *SettingService) SetBackupSchedule(value BackupSchedule) error {
	if err := validateBackupSchedule(value); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.saveSetting("backupSchedule", string(data))
}

func nextBackupRun(now time.Time, cadence string) time.Time {
	now = now.UTC()
	if cadence == "hourly" {
		return now.Truncate(time.Hour).Add(time.Hour)
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	if cadence == "weekly" {
		for next.Weekday() != time.Sunday {
			next = next.AddDate(0, 0, 1)
		}
	}
	return next
}

func (s *BackupService) directory() string {
	if s.Directory != "" {
		return s.Directory
	}
	return filepath.Join(config.GetBinFolderPath(), "backups")
}

func (s *BackupService) writeRecord(record BackupRecordView) error {
	if err := os.MkdirAll(s.directory(), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.directory(), ".record-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(s.directory(), record.ID+".json"))
}

func (s *BackupService) records() ([]BackupRecordView, error) {
	records := []BackupRecordView{}
	entries, err := os.ReadDir(s.directory())
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-5]
		if !backupIDPattern.MatchString(id) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.directory(), entry.Name()))
		if err != nil {
			return nil, err
		}
		var row BackupRecordView
		if err := json.Unmarshal(data, &row); err != nil {
			return nil, err
		}
		if row.ID != id {
			return nil, errors.New("backup record identifier mismatch")
		}
		info, err := os.Lstat(s.archivePath(id, row.Encrypted))
		row.Available = err == nil && info.Mode().IsRegular() && row.Operation == "backup" && row.Status == "completed"
		records = append(records, row)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].StartedAt > records[j].StartedAt })
	return records, nil
}

func (s *BackupService) Status() (BackupStatusView, error) {
	schedule, err := (&SettingService{}).GetBackupSchedule()
	if err != nil {
		return BackupStatusView{}, err
	}
	busy := !backupOperation.TryLock()
	if !busy {
		defer backupOperation.Unlock()
	}
	backupFiles.Lock()
	defer backupFiles.Unlock()
	rows, err := s.records()
	if err != nil {
		return BackupStatusView{}, err
	}
	if !busy {
		for i := range rows {
			if rows[i].TelegramStatus == "sending" {
				rows[i].TelegramStatus, rows[i].TelegramError = "failed", "Telegram delivery was interrupted; check administrator chats before resending"
				if err := s.writeRecord(rows[i]); err != nil {
					return BackupStatusView{}, err
				}
			}
			if rows[i].Status != "running" {
				continue
			}
			rows[i].Status, rows[i].Error = "interrupted", "panel stopped before the operation completed; inspect the database before retrying"
			rows[i].FinishedAt = time.Now().UnixMilli()
			if err := s.writeRecord(rows[i]); err != nil {
				return BackupStatusView{}, err
			}
		}
	}
	view := BackupStatusView{Schedule: schedule, Directory: s.directory(), Busy: busy, Records: rows, ManagedCoreConfigs: true, TrafficHistory: true, TelegramAvailable: backupSender() != nil}
	if schedule.Enabled {
		view.NextRun = nextBackupRun(time.Now(), schedule.Cadence).UnixMilli()
	}
	return view, nil
}

func (s *BackupService) perform(operation, source, filename string, action func(string) (int64, error)) (BackupRecordView, error) {
	return s.performWithFormat(operation, source, filename, action, false)
}

func (s *BackupService) performWithFormat(operation, source, filename string, action func(string) (int64, error), encrypted bool) (BackupRecordView, error) {
	if !backupOperation.TryLock() {
		return BackupRecordView{}, errors.New("a backup or restore is already running")
	}
	defer backupOperation.Unlock()
	return s.performLocked(operation, source, filename, action, encrypted)
}

func (s *BackupService) performLocked(operation, source, filename string, action func(string) (int64, error), encrypted bool) (BackupRecordView, error) {
	now := time.Now().UTC()
	row := BackupRecordView{ID: now.Format("20060102T150405.000000000"), Operation: operation, Source: source, Filename: filename, Status: "running", StartedAt: now.UnixMilli(), Encrypted: encrypted, TelegramStatus: "not_requested"}
	if operation == "backup" {
		row.Filename = filepath.Base(s.archivePath(row.ID, encrypted))
	}
	backupFiles.Lock()
	err := s.writeRecord(row)
	backupFiles.Unlock()
	if err != nil {
		return row, err
	}
	row.Size, err = action(row.ID)
	row.Status, row.FinishedAt = "completed", time.Now().UnixMilli()
	if err != nil {
		row.Status, row.Error = "failed", err.Error()
	}
	row.Available = operation == "backup" && err == nil
	backupFiles.Lock()
	writeErr := s.writeRecord(row)
	backupFiles.Unlock()
	return row, errors.Join(err, writeErr)
}

func (s *BackupService) createArchive(id string) (int64, error) {
	return s.createArchiveWithOptions(id, BackupCreateOptions{})
}

func (s *BackupService) archivePath(id string, encrypted bool) string {
	extension := ".dump"
	if encrypted {
		extension += ".pui"
	}
	return filepath.Join(s.directory(), id+extension)
}

func (s *BackupService) createArchiveWithOptions(id string, options BackupCreateOptions) (int64, error) {
	dump, err := s.server.GetDb()
	if err != nil {
		return 0, err
	}
	defer dump.Close()
	if dump.Size == 0 {
		return 0, errors.New("pg_dump produced an empty archive")
	}
	path := s.archivePath(id, options.Encrypt)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	var size int64
	var copyErr error
	if options.Encrypt {
		size, copyErr = encryptBackupArchive(file, dump, options.Passphrase)
	} else {
		size, copyErr = io.Copy(file, dump)
	}
	err = errors.Join(copyErr, file.Sync(), file.Close())
	if err != nil {
		_ = os.Remove(path)
	}
	return size, err
}

func (s *BackupService) Create(source string) (BackupRecordView, error) {
	return s.CreateWithOptions(context.Background(), source, BackupCreateOptions{})
}

func (s *BackupService) CreateWithOptions(ctx context.Context, source string, options BackupCreateOptions) (BackupRecordView, error) {
	if options.Encrypt {
		if err := validateBackupPassphrase(options.Passphrase); err != nil {
			return BackupRecordView{}, err
		}
	} else if options.Passphrase != "" {
		return BackupRecordView{}, errors.New("enable archive encryption before supplying a passphrase")
	}
	if options.SendToTelegram && backupSender() == nil {
		return BackupRecordView{}, errors.New("Telegram archive delivery is not configured")
	}
	if !backupOperation.TryLock() {
		return BackupRecordView{}, errors.New("a backup or restore is already running")
	}
	defer backupOperation.Unlock()
	row, err := s.performLocked("backup", source, "", func(id string) (int64, error) { return s.createArchiveWithOptions(id, options) }, options.Encrypt)
	if err != nil {
		return row, err
	}
	if options.SendToTelegram {
		row, err = s.deliverArchive(ctx, row.ID)
	}
	return row, errors.Join(err, s.prune())
}

func (s *BackupService) Open(id string) (*os.File, error) {
	if !backupIDPattern.MatchString(id) {
		return nil, errors.New("invalid backup id")
	}
	backupFiles.Lock()
	defer backupFiles.Unlock()
	data, err := os.ReadFile(filepath.Join(s.directory(), id+".json"))
	if err != nil {
		return nil, err
	}
	var record BackupRecordView
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if record.ID != id || record.Operation != "backup" || record.Status != "completed" {
		return nil, errors.New("backup did not complete successfully")
	}
	path := s.archivePath(id, record.Encrypted)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("backup is not a regular file")
	}
	return os.Open(path)
}

func (s *BackupService) Restore(id string) (BackupRecordView, error) {
	return s.RestoreWithPassphrase(id, "")
}

func (s *BackupService) RestoreWithPassphrase(id, passphrase string) (BackupRecordView, error) {
	if !backupOperation.TryLock() {
		return BackupRecordView{}, errors.New("a backup or restore is already running")
	}
	defer backupOperation.Unlock()
	file, err := s.Open(id)
	if err != nil {
		return BackupRecordView{}, err
	}
	defer file.Close()
	return s.performLocked("restore", "snapshot", filepath.Base(file.Name()), func(_ string) (int64, error) { return 0, s.restoreArchive(file, passphrase) }, filepath.Ext(file.Name()) == ".pui")
}

func (s *BackupService) RestoreUpload(file multipart.File, name string) (BackupRecordView, error) {
	return s.RestoreUploadWithPassphrase(file, name, "")
}

func (s *BackupService) RestoreUploadWithPassphrase(file multipart.File, name, passphrase string) (BackupRecordView, error) {
	header := make([]byte, len(backupEncryptedMagic))
	n, _ := file.ReadAt(header, 0)
	return s.performWithFormat("restore", "upload", filepath.Base(name), func(_ string) (int64, error) { return 0, s.restoreArchive(file, passphrase) }, string(header[:n]) == backupEncryptedMagic)
}

func (s *BackupService) restoreArchive(file multipart.File, passphrase string) error {
	plain, cleanup, err := authenticatedBackupUpload(file, passphrase)
	if err != nil {
		return err
	}
	defer cleanup()
	return s.server.ImportDB(plain)
}

func (s *BackupService) prune() error {
	schedule, err := (&SettingService{}).GetBackupSchedule()
	if err != nil {
		return err
	}
	backupFiles.Lock()
	defer backupFiles.Unlock()
	rows, err := s.records()
	if err != nil {
		return err
	}
	kept := 0
	for i, row := range rows {
		if row.Available {
			kept++
			if kept > schedule.Keep {
				if err := os.Remove(s.archivePath(row.ID, row.Encrypted)); err != nil {
					return err
				}
			}
		}
		if i >= 500 && !row.Available && row.Status != "running" {
			if err := os.Remove(filepath.Join(s.directory(), row.ID+".json")); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *BackupService) RunScheduled(now time.Time) error {
	if !backupOperation.TryLock() {
		return nil
	}
	defer backupOperation.Unlock()
	schedule, err := (&SettingService{}).GetBackupSchedule()
	if err != nil || !schedule.Enabled {
		return err
	}
	next := nextBackupRun(now.Add(-time.Minute), schedule.Cadence)
	if next.After(now) {
		return nil
	}
	backupFiles.Lock()
	rows, err := s.records()
	backupFiles.Unlock()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Source == "scheduled" && row.StartedAt >= next.UnixMilli() {
			return nil
		}
	}
	row, err := s.performLocked("backup", "scheduled", "", s.createArchive, false)
	if err != nil {
		return fmt.Errorf("scheduled backup: %w", err)
	}
	var deliveryErr error
	if schedule.SendToTelegram {
		_, deliveryErr = s.deliverArchive(context.Background(), row.ID)
	}
	return errors.Join(deliveryErr, s.prune())
}
