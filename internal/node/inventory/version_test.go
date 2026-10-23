package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

type cancelReader struct{ cancel context.CancelFunc }

func (r cancelReader) Read(data []byte) (int, error) {
	r.cancel()
	return copy(data, "binary"), nil
}

func TestExecutableFingerprintRejectsIncompleteObservations(t *testing.T) {
	data := []byte("actual running executable bytes")
	digest := sha256.Sum256(data)
	want := "sha256:" + hex.EncodeToString(digest[:])
	got, err := digestExecutable(t.Context(), bytes.NewReader(data), int64(len(data)))
	if err != nil || got != want {
		t.Fatalf("executable fingerprint=%q %v", got, err)
	}
	for name, content := range map[string][]byte{"empty": nil, "oversize": append(bytes.Clone(data), 0)} {
		t.Run(name, func(t *testing.T) {
			if got, err := digestExecutable(t.Context(), bytes.NewReader(content), int64(len(data))); !errors.Is(err, ErrInventory) || got != "" {
				t.Fatalf("incomplete fingerprint=%q %v", got, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if got, err := digestExecutable(ctx, cancelReader{cancel}, 64); !errors.Is(err, context.Canceled) || got != "" {
		t.Fatalf("canceled fingerprint=%q %v", got, err)
	}
}
