package runtime

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type ResetScheduleRequest struct {
	OperationID      string `json:"operationId" validate:"required,uuid" example:"11111111-1111-4111-8111-111111111111"`
	ClientID         string `json:"clientId" validate:"required,uuid" example:"22222222-2222-4222-8222-222222222222"`
	ExpectedRevision uint64 `json:"expectedRevision" example:"1"`
	Override         bool   `json:"override" example:"true"`
	Cycle            string `json:"cycle,omitempty" validate:"omitempty,oneof=none hourly daily weekly monthly" example:"monthly"`
}

type ResetScheduleView struct {
	ClientID             string `json:"clientId" example:"22222222-2222-4222-8222-222222222222"`
	Revision             uint64 `json:"revision" example:"1"`
	Inherited            bool   `json:"inherited" example:"false"`
	Cycle                string `json:"cycle" example:"monthly"`
	AnchorUnixMilli      int64  `json:"anchorUnixMilli" example:"1792353600000"`
	NextDueUnixMilli     int64  `json:"nextDueUnixMilli" example:"1795032000000"`
	PendingOperationID   string `json:"pendingOperationId,omitempty" example:"33333333-3333-4333-8333-333333333333"`
	NextAttemptUnixMilli int64  `json:"nextAttemptUnixMilli" example:"1795032000000"`
	Diagnostic           string `json:"diagnostic,omitempty" example:"WAITING_FOR_STOP"`
	Replayed             bool   `json:"-"`
}

type SourceResetScheduleRuntime interface {
	ConfigureResetScheduleTx(context.Context, *gorm.DB, *model.User, ResetScheduleRequest) (ResetScheduleView, error)
	CancelResetScheduleTx(context.Context, *gorm.DB, *model.User, ResetScheduleCancelRequest) (ResetScheduleView, error)
}

type ResetScheduleCancelRequest struct {
	OperationID        string `json:"operationId" validate:"required,uuid" example:"11111111-1111-4111-8111-111111111111"`
	ClientID           string `json:"clientId" validate:"required,uuid" example:"22222222-2222-4222-8222-222222222222"`
	PendingOperationID string `json:"pendingOperationId" validate:"required,uuid" example:"33333333-3333-4333-8333-333333333333"`
	ExpectedRevision   uint64 `json:"expectedRevision" validate:"gt=0" example:"1"`
}
