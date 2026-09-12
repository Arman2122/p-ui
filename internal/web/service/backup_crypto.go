package service

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"

	"github.com/minio/sio"
	"golang.org/x/crypto/argon2"
)

const (
	backupEncryptedMagic      = "PUIENC01"
	backupEncryptedHeaderSize = len(backupEncryptedMagic) + 32
)

var ErrBackupAuthentication = errors.New("archive authentication failed: wrong passphrase, damaged or incomplete archive")

func validateBackupPassphrase(passphrase string) error {
	if len(passphrase) < 12 || len(passphrase) > 1024 {
		return errors.New("archive passphrase must contain between 12 and 1024 bytes")
	}
	return nil
}

// The version fixes bounded KDF parameters; the complete header is bound through the derived key.
func backupArchiveKey(passphrase string, header []byte) []byte {
	password := []byte(passphrase)
	defer clear(password)
	return argon2.IDKey(password, header, 3, 64*1024, 4, 32)
}

func backupCipher(key []byte) sio.Config {
	return sio.Config{MinVersion: sio.Version20, MaxVersion: sio.Version20, CipherSuites: []byte{sio.AES_GCM}, Key: key}
}

func encryptBackupArchive(dst io.Writer, src io.Reader, passphrase string) (int64, error) {
	if err := validateBackupPassphrase(passphrase); err != nil {
		return 0, err
	}
	header := make([]byte, backupEncryptedHeaderSize)
	copy(header, backupEncryptedMagic)
	if _, err := rand.Read(header[len(backupEncryptedMagic):]); err != nil {
		return 0, err
	}
	key := backupArchiveKey(passphrase, header)
	defer clear(key)
	if n, err := dst.Write(header); err != nil {
		return int64(n), err
	} else if n != len(header) {
		return int64(n), io.ErrShortWrite
	}
	n, err := sio.Encrypt(dst, src, backupCipher(key))
	return n + int64(len(header)), err
}

func decryptBackupArchive(dst io.Writer, src io.Reader, passphrase string) (int64, error) {
	if err := validateBackupPassphrase(passphrase); err != nil {
		return 0, err
	}
	header := make([]byte, backupEncryptedHeaderSize)
	if _, err := io.ReadFull(src, header); err != nil {
		return 0, ErrBackupAuthentication
	}
	if !bytes.Equal(header[:len(backupEncryptedMagic)], []byte(backupEncryptedMagic)) {
		return 0, ErrBackupAuthentication
	}
	key := backupArchiveKey(passphrase, header)
	defer clear(key)
	n, err := sio.Decrypt(dst, src, backupCipher(key))
	if err != nil {
		var invalid sio.Error
		if errors.As(err, &invalid) || errors.Is(err, io.ErrUnexpectedEOF) {
			return n, ErrBackupAuthentication
		}
		return n, fmt.Errorf("decrypt archive: %w", err)
	}
	if n == 0 {
		return n, ErrBackupAuthentication
	}
	return n, nil
}

// No PostgreSQL call receives plaintext until every authenticated frame and the final frame pass.
func authenticatedBackupUpload(file multipart.File, passphrase string) (multipart.File, func(), error) {
	header := make([]byte, len(backupEncryptedMagic))
	n, err := file.ReadAt(header, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}
	if isPgDumpHeader(header[:n]) {
		if passphrase != "" {
			return nil, nil, errors.New("this archive is unencrypted; leave its passphrase empty")
		}
		return file, func() {}, nil
	}
	if !bytes.Equal(header[:n], []byte(backupEncryptedMagic)) {
		return nil, nil, ErrBackupAuthentication
	}
	plain, err := os.CreateTemp(dumpSpoolDir(), "p-ui-authenticated-*.dump")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = plain.Close(); _ = os.Remove(plain.Name()) }
	if _, err := decryptBackupArchive(plain, file, passphrase); err != nil {
		cleanup()
		return nil, nil, err
	}
	if _, err := plain.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, nil, err
	}
	return plain, cleanup, nil
}
