package job

import (
	"time"

	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/service"
)

type BackupJob struct{ service service.BackupService }

func NewBackupJob() *BackupJob { return &BackupJob{} }

func (j *BackupJob) Run() {
	if err := j.service.RunScheduled(time.Now()); err != nil {
		logger.Error("automatic backup failed: ", err)
	}
}
