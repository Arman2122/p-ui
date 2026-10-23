package configuration

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"slices"
	"strings"

	"github.com/Arman2122/p-ui/internal/node/admission"
)

type DescriptorScope struct {
	CredentialGeneration uint64
	PlacementID          string
	PlacementRevision    uint64
	SHA256               []byte
}

func Project(payload []byte, id, core string) ([]byte, error) {
	bundle, err := Decode(payload)
	if err != nil {
		return nil, err
	}
	own, err := ProjectBundle(bundle, id, core)
	if err != nil {
		return nil, err
	}
	return json.Marshal(own)
}

// The caller validates the complete bundle once before projecting multiple runtimes.
func ProjectBundle(bundle Bundle, id, core string) (Bundle, error) {
	if bundle.Version != 1 || !runtimeID.MatchString(id) || !runtimeID.MatchString(core) {
		return Bundle{}, ErrConfiguration
	}
	own := Bundle{Version: bundle.Version}
	for _, runtime := range bundle.Runtimes {
		if runtime.ID == id {
			if runtime.Core != core {
				return Bundle{}, ErrConfiguration
			}
			own.Runtimes = []Runtime{runtime}
			break
		}
	}
	return own, nil
}

func DescriptorScopes(runtime Runtime) (map[string]DescriptorScope, error) {
	if !runtimeID.MatchString(runtime.ID) || admission.Validate(runtime.Admissions) != nil {
		return nil, ErrConfiguration
	}
	groups := make(map[string][]admission.Descriptor)
	for _, descriptor := range runtime.Admissions {
		groups[descriptor.ClientID] = append(groups[descriptor.ClientID], descriptor)
	}
	scopes := make(map[string]DescriptorScope, len(groups))
	for client, descriptors := range groups {
		first := descriptors[0]
		for _, descriptor := range descriptors {
			if descriptor.CredentialGeneration != first.CredentialGeneration || descriptor.PlacementID != first.PlacementID || descriptor.PlacementRevision != first.PlacementRevision {
				return nil, ErrConfiguration
			}
		}
		slices.SortFunc(descriptors, func(a, b admission.Descriptor) int {
			if compared := strings.Compare(a.InboundInstanceID, b.InboundInstanceID); compared != 0 {
				return compared
			}
			return strings.Compare(a.InboundTag, b.InboundTag)
		})
		encoded, err := json.Marshal(descriptors)
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		_, _ = hash.Write([]byte("penhoon/node/client-descriptors/v1\x00"))
		for _, value := range []string{runtime.ID, client} {
			_, _ = hash.Write(binary.BigEndian.AppendUint32(nil, uint32(len(value))))
			_, _ = hash.Write([]byte(value))
		}
		_, _ = hash.Write(encoded)
		scopes[client] = DescriptorScope{CredentialGeneration: first.CredentialGeneration, PlacementID: first.PlacementID, PlacementRevision: first.PlacementRevision, SHA256: hash.Sum(nil)}
	}
	return scopes, nil
}
