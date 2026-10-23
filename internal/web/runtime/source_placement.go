package runtime

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type SourcePlacementRequest struct {
	OperationID      string `json:"operationId" example:"39226ea4-a89b-4377-8c2a-c32c977ead47"`
	ClientID         string `json:"clientId" example:"70924ba1-e366-4638-801c-b1055cabcd77"`
	ExpectedRevision uint64 `json:"expectedRevision" example:"1"`
	Action           string `json:"action" example:"restrict"`
	NodeID           string `json:"nodeId,omitempty" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	InboundIDs       *[]int `json:"inboundIds" example:"[1,2]"`
}

type SourcePlacementResult struct {
	ClientID string `json:"clientId" example:"70924ba1-e366-4638-801c-b1055cabcd77"`
	Revision uint64 `json:"revision" example:"2"`
	Pending  bool   `json:"pending" example:"true"`
	Replayed bool   `json:"-"`
}

type SourcePlacementRuntime interface {
	SetClientPlacementTx(context.Context, *gorm.DB, *model.User, SourcePlacementRequest) (SourcePlacementResult, error)
}
