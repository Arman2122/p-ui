package job

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/service/tgbot"
)

type FleetStatsNotifyJob struct {
	db      *gorm.DB
	ctx     context.Context
	running sync.Mutex
}

func NewFleetStatsNotifyJob(ctx context.Context, db *gorm.DB) (*FleetStatsNotifyJob, error) {
	if db == nil || ctx == nil {
		return nil, errors.New("fleet statistics require a database and lifecycle context")
	}
	return &FleetStatsNotifyJob{db: db, ctx: ctx}, nil
}

func (j *FleetStatsNotifyJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()
	ctx, cancel := context.WithTimeout(j.ctx, 35*time.Minute)
	defer cancel()
	if err := (&tgbot.Tgbot{}).SendFleetReport(ctx, j.db); err != nil {
		logger.Warning("fleet statistics report failed: ", err)
	}
}
