package tgbot

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	tu "github.com/mymmrac/telego/telegoutil"
)

// Only this bot's configured administrator chats receive the exact stored archive.
func SendBackupArchive(ctx context.Context, file *os.File, filename string) error {
	tgBotMutex.Lock()
	sender, running := bot, isRunning
	chats := append([]int64(nil), adminIds...)
	tgBotMutex.Unlock()
	if !running || sender == nil || len(chats) == 0 {
		return errors.New("Telegram bot or administrator chats unavailable")
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= 0 {
		return errors.New("cannot send an empty backup")
	}
	var failed bool
	for _, chat := range chats {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		deadline, cancel := context.WithTimeout(ctx, 5*time.Minute)
		_, err := sender.SendDocument(deadline, tu.Document(tu.ID(chat), tu.FileFromReader(file, filename)))
		cancel()
		if err != nil {
			failed = true
		}
	}
	if failed {
		return errors.New("Telegram failed to deliver the archive to one or more administrator chats")
	}
	return nil
}
