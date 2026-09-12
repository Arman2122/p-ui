package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type BackupArchiveSender func(context.Context, *os.File, string) error

var backupDelivery struct {
	sync.RWMutex
	send BackupArchiveSender
}

func SetBackupArchiveSender(sender BackupArchiveSender) {
	backupDelivery.Lock()
	defer backupDelivery.Unlock()
	backupDelivery.send = sender
}

func backupSender() BackupArchiveSender {
	backupDelivery.RLock()
	defer backupDelivery.RUnlock()
	return backupDelivery.send
}

func (s *BackupService) SendToTelegram(ctx context.Context, id string) (BackupRecordView, error) {
	if !backupOperation.TryLock() {
		return BackupRecordView{}, errors.New("a backup or restore is already running")
	}
	defer backupOperation.Unlock()
	return s.deliverArchive(ctx, id)
}

func (s *BackupService) deliverArchive(ctx context.Context, id string) (BackupRecordView, error) {
	file, err := s.Open(id)
	if err != nil {
		return BackupRecordView{}, err
	}
	defer file.Close()
	data, err := os.ReadFile(filepath.Join(s.directory(), id+".json"))
	if err != nil {
		return BackupRecordView{}, err
	}
	var row BackupRecordView
	if err := json.Unmarshal(data, &row); err != nil {
		return row, err
	}
	row.TelegramStatus, row.TelegramError = "sending", ""
	backupFiles.Lock()
	err = s.writeRecord(row)
	backupFiles.Unlock()
	if err != nil {
		return row, err
	}
	sender := backupSender()
	if sender == nil {
		err = errors.New("Telegram archive delivery is not configured")
	} else {
		err = sender(ctx, file, filepath.Base(file.Name()))
	}
	row.TelegramStatus = "sent"
	if err != nil {
		row.TelegramStatus = "failed"
		row.TelegramError = "archive saved locally, but Telegram delivery failed; check the bot and configured administrator chats"
		err = errors.New(row.TelegramError)
	}
	backupFiles.Lock()
	writeErr := s.writeRecord(row)
	backupFiles.Unlock()
	return row, errors.Join(err, writeErr)
}
