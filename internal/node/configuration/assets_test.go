package configuration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func fileFixture(id string, data []byte) FileAsset {
	digest := sha256.Sum256(data)
	return FileAsset{ID: id, SHA256: hex.EncodeToString(digest[:]), Data: data}
}

func TestDecodeRejectsInvalidOrUnboundedFileAssets(t *testing.T) {
	for _, mode := range []string{"hash", "duplicate", "id", "count", "file size", "bundle size"} {
		t.Run(mode, func(t *testing.T) {
			bundle := Bundle{Version: 1, Runtimes: []Runtime{{ID: "ssh", Core: "ssh", Configuration: json.RawMessage(`{}`), FileAssets: []FileAsset{fileFixture("key", []byte("key"))}}}}
			files := &bundle.Runtimes[0].FileAssets
			switch mode {
			case "hash":
				(*files)[0].SHA256 = hex.EncodeToString(bytes.Repeat([]byte{1}, 32))
			case "duplicate":
				*files = append(*files, (*files)[0])
			case "id":
				(*files)[0].ID = "../escape"
			case "count":
				for i := 0; i < MaxRuntimeFileAssets; i++ {
					*files = append(*files, fileFixture(fmt.Sprintf("key-%d", i), []byte("key")))
				}
			case "file size":
				*files = []FileAsset{fileFixture("key", make([]byte, MaxFileAssetBytes+1))}
			case "bundle size":
				*files = []FileAsset{fileFixture("key", make([]byte, MaxFileAssetBytes)), fileFixture("cert", make([]byte, MaxFileAssetBytes))}
				bundle.Runtimes = append(bundle.Runtimes, Runtime{ID: "ssh-extra", Core: "ssh", Configuration: json.RawMessage(`{}`), FileAssets: []FileAsset{fileFixture("extra", []byte{1})}})
			}
			payload, err := json.Marshal(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(payload); !errors.Is(err, ErrConfiguration) {
				t.Fatalf("accepted %s: %v", mode, err)
			}
		})
	}
}
