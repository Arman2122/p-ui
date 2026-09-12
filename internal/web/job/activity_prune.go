package job

import (
	"context"
	"time"

	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/service"
)

type ActivityPruneJob struct{}

func (j *ActivityPruneJob) Run() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := (service.ActivityService{}).Prune(ctx, time.Now()); err != nil {
		logger.Warning("prune activity:", err)
	}
}
