package web

import (
	"testing"

	"github.com/robfig/cron/v3"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/job"
	"github.com/Arman2122/p-ui/internal/web/service"
)

/*
Turning LDAP on used to do nothing until the panel was restarted.

The sync job was registered once inside startTask, gated on the value of
ldapEnable read at process start, and nothing re-read it -- while the
Telegram bot in the same handler had a reload hook and the alert watchdogs
had rescheduleSettingJobs. The settings page said yes and scheduled nothing:
every directory-disabled account kept working until someone restarted. F374.

LDAP now rides the same rescheduler the watchdogs do, so each save converges
the cron on what the settings say.
*/
func TestEnablingLdapSchedulesTheSyncWithoutARestart(t *testing.T) {
	initTestDB(t)
	s := &Server{cron: cron.New(cron.WithSeconds()), settingService: service.SettingService{}}

	s.rescheduleSettingJobs()
	if hasLdapSync(s.cron) {
		t.Fatal("LDAP sync scheduled while ldapEnable is off")
	}

	setSetting(t, "ldapEnable", "true")
	s.rescheduleSettingJobs()
	if !hasLdapSync(s.cron) {
		t.Fatal("ldapEnable was turned on and nothing was scheduled: the sync waits for a restart")
	}

	setSetting(t, "ldapEnable", "false")
	s.rescheduleSettingJobs()
	if hasLdapSync(s.cron) {
		t.Fatal("ldapEnable was turned off and the sync stayed scheduled")
	}
}

func hasLdapSync(c *cron.Cron) bool {
	for _, entry := range c.Entries() {
		if _, ok := entry.Job.(*job.LdapSyncJob); ok {
			return true
		}
	}
	return false
}

// setSetting writes one settings row by key. Settings are keyed on Id, so a
// bare Save would insert a duplicate; this updates the row when it exists.
func setSetting(t *testing.T, key, value string) {
	t.Helper()
	err := database.GetDB().Where("key = ?", key).
		Assign(model.Setting{Value: value}).
		FirstOrCreate(&model.Setting{Key: key}).Error
	if err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}
