package service

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/policy"
	"github.com/Arman2122/p-ui/internal/web/session"
)

var (
	ErrFleetSource    = errors.New("fleet source policy or placement is unavailable or has changed")
	ErrFleetPool      = errors.New("shared quota pool still has another accounting authority")
	ErrFleetPhase     = errors.New("fleet speed-tier authority must settle before the next phase")
	ErrFleetLifecycle = errors.New("client lifecycle is owned by the fleet control plane")
)

type fleetClientSnapshot struct {
	Client           model.ClientRecord
	Fleet            model.FleetClient
	Term             *model.FleetTerm
	Policy           *model.Policy
	Assignment       *model.ClientPolicy
	Group            *model.ClientGroup
	GroupQuota       int64
	Owner            *model.ResellerAccount
	Traffic          core.ClientTraffic
	Tiers            policy.Plan
	Inbounds         []model.Inbound
	Attachments      []model.ClientInbound
	Credentials      []model.ClientCredential
	CredentialDigest []byte
	PolicyDigest     []byte
	DefinitionDigest []byte
}

func fleetDigest(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return digest[:], nil
}

func requireFleetAdmin(tx *gorm.DB, actor *model.User) error {
	if actor == nil || actor.Id <= 0 || !session.IsAdminUser(actor) {
		return ErrResellerForbidden
	}
	var current model.User
	if err := tx.First(&current, actor.Id).Error; err != nil {
		return err
	}
	if !session.IsAdminUser(&current) || current.LoginEpoch != actor.LoginEpoch {
		return ErrResellerForbidden
	}
	return nil
}

func fleetSnapshot(tx *gorm.DB, clientID int, inboundIDs []int, runtimeCore string) (fleetClientSnapshot, error) {
	var out fleetClientSnapshot
	if clientID <= 0 || len(inboundIDs) == 0 || len(inboundIDs) > 4096 {
		return out, ErrFleetSource
	}
	inboundIDs = slices.Clone(inboundIDs)
	slices.Sort(inboundIDs)
	for i, id := range inboundIDs {
		if id <= 0 || i > 0 && id == inboundIDs[i-1] {
			return out, ErrFleetSource
		}
	}
	var err error
	out, err = fleetClientPolicySnapshotTx(tx, clientID)
	if err != nil {
		return out, err
	}
	if out.Owner != nil {
		var granted int64
		if err := tx.Model(&model.ResellerInboundGrant{}).Where("user_id = ? AND inbound_id IN ?", out.Owner.UserID, inboundIDs).Count(&granted).Error; err != nil {
			return out, err
		}
		if granted != int64(len(inboundIDs)) {
			return out, ErrResellerForbidden
		}
	}
	var links []model.ClientInbound
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("client_id = ?", clientID).Order("inbound_id").Find(&links).Error; err != nil {
		return out, err
	}
	out.Attachments = links
	var attached []int
	var transportBindings []struct {
		InboundID int
		Flow      string
	}
	for _, link := range links {
		attached = append(attached, link.InboundId)
		if slices.Contains(inboundIDs, link.InboundId) {
			transportBindings = append(transportBindings, struct {
				InboundID int
				Flow      string
			}{link.InboundId, link.FlowOverride})
		}
	}
	attached, err = fleetEffectiveTemplateIDsTx(tx, out.Client, out.Assignment, attached)
	if err != nil {
		return out, err
	}
	for _, id := range inboundIDs {
		if !slices.Contains(attached, id) {
			return out, ErrFleetSource
		}
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id IN ?", inboundIDs).Order("id").Find(&out.Inbounds).Error; err != nil {
		return out, err
	}
	if len(out.Inbounds) != len(inboundIDs) {
		return out, ErrFleetSource
	}
	var definitions []any
	for _, inbound := range out.Inbounds {
		family, known := cores.NodeRuntimeFor(core.Kind(inbound.Protocol))
		if !known || family != runtimeCore || inbound.NodeID != nil {
			return out, ErrFleetSource
		}
		settings := map[string]json.RawMessage{}
		if inbound.Settings != "" {
			if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
				return out, ErrFleetSource
			}
		}
		delete(settings, "clients")
		encodedSettings, err := json.Marshal(settings)
		if err != nil {
			return out, err
		}
		definitions = append(definitions, struct {
			ID                              int
			Protocol                        model.Protocol
			Listen                          string
			Port                            int
			Enable                          bool
			Expiry, Quota                   int64
			Tag, Settings, Stream, Sniffing string
			Egress                          *int
		}{
			inbound.Id, inbound.Protocol, inbound.Listen, inbound.Port, inbound.Enable, inbound.ExpiryTime, inbound.Total,
			inbound.Tag, string(encodedSettings), inbound.StreamSettings, inbound.Sniffing, inbound.EgressID,
		})
	}
	out.DefinitionDigest, err = fleetDigest(struct {
		Definitions                         []any
		Flow, Security, Reverse, AllowedIPs string
		KeepAlive                           int
		Bindings                            []struct {
			InboundID int
			Flow      string
		}
	}{definitions, out.Client.Flow, out.Client.Security, out.Client.Reverse, out.Client.AllowedIPs, out.Client.KeepAlive, transportBindings})
	if err != nil {
		return out, err
	}
	if err := completeFleetClientSnapshotTx(tx, &out); err != nil {
		return out, err
	}
	if err := validateFleetSourceCredentialClaimsTx(tx, out); err != nil {
		return out, err
	}
	return out, nil
}

func sameOwner(first, second *int) bool {
	return first == nil && second == nil || first != nil && second != nil && *first == *second
}

func fleetRates(plan policy.Plan, used int64) controlplane.Rates {
	selected := policy.Evaluate(plan, used)
	rate := func(bits int64) limits.Rate {
		if bits == 0 {
			return limits.Rate{Mode: limits.Unlimited}
		}
		return limits.Rate{Mode: limits.Limited, BitsPerSecond: uint64(bits), BurstBytes: int(max(64<<10, min(1<<20, bits/80)))}
	}
	return controlplane.Rates{Upload: rate(selected.UpBps), Download: rate(selected.DownBps)}
}
