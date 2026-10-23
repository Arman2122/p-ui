package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/Arman2122/p-ui/internal/node/compatibility"
)

var ErrRelease = errors.New("node release signature, platform, or asset rejected")

const signatureDomain = "penhoon-node-release/v1\x00"

var versionName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,95}$`)

type Asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Version     uint32  `json:"version"`
	Release     string  `json:"release"`
	Sequence    uint64  `json:"sequence"`
	Platform    string  `json:"platform"`
	StateFormat uint32  `json:"stateFormat"`
	Assets      []Asset `json:"assets"`
}

var binaries = []string{"penhoon-agent", "penhoon-xray", "penhoon-ssh", "penhoon-singbox", "penhoon-mtproto", "penhoon-native", "penhoon-release"}

func validPlatform(platform string) bool {
	return slices.Contains([]string{"linux-amd64", "linux-arm64", "linux-armv7", "linux-armv6", "linux-armv5", "linux-386", "linux-s390x"}, platform)
}

func (m Manifest) validate() error {
	if m.Version != 1 || !versionName.MatchString(m.Release) || m.Sequence == 0 || m.StateFormat == 0 || !validPlatform(m.Platform) || len(m.Assets) != len(binaries) {
		return ErrRelease
	}
	seen := make(map[string]bool)
	for _, asset := range m.Assets {
		if !slices.Contains(binaries, asset.Name) || seen[asset.Name] || asset.Size <= 0 || asset.Size > 512<<20 {
			return ErrRelease
		}
		digest, err := hex.DecodeString(asset.SHA256)
		if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != asset.SHA256 {
			return ErrRelease
		}
		seen[asset.Name] = true
	}
	return nil
}

func Create(dir, name, platform string, sequence uint64) (Manifest, error) {
	m := Manifest{Version: 1, Release: name, Platform: platform, Sequence: sequence, StateFormat: compatibility.StateFormat}
	for _, binary := range binaries {
		path := filepath.Join(dir, binary)
		info, err := os.Lstat(path)
		if err != nil {
			return m, err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 512<<20 {
			return m, ErrRelease
		}
		file, err := os.Open(path)
		if err != nil {
			return m, err
		}
		hash := sha256.New()
		size, err := io.Copy(hash, file)
		closeErr := file.Close()
		if err != nil {
			return m, err
		}
		if closeErr != nil {
			return m, closeErr
		}
		if size != info.Size() {
			return m, ErrRelease
		}
		m.Assets = append(m.Assets, Asset{Name: binary, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	return m, m.validate()
}

func Sign(m Manifest, key ed25519.PrivateKey) ([]byte, []byte, error) {
	if m.validate() != nil || len(key) != ed25519.PrivateKeySize {
		return nil, nil, ErrRelease
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, nil, err
	}
	return data, ed25519.Sign(key, append([]byte(signatureDomain), data...)), nil
}

func Verify(data, signature []byte, key ed25519.PublicKey, platform string) (Manifest, error) {
	if len(data) > 64<<10 || len(key) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, append([]byte(signatureDomain), data...), signature) {
		return Manifest{}, ErrRelease
	}
	return decodeManifest(data, platform)
}

func decodeManifest(data []byte, platform string) (Manifest, error) {
	var m Manifest
	if len(data) > 64<<10 {
		return m, ErrRelease
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || decoder.Decode(new(any)) != io.EOF || m.validate() != nil || m.Platform != platform || m.StateFormat != compatibility.StateFormat {
		return Manifest{}, ErrRelease
	}
	canonical, err := json.Marshal(m)
	if err != nil || !bytes.Equal(canonical, data) {
		return Manifest{}, ErrRelease
	}
	return m, nil
}

func VerifyAndStage(dir, destination string, data, signature []byte, key ed25519.PublicKey, platform string) (Manifest, error) {
	m, err := Verify(data, signature, key, platform)
	if err != nil {
		return Manifest{}, err
	}
	if err := stage(dir, destination, m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func stage(dir, destination string, m Manifest) error {
	if m.validate() != nil || !filepath.IsAbs(dir) || !filepath.IsAbs(destination) {
		return ErrRelease
	}
	if _, err := os.Lstat(destination); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(destination), ".node-stage-")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			for _, asset := range m.Assets {
				_ = os.Remove(filepath.Join(temporary, asset.Name))
			}
			_ = os.Remove(temporary)
		}
	}()
	for _, asset := range m.Assets {
		path := filepath.Join(dir, asset.Name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != asset.Size {
			return ErrRelease
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(filepath.Join(temporary, asset.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			_ = input.Close()
			return err
		}
		hash := sha256.New()
		copied, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, asset.Size+1))
		_ = input.Close()
		if copyErr == nil {
			copyErr = output.Sync()
		}
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if copied != asset.Size || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
			return ErrRelease
		}
	}
	if err := commitStage(temporary, destination); err != nil {
		return err
	}
	committed = true
	return nil
}
