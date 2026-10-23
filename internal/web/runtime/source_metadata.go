package runtime

import (
	"context"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

type SourceMetadataRuntime interface {
	EditClientMetadataTx(context.Context, *gorm.DB, *model.User, entity.FleetClientMetadataRequest) (entity.FleetClientMetadataResult, error)
}
