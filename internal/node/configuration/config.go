package configuration

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/governor"
)

var ErrConfiguration = errors.New("invalid runtime configuration")

var runtimeID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

type Runtime struct {
	ID            string                 `json:"id"`
	Core          string                 `json:"core"`
	Configuration json.RawMessage        `json:"configuration"`
	Grants        []governor.Grant       `json:"grants"`
	FileAssets    []FileAsset            `json:"fileAssets,omitempty"`
	Admissions    []admission.Descriptor `json:"admissions,omitempty"`
}

type Bundle struct {
	Version  uint32    `json:"version"`
	Runtimes []Runtime `json:"runtimes"`
}

func Decode(payload []byte) (Bundle, error) {
	var result Bundle
	if len(payload) == 0 || len(payload) > 32<<20 {
		return result, ErrConfiguration
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Bundle{}, ErrConfiguration
	}
	if decoder.Decode(new(any)) != io.EOF || result.Version != 1 || len(result.Runtimes) > 32 {
		return Bundle{}, ErrConfiguration
	}
	seen := make(map[string]bool)
	grants := make(map[string]bool)
	assetBytes := 0
	for _, runtime := range result.Runtimes {
		if !runtimeID.MatchString(runtime.ID) || !runtimeID.MatchString(runtime.Core) || seen[runtime.ID] || len(runtime.Grants) > 1_000_000 || len(runtime.Configuration) == 0 {
			return Bundle{}, ErrConfiguration
		}
		seen[runtime.ID] = true
		if admission.Validate(runtime.Admissions) != nil {
			return Bundle{}, ErrConfiguration
		}
		if err := ValidateFileAssets(runtime.FileAssets); err != nil {
			return Bundle{}, err
		}
		for _, asset := range runtime.FileAssets {
			assetBytes += len(asset.Data)
			if assetBytes > MaxBundleFileBytes {
				return Bundle{}, ErrConfiguration
			}
		}
		clients := make(map[string]bool)
		for _, grant := range runtime.Grants {
			if grants[grant.ID] || clients[grant.ClientID] || governor.Validate(grant) != nil {
				return Bundle{}, ErrConfiguration
			}
			grants[grant.ID], clients[grant.ClientID] = true, true
		}
	}
	return result, nil
}
