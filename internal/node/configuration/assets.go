package configuration

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
)

const (
	MaxRuntimeFileAssets = 256
	MaxFileAssetBytes    = 8 << 20
	MaxBundleFileBytes   = 16 << 20
	AssetManifestHeader  = "X-Pui-Assets-Sha256"
	assetManifestDomain  = "penhoon/node/file-assets/v1\x00"
)

var assetID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type FileAsset struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	Data   []byte `json:"data"`
}

type FileAssetResolver func(string) (string, error)

func validAssetDigest(value string) bool {
	digest, err := hex.DecodeString(value)
	return err == nil && len(digest) == sha256.Size && hex.EncodeToString(digest) == value
}

func AssetReference(id, digest string) string { return "node-asset:" + id + ":" + digest }

func ParseAssetReference(value string) (string, string, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 || parts[0] != "node-asset" || !assetID.MatchString(parts[1]) || !validAssetDigest(parts[2]) {
		return "", "", ErrConfiguration
	}
	return parts[1], parts[2], nil
}

func ValidateFileAssets(assets []FileAsset) error {
	if len(assets) > MaxRuntimeFileAssets {
		return ErrConfiguration
	}
	seen := make(map[string]bool, len(assets))
	total := 0
	for _, asset := range assets {
		if !assetID.MatchString(asset.ID) || !validAssetDigest(asset.SHA256) || seen[asset.ID] || len(asset.Data) == 0 || len(asset.Data) > MaxFileAssetBytes {
			return ErrConfiguration
		}
		seen[asset.ID] = true
		total += len(asset.Data)
		if total > MaxBundleFileBytes {
			return ErrConfiguration
		}
		digest := sha256.Sum256(asset.Data)
		if hex.EncodeToString(digest[:]) != asset.SHA256 {
			return ErrConfiguration
		}
	}
	return nil
}

func AssetManifest(bundle Bundle) []byte {
	type claim struct{ runtime, id, digest string }
	var claims []claim
	for _, runtime := range bundle.Runtimes {
		for _, asset := range runtime.FileAssets {
			claims = append(claims, claim{runtime.ID, asset.ID, asset.SHA256})
		}
	}
	slices.SortFunc(claims, func(a, b claim) int {
		if order := strings.Compare(a.runtime, b.runtime); order != 0 {
			return order
		}
		return strings.Compare(a.id, b.id)
	})
	hash := sha256.New()
	_, _ = hash.Write([]byte(assetManifestDomain))
	for _, claim := range claims {
		for _, value := range []string{claim.runtime, claim.id, claim.digest} {
			_, _ = hash.Write(binary.BigEndian.AppendUint32(nil, uint32(len(value))))
			_, _ = hash.Write([]byte(value))
		}
	}
	return hash.Sum(nil)
}

func RuntimeAssetManifest(id string, assets []FileAsset) []byte {
	return AssetManifest(Bundle{Runtimes: []Runtime{{ID: id, FileAssets: assets}}})
}

func PayloadAssetManifest(payload []byte) []byte {
	bundle, err := Decode(payload)
	if err != nil {
		return AssetManifest(Bundle{})
	}
	return AssetManifest(bundle)
}
