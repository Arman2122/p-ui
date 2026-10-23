package controlplane

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type PlacementCapability struct {
	CoreID                 string   `json:"coreId"`
	Version                string   `json:"version"`
	Protocols              []string `json:"protocols"`
	ClientRateEnforcement  bool     `json:"clientRateEnforcement"`
	ClientQuotaEnforcement bool     `json:"clientQuotaEnforcement"`
	ActivationAdmission    bool     `json:"activationAdmission"`
}

func placementCapabilityToken(value string) bool {
	if len(value) == 0 || len(value) > 48 {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.'
	})
}

func canonicalPlacementCapabilities(input []*nodev1.CoreCapability) ([]byte, error) {
	if len(input) > 128 {
		return nil, ErrInvalid
	}
	capabilities := make([]PlacementCapability, 0, len(input))
	cores := make(map[string]bool, len(input))
	for _, item := range input {
		if item == nil || !placementCapabilityToken(item.CoreId) || cores[item.CoreId] || len(item.Version) == 0 || len(item.Version) > 128 ||
			strings.ContainsFunc(item.Version, func(r rune) bool { return r < 33 || r > 126 }) || len(item.Protocols) > 128 {
			return nil, ErrInvalid
		}
		cores[item.CoreId] = true
		protocols := append([]string{}, item.Protocols...)
		slices.Sort(protocols)
		for i, protocol := range protocols {
			if !placementCapabilityToken(protocol) || i > 0 && protocols[i-1] == protocol {
				return nil, ErrInvalid
			}
		}
		capabilities = append(capabilities, PlacementCapability{
			CoreID: item.CoreId, Version: item.Version, Protocols: protocols,
			ClientRateEnforcement: item.ClientRateEnforcement, ClientQuotaEnforcement: item.ClientQuotaEnforcement, ActivationAdmission: item.ActivationAdmission,
		})
	}
	slices.SortFunc(capabilities, func(a, b PlacementCapability) int { return strings.Compare(a.CoreID, b.CoreID) })
	return json.Marshal(capabilities)
}

func recordPlacementCapabilitiesTx(tx *gorm.DB, node model.FleetNode, encoded []byte, now int64) error {
	digest := sha256.Sum256(encoded)
	var current model.FleetNodeCapabilitySnapshot
	query := tx.Where("node_id = ?", node.ID).Find(&current)
	if query.Error != nil {
		return query.Error
	}
	changed := query.RowsAffected == 0 || current.InstallationID != node.InstallationID || !bytes.Equal(current.Digest, digest[:])
	if current.InstallationID != node.InstallationID {
		current.Revision = 0
	}
	if changed {
		if current.Revision == math.MaxInt64 {
			return ErrRevision
		}
		current.Revision++
	}
	current.NodeID, current.InstallationID, current.SessionGeneration = node.ID, node.InstallationID, node.AuthorityGeneration
	current.Capabilities, current.Digest, current.ReceivedUnixMilli = string(encoded), digest[:], now
	if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&current).Error; err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return QueuePlacementReevaluationTx(tx, "node", node.ID+"/"+node.InstallationID)
}

// Callers commit the invalidation with its source write; fanout workers acquire no source locks while leasing.
func QueuePlacementReevaluationTx(tx *gorm.DB, scope, id string) error {
	if tx == nil || len(id) == 0 || len(id) > 128 || !slices.Contains([]string{"node", "rule", "client", "default"}, scope) {
		return ErrInvalid
	}
	if _, transactional := tx.Statement.ConnPool.(gorm.TxCommitter); !transactional {
		return ErrInvalid
	}
	row := model.FleetPlacementFanout{Scope: scope, ScopeID: id, Revision: 1, Cursor: "{}", Pending: true, AvailableUnixMilli: time.Now().UnixMilli()}
	result := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "scope"}, {Name: "scope_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"revision": gorm.Expr("fleet_placement_fanouts.revision + 1"), "cursor": "{}", "pending": true,
			"available_unix_milli": row.AvailableUnixMilli, "lease_id": nil, "lease_until_unix_milli": 0,
		}),
		Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "fleet_placement_fanouts.revision < ?", Vars: []any{int64(math.MaxInt64)}}}},
	}).Create(&row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRevision
	}
	return nil
}
