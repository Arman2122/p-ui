package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

var ErrFleetCreateInput = errors.New("invalid fleet client creation request")

type fleetCreateReceipt struct {
	Kind    string
	ActorID int
	Result  entity.FleetClientCreateResult
}

func CreateFleetClientTx(ctx context.Context, db *gorm.DB, actor *model.User, request entity.ClientCreatePayload) (entity.FleetClientCreateResult, error) {
	var result entity.FleetClientCreateResult
	if db == nil || actor == nil {
		return result, ErrFleetCreateInput
	}
	if request.ExpectedActorID != 0 && request.ExpectedActorID != actor.Id {
		return result, controlplane.ErrScope
	}
	request, digest, err := canonicalFleetCreate(request, actor.Id)
	if err != nil {
		return result, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		operation := model.FleetOperation{ID: request.OperationID, Digest: digest, Result: []byte{}}
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&operation)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			if err := tx.First(&operation, "id = ?", request.OperationID).Error; err != nil {
				return err
			}
			return replayFleetCreate(operation, actor.Id, digest, &result)
		}
		var err error
		result, err = persistFleetSourceCreate(tx, request)
		if err != nil {
			return err
		}
		result.ActorID = actor.Id
		encoded, err := json.Marshal(fleetCreateReceipt{Kind: "fleet-source-create/v1", ActorID: actor.Id, Result: result})
		if err != nil {
			return err
		}
		return tx.Model(&operation).Update("result", encoded).Error
	})
	if err != nil {
		return entity.FleetClientCreateResult{}, err
	}
	return result, nil
}

func canonicalFleetCreate(request entity.ClientCreatePayload, actorID int) (entity.ClientCreatePayload, []byte, error) {
	client := request.Client
	if !fleetID(request.OperationID) || actorID <= 0 || strings.TrimSpace(client.Email) == "" || len(client.Email) > 256 ||
		len(client.SubID) > 256 || len(client.Group) > 128 || len(client.Comment) > 4096 || client.TotalGB < 0 ||
		client.ExpiryTime == math.MinInt64 || client.LimitIP < 0 || client.Reset < 0 || client.KeepAlive < 0 || client.KeepAlive > 65535 ||
		len(request.InboundIds) > 4096 || len(request.CredentialBindings) > 4096 || len(request.ExternalLinks) > 128 ||
		request.PolicyID != nil && *request.PolicyID <= 0 || request.PolicyID == nil && request.PlanOverrides != nil {
		return request, nil, ErrFleetCreateInput
	}
	if validateClientEmail(client.Email) != nil || validateNewClientSubID(client.SubID) != nil {
		return request, nil, ErrFleetCreateInput
	}
	var err error
	request.InboundIds, err = canonicalPlacementInboundIDs(request.InboundIds)
	if err != nil {
		return request, nil, ErrFleetCreateInput
	}
	request.CredentialBindings = slices.Clone(request.CredentialBindings)
	slices.SortFunc(request.CredentialBindings, func(a, b entity.ClientCredentialBindingInput) int {
		if a.InboundID < b.InboundID {
			return -1
		}
		if a.InboundID > b.InboundID {
			return 1
		}
		return 0
	})
	encoded, err := json.Marshal(struct {
		ActorID int
		Request entity.ClientCreatePayload
	}{actorID, request})
	if err != nil || len(encoded) > 512<<10 {
		return request, nil, ErrFleetCreateInput
	}
	digest := sha256.Sum256(append([]byte("fleet-source-create/v1:"), encoded...))
	return request, digest[:], nil
}

func replayFleetCreate(operation model.FleetOperation, actorID int, digest []byte, result *entity.FleetClientCreateResult) error {
	var receipt fleetCreateReceipt
	if !bytes.Equal(operation.Digest, digest) || json.Unmarshal(operation.Result, &receipt) != nil || receipt.Kind != "fleet-source-create/v1" ||
		receipt.ActorID != actorID || receipt.Result.ActorID != 0 && receipt.Result.ActorID != receipt.ActorID || !fleetID(receipt.Result.ClientID) || receipt.Result.PanelClientID <= 0 || receipt.Result.Revision == 0 {
		return controlplane.ErrConflict
	}
	*result = receipt.Result
	result.ActorID = receipt.ActorID
	result.Replayed = true
	return nil
}

