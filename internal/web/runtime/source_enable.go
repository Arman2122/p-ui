package runtime

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type SourceEnableRequest struct {
	OperationID      string
	ClientID         string
	ExpectedRevision uint64
	Enabled          bool
}

type SourceEnableResult struct {
	ClientID string
	Revision uint64
	Enabled  bool
	Pending  bool
	Targets  int
	Replayed bool `json:"-"`
}

type SourceEnableRuntime interface {
	SetClientEnabledTx(context.Context, *gorm.DB, *model.User, SourceEnableRequest) (SourceEnableResult, error)
}
