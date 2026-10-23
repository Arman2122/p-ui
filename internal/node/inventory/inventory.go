package inventory

import (
	"context"
	"errors"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

var ErrInventory = errors.New("runtime inventory is unavailable or invalid")

const MaxResponseBytes = 16 << 10

type Source interface {
	Capability(context.Context) (*nodev1.CoreCapability, error)
}

type Snapshot struct {
	NodeID         string                         `json:"nodeId"`
	InstallationID string                         `json:"installationId"`
	ControlPlaneID string                         `json:"controlPlaneId"`
	Journal        *nodev1.RuntimeJournalIdentity `json:"journal"`
	Capability     *nodev1.CoreCapability         `json:"capability"`
}

func validToken(value string) bool {
	return len(value) > 0 && len(value) <= 48 && !strings.ContainsFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.'
	})
}

func Normalize(value *nodev1.CoreCapability) (*nodev1.CoreCapability, error) {
	if value == nil || !validToken(value.CoreId) || len(value.Version) != 71 || !strings.HasPrefix(value.Version, "sha256:") ||
		strings.ContainsFunc(value.Version[7:], func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }) || len(value.Protocols) == 0 || len(value.Protocols) > 128 {
		return nil, ErrInventory
	}
	result := proto.Clone(value).(*nodev1.CoreCapability)
	slices.Sort(result.Protocols)
	for i, protocol := range result.Protocols {
		if !validToken(protocol) || i > 0 && protocol == result.Protocols[i-1] {
			return nil, ErrInventory
		}
	}
	return result, nil
}

func Canonical(values []*nodev1.CoreCapability) ([]*nodev1.CoreCapability, error) {
	if len(values) > 128 {
		return nil, ErrInventory
	}
	result := make([]*nodev1.CoreCapability, len(values))
	for i, value := range values {
		var err error
		result[i], err = Normalize(value)
		if err != nil {
			return nil, err
		}
	}
	slices.SortFunc(result, func(a, b *nodev1.CoreCapability) int { return strings.Compare(a.CoreId, b.CoreId) })
	for i := 1; i < len(result); i++ {
		if result[i-1].CoreId == result[i].CoreId {
			return nil, ErrInventory
		}
	}
	return result, nil
}
