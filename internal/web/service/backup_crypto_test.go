package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupEncryptionAuthenticatesEveryFrameAndHeader(t *testing.T) {
	const passphrase = "offline random passphrase 7251"
	plain := append([]byte("PGDMP"), bytes.Repeat([]byte("managed core configuration and persisted traffic history"), 4000)...)
	var encrypted bytes.Buffer
	if _, err := encryptBackupArchive(&encrypted, bytes.NewReader(plain), passphrase); err != nil {
		t.Fatal(err)
	}
	ciphertext := encrypted.Bytes()
	var restored bytes.Buffer
	if _, err := decryptBackupArchive(&restored, bytes.NewReader(ciphertext), passphrase); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Bytes(), plain) {
		t.Fatal("restored database archive differs")
	}
	if bytes.Contains(ciphertext, []byte(passphrase)) || bytes.Contains(ciphertext, []byte("managed core configuration")) {
		t.Fatal("encrypted archive contains plaintext")
	}
	var second bytes.Buffer
	if _, err := encryptBackupArchive(&second, bytes.NewReader(plain), passphrase); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ciphertext, second.Bytes()) {
		t.Fatal("fresh archives reused randomness")
	}
	frame := (1 << 16) + 32
	cases := map[string][]byte{
		"header truncated":           ciphertext[:backupEncryptedHeaderSize-1],
		"empty ciphertext":           ciphertext[:backupEncryptedHeaderSize],
		"final tag truncated":        ciphertext[:len(ciphertext)-1],
		"whole final frames removed": ciphertext[:backupEncryptedHeaderSize+frame],
		"data appended":              append(bytes.Clone(ciphertext), 0),
	}
	for name, offset := range map[string]int{"header changed": 10, "first frame changed": backupEncryptedHeaderSize + 19, "middle frame changed": backupEncryptedHeaderSize + frame + 41, "final tag changed": len(ciphertext) - 1} {
		changed := bytes.Clone(ciphertext)
		changed[offset] ^= 1
		cases[name] = changed
	}
	reordered := bytes.Clone(ciphertext)
	copy(reordered[backupEncryptedHeaderSize:backupEncryptedHeaderSize+frame], ciphertext[backupEncryptedHeaderSize+frame:backupEncryptedHeaderSize+2*frame])
	copy(reordered[backupEncryptedHeaderSize+frame:backupEncryptedHeaderSize+2*frame], ciphertext[backupEncryptedHeaderSize:backupEncryptedHeaderSize+frame])
	cases["reordered frames"] = reordered
	for name, damaged := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decryptBackupArchive(io.Discard, bytes.NewReader(damaged), passphrase); !errors.Is(err, ErrBackupAuthentication) {
				t.Fatalf("damaged archive accepted: %v", err)
			}
		})
	}
	if _, err := decryptBackupArchive(io.Discard, bytes.NewReader(ciphertext), "wrong offline passphrase"); !errors.Is(err, ErrBackupAuthentication) {
		t.Fatalf("wrong password accepted: %v", err)
	}
}

func TestBackupUploadAuthenticatesBeforeRestoring(t *testing.T) {
	const passphrase = "offline secret words for test"
	var encrypted bytes.Buffer
	if _, err := encryptBackupArchive(&encrypted, strings.NewReader("PGDMP-original"), passphrase); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(t.TempDir(), "encrypted-*.dump.pui")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(encrypted.Bytes()); err != nil {
		t.Fatal(err)
	}
	plain, cleanup, err := authenticatedBackupUpload(file, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(plain)
	if err != nil || string(body) != "PGDMP-original" {
		t.Fatalf("authenticated contents = %q, %v", body, err)
	}
	cleanup()
	if err := file.Truncate(int64(encrypted.Len() - 1)); err != nil {
		t.Fatal(err)
	}
	s := &BackupService{Directory: t.TempDir()}
	row, err := s.RestoreUploadWithPassphrase(file, "fixture.dump.pui", passphrase)
	if !errors.Is(err, ErrBackupAuthentication) || row.Status != "failed" {
		t.Fatalf("restore did not stop at authentication: %+v, %v", row, err)
	}
	data, err := os.ReadFile(filepath.Join(s.Directory, row.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(passphrase)) {
		t.Fatal("history persisted the passphrase")
	}
}

func TestBackupTelegramDeliversStoredArchiveAndRecordsFailures(t *testing.T) {
	s := &BackupService{Directory: t.TempDir()}
	const id = "20260912T030000.000000000"
	row := BackupRecordView{ID: id, Operation: "backup", Status: "completed", Encrypted: true, Filename: id + ".dump.pui"}
	if err := s.writeRecord(row); err != nil {
		t.Fatal(err)
	}
	content := []byte("exact stored ciphertext")
	if err := os.WriteFile(s.archivePath(id, true), content, 0o600); err != nil {
		t.Fatal(err)
	}
	previous := backupSender()
	t.Cleanup(func() { SetBackupArchiveSender(previous) })
	var calls int
	SetBackupArchiveSender(func(_ context.Context, file *os.File, name string) error {
		calls++
		body, err := io.ReadAll(file)
		if err != nil || !bytes.Equal(body, content) || name != row.Filename {
			t.Fatalf("wrong Telegram source: %s, %q, %v", name, body, err)
		}
		return errors.New("a transport error with a sensitive token that must not be persisted")
	})
	for _, invalid := range []string{"../secret", "20260913T030000.000000000"} {
		if _, err := s.SendToTelegram(context.Background(), invalid); err == nil {
			t.Fatalf("accepted source %q", invalid)
		}
	}
	if calls != 0 {
		t.Fatal("invalid source reached Telegram")
	}
	result, err := s.SendToTelegram(context.Background(), id)
	if err == nil || result.Status != "completed" || result.TelegramStatus != "failed" || calls != 1 {
		t.Fatalf("delivery result = %+v, %v, calls %d", result, err, calls)
	}
	if strings.Contains(result.TelegramError, "sensitive token") {
		t.Fatal("delivery history leaked transport credentials")
	}
}

func TestBackupScheduleRefusesRequestOnlyEncryption(t *testing.T) {
	err := validateBackupSchedule(BackupSchedule{Enabled: true, Cadence: "daily", Keep: 14, Encrypt: true})
	if err == nil || err.Error() != "scheduled encryption requires an external key provider; request-only passphrases cannot be saved for unattended jobs" {
		t.Fatalf("impossible scheduled encryption = %v", err)
	}
}
