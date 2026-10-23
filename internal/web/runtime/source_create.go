package runtime

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

type SourceCreateRuntime interface {
	CreateClientTx(context.Context, *gorm.DB, *model.User, entity.ClientCreatePayload) (entity.FleetClientCreateResult, error)
}
