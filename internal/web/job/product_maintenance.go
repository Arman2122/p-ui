package job

import (
	"context"
	"time"

	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/service"
)

type ClientHistoryJob struct{}

func NewClientHistoryJob() *ClientHistoryJob { return &ClientHistoryJob{} }

func (j *ClientHistoryJob) Run() {
	if err := service.RecordClientHistory(time.Now()); err != nil {
		logger.Warning("record client history failed:", err)
	}
}

type GroupMembershipJob struct{}

func NewGroupMembershipJob() *GroupMembershipJob { return &GroupMembershipJob{} }

func (j *GroupMembershipJob) Run() {
	if err := (&service.ClientService{}).ConvergeGroupMemberships(); err != nil {
		logger.Warning("group membership convergence failed:", err)
	}
}

// Runtime mutations may enqueue traffic writes, so callers must release the writer first.
func enforceProductQuotas(ctx context.Context) {
	if err := (&service.ResellerService{}).EnforceBudgets(ctx); err != nil {
		logger.Warning("reseller budget enforcement failed:", err)
	}
	if err := (&service.ClientService{}).EnforceGroupQuotas(); err != nil {
		logger.Warning("group quota enforcement failed:", err)
	}
}
