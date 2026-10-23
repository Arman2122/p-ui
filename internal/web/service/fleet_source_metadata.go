package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

type fleetMetadataReceipt struct {
	Kind    string
	ActorID int
	Result  entity.FleetClientMetadataResult
}

func EditFleetClientMetadataTx(ctx context.Context, db *gorm.DB, actor *model.User, request entity.FleetClientMetadataRequest) (entity.FleetClientMetadataResult, error) {
	var result entity.FleetClientMetadataResult
	if db == nil || actor == nil || !fleetID(request.OperationID) || !fleetID(request.ClientID) || request.ExpectedRevision == 0 || request.ExpectedRevision >= math.MaxInt64 ||
		request.Comment == nil || len(*request.Comment) > 4096 || !utf8.ValidString(*request.Comment) || strings.ContainsRune(*request.Comment, 0) {
		return result, controlplane.ErrInvalid
	}
	if request.ExpectedActorID <= 0 || request.ExpectedActorID != actor.Id {
		return result, controlplane.ErrScope
	}
	comment := *request.Comment
	request.Comment = &comment
	encoded, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(append([]byte("fleet-source-metadata/v1:"), encoded...))
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var operation model.FleetOperation
		found := tx.Where("id = ?", request.OperationID).Find(&operation)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected != 0 {
			return replayFleetMetadata(operation, actor.Id, digest[:], &result)
		}
		client, source, err := lockFleetSourceClient(tx, request.ClientID)
		if err != nil {
			return err
		}
		operation = model.FleetOperation{ID: request.OperationID, Digest: digest[:], Result: []byte{}}
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&operation)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			if err := tx.First(&operation, "id = ?", request.OperationID).Error; err != nil {
				return err
			}
			return replayFleetMetadata(operation, actor.Id, digest[:], &result)
		}
		if uint64(client.Revision) != request.ExpectedRevision {
			return controlplane.ErrRevision
		}
		result = entity.FleetClientMetadataResult{ActorID: actor.Id, ClientID: client.ID, PanelClientID: source.Id, Revision: uint64(client.Revision), Changed: source.Comment != comment}
		if result.Changed {
			updated := tx.Model(&source).Updates(map[string]any{"comment": comment, "updated_at": time.Now().UnixMilli()})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return controlplane.ErrRevision
			}
			advanced := tx.Model(&model.FleetClient{}).Where("id = ? AND revision = ?", client.ID, client.Revision).UpdateColumn("revision", client.Revision+1)
			if advanced.Error != nil {
				return advanced.Error
			}
			if advanced.RowsAffected != 1 {
				return controlplane.ErrRevision
			}
			result.Revision++
		}
		receipt, err := json.Marshal(fleetMetadataReceipt{Kind: "fleet-source-metadata/v1", ActorID: actor.Id, Result: result})
		if err != nil {
			return err
		}
		return tx.Model(&operation).Update("result", receipt).Error
	})
	if err != nil {
		return entity.FleetClientMetadataResult{}, err
	}
	return result, nil
}

func replayFleetMetadata(operation model.FleetOperation, actorID int, digest []byte, result *entity.FleetClientMetadataResult) error {
	var receipt fleetMetadataReceipt
	if !bytes.Equal(operation.Digest, digest) || json.Unmarshal(operation.Result, &receipt) != nil || receipt.Kind != "fleet-source-metadata/v1" ||
		receipt.ActorID != actorID || receipt.Result.ActorID != actorID || !fleetID(receipt.Result.ClientID) || receipt.Result.PanelClientID <= 0 || receipt.Result.Revision == 0 {
		return controlplane.ErrConflict
	}
	*result = receipt.Result
	result.Replayed = true
	return nil
}
