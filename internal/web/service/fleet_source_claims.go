package service

import (
	"bytes"
	"errors"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
)

var ErrFleetCredentialClaim = errors.New("authentication identity is already reserved or has an unqualified owner")

func validateFleetSourceCredentialClaimsTx(tx *gorm.DB, snapshot fleetClientSnapshot) error {
	if snapshot.Fleet.CredentialClaimsVersion == 0 {
		return nil
	}
	if snapshot.Fleet.CredentialClaimsVersion != 1 || !snapshot.Fleet.Owned {
		return ErrFleetCredentialClaim
	}
	byInbound := make(map[int]map[string]string)
	for _, row := range snapshot.Credentials {
		if byInbound[row.InboundId] == nil {
			byInbound[row.InboundId] = make(map[string]string)
		}
		byInbound[row.InboundId][row.Key] = row.Value
	}
	var keys [][]any
	for _, inbound := range snapshot.Inbounds {
		client := snapshot.Client.ToClient()
		applyCoreCredentials(client, byInbound[inbound.Id])
		authority, ok := cores.SourceClientCredentialAuthority(core.Kind(inbound.Protocol))
		if !ok {
			return ErrFleetSourceCredentials
		}
		claims, err := authority.SourceClientCredentialClaims(core.Kind(inbound.Protocol), inbound.Settings, client.Email, clientCredentialValues(client))
		if err != nil || len(claims) == 0 || len(claims) > 8 {
			return ErrFleetSourceCredentials
		}
		seen := make(map[core.SourceCredentialClaim]bool)
		for _, claim := range claims {
			if !seen[claim] {
				keys = append(keys, []any{inbound.Id, claim.Scheme, bytes.Clone(claim.Digest[:])})
				seen[claim] = true
			}
		}
	}
	for offset := 0; offset < len(keys); offset += 256 {
		batch := keys[offset:min(offset+256, len(keys))]
		var count int64
		if err := tx.Model(&model.FleetSourceCredentialClaim{}).Where("client_id = ? AND (inbound_id,scheme,digest) IN ?", snapshot.Fleet.ID, batch).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(batch)) {
			return ErrFleetCredentialClaim
		}
	}
	return nil
}

func reserveFleetSourceCredentialClaimsTx(tx *gorm.DB, client model.FleetClient, prepared fleetPreparedCredentials, inbounds []model.Inbound) error {
	var rows []model.FleetSourceCredentialClaim
	ids := make([]int, 0, len(inbounds))
	for _, inbound := range inbounds {
		authority, ok := cores.SourceClientCredentialAuthority(core.Kind(inbound.Protocol))
		if !ok || prepared.Bindings[inbound.Id] == nil {
			return ErrFleetSourceCredentials
		}
		claims, err := authority.SourceClientCredentialClaims(core.Kind(inbound.Protocol), inbound.Settings, prepared.Client.Email, prepared.Bindings[inbound.Id])
		if err != nil || len(claims) == 0 || len(claims) > 8 {
			return ErrFleetSourceCredentials
		}
		ids = append(ids, inbound.Id)
		for _, claim := range claims {
			if len(claim.Scheme) == 0 || len(claim.Scheme) > 64 || claim.Digest == [32]byte{} {
				return ErrFleetSourceCredentials
			}
			rows = append(rows, model.FleetSourceCredentialClaim{InboundID: inbound.Id, Scheme: claim.Scheme, Digest: bytes.Clone(claim.Digest[:]), ClientID: client.ID, CredentialGeneration: client.CredentialGeneration})
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var unqualified bool
	if err := tx.Raw(`WITH candidates AS (
SELECT ci.client_id,ci.inbound_id FROM client_inbounds ci WHERE ci.inbound_id IN ?
UNION ALL
SELECT c.id,pi.inbound_id FROM policy_inbounds pi
JOIN client_policies cp ON cp.policy_id=pi.policy_id AND NOT cp.override_inbounds
JOIN clients c ON c.email=cp.email WHERE pi.inbound_id IN ?
UNION ALL
SELECT c.id,gi.inbound_id FROM group_inbounds gi
JOIN clients c ON c.group_name=gi.name WHERE gi.inbound_id IN ?)
SELECT EXISTS (SELECT 1 FROM candidates candidate
LEFT JOIN fleet_clients fc ON fc.panel_client_id=candidate.client_id
WHERE candidate.client_id<>? AND (fc.id IS NULL OR NOT fc.owned OR fc.credential_claims_version<>1 OR NOT EXISTS
(SELECT 1 FROM fleet_source_credential_claims claim WHERE claim.inbound_id=candidate.inbound_id AND claim.client_id=fc.id)))
OR EXISTS (SELECT 1 FROM fleet_placement_inbounds binding
JOIN fleet_placements placement ON placement.id=binding.placement_id
JOIN fleet_clients fc ON fc.id=placement.client_id
WHERE binding.panel_inbound IN ? AND fc.id<>? AND (NOT fc.owned OR fc.credential_claims_version<>1 OR NOT EXISTS
(SELECT 1 FROM fleet_source_credential_claims claim WHERE claim.inbound_id=binding.panel_inbound AND claim.client_id=fc.id)))`,
		ids, ids, ids, *client.PanelClientID, ids, client.ID).Scan(&unqualified).Error; err != nil {
		return err
	}
	if unqualified {
		return ErrFleetCredentialClaim
	}
	slices.SortFunc(rows, func(a, b model.FleetSourceCredentialClaim) int {
		if a.InboundID != b.InboundID {
			if a.InboundID < b.InboundID {
				return -1
			}
			return 1
		}
		if order := strings.Compare(a.Scheme, b.Scheme); order != 0 {
			return order
		}
		return bytes.Compare(a.Digest, b.Digest)
	})
	for _, row := range rows {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		var stored model.FleetSourceCredentialClaim
		if err := tx.First(&stored, "inbound_id = ? AND scheme = ? AND digest = ?", row.InboundID, row.Scheme, row.Digest).Error; err != nil {
			return err
		}
		if stored.ClientID != client.ID {
			return ErrFleetCredentialClaim
		}
	}
	return nil
}