func lockFleetNewSubscription(tx *gorm.DB, subID string) error {
	digest := sha256.Sum256([]byte("fleet-client-subscription/v1:" + subID))
	if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(binary.BigEndian.Uint64(digest[:8]))).Error; err != nil {
		return err
	}
	var exists bool
	if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM clients WHERE sub_id = ?)", subID).Scan(&exists).Error; err != nil {
		return err
	}
	if exists {
		return controlplane.ErrConflict
	}
	return nil
}

func persistFleetSourceCreate(tx *gorm.DB, request entity.ClientCreatePayload) (entity.FleetClientCreateResult, error) {
	var result entity.FleetClientCreateResult
	client := request.Client
	if client.SubID == "" {
		client.SubID = uuid.NewString()
	}
	if err := lockFleetNewSubscription(tx, client.SubID); err != nil {
		return result, err
	}
	if client.Group != "" {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ClientGroup{Name: client.Group}).Error; err != nil {
			return result, err
		}
	}
	external, err := normalizeExternalLinks(request.ExternalLinks)
	if err != nil {
		return result, ErrFleetCreateInput
	}
	explicit, err := prepareFleetCreatePlan(tx, request, &client)
	if err != nil {
		return result, err
	}
	if client.ID == "" {
		client.ID = uuid.NewString()
	}
	if !fleetID(client.ID) {
		return result, ErrFleetSourceCredentials
	}
	now := time.Now().UnixMilli()
	client.CreatedAt, client.UpdatedAt = now, now
	record := client.ToRecord()
	created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(record)
	if created.Error != nil {
		return result, created.Error
	}
	if created.RowsAffected != 1 {
		return result, controlplane.ErrConflict
	}
	if err := tx.Model(record).Update("enable", client.Enable).Error; err != nil {
		return result, err
	}
	record.Enable = client.Enable
	assignment, err := recordFleetCreatePlan(tx, request, record.Email)
	if err != nil {
		return result, err
	}
	ids, err := fleetEffectiveTemplateIDsTx(tx, *record, assignment, explicit)
	if err != nil {
		return result, err
	}
	var inbounds []model.Inbound
	if len(ids) > 0 {
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id IN ? AND node_id IS NULL", ids).Order("id").Find(&inbounds).Error; err != nil {
			return result, err
		}
	}
	if len(inbounds) != len(ids) {
		return result, ErrFleetSource
	}
	prepared, err := prepareFleetSourceCredentials(client, inbounds, request.CredentialBindings)
	if err != nil {
		return result, err
	}
	credentials := fleetSourceCredentialRows(record.Id, prepared)
	if err := validateFleetCredentialStorage(credentials); err != nil {
		return result, err
	}
	if len(credentials) > 0 {
		if err := tx.CreateInBatches(&credentials, 256).Error; err != nil {
			return result, err
		}
	}
	for _, id := range explicit {
		if !slices.Contains(ids, id) {
			return result, ErrFleetSource
		}
		if err := tx.Create(&model.ClientInbound{ClientId: record.Id, InboundId: id, FlowOverride: client.Flow}).Error; err != nil {
			return result, err
		}
	}
	for i := range external {
		external[i].ClientId = record.Id
	}
	if len(external) > 0 {
		if err := tx.CreateInBatches(&external, 128).Error; err != nil {
			return result, err
		}
	}
	snapshot, err := fleetBillingSnapshotTx(tx, record.Id)
	if err != nil {
		return result, err
	}
	owner := model.FleetClient{ID: uuid.NewString(), PanelClientID: &record.Id, Revision: 1, CredentialGeneration: 1, CredentialClaimsVersion: 1, CredentialDigest: snapshot.CredentialDigest, Owned: true}
	if err := tx.Create(&owner).Error; err != nil {
		return result, err
	}
	if err := reserveFleetSourceCredentialClaimsTx(tx, owner, prepared, inbounds); err != nil {
		return result, err
	}
	if err := refreshFleetResetEnabledTx(tx, owner, client.Enable); err != nil {
		return result, err
	}
	if err := controlplane.QueuePlacementReevaluationTx(tx, "client", owner.ID); err != nil {
		return result, err
	}
	return entity.FleetClientCreateResult{ClientID: owner.ID, PanelClientID: record.Id, Revision: 1, Pending: true, NodeDerived: prepared.NodeDerived}, nil
}
