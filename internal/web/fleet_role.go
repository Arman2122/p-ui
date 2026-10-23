package web

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/web/job"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

var ErrFleetRoleServices = errors.New("fleet role requires owned runtime and scheduled business handlers")

type FleetRoleJobs struct {
	GroupMembership      cron.Job
	LDAP                 cron.Job
	InboundTrafficReset  map[string]cron.Job
	OutboundSubscription cron.Job
	Stats                cron.Job
}

type FleetRoleServices struct {
	Runtime runtime.Runtime
	Jobs    FleetRoleJobs
}

type ServerOption func(*Server)

func WithFleetRoleServices(services FleetRoleServices) ServerOption {
	return func(s *Server) { s.fleetServices = &services }
}

func missingFleetHandler(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func (s *Server) prepareRole() error {
	role, err := config.RoleFromEnv()
	if err != nil {
		return err
	}
	s.role = role
	if err := role.ValidatePanel(); err != nil {
		return err
	}
	if !role.ManagementOnly() {
		return nil
	}
	if s.fleetServices == nil {
		return ErrFleetRoleServices
	}
	jobs := s.fleetServices.Jobs
	checks := []struct {
		name  string
		value any
	}{
		{"runtime", s.fleetServices.Runtime},
		{"groups", jobs.GroupMembership},
		{"ldap", jobs.LDAP},
		{"outbound subscriptions", jobs.OutboundSubscription},
		{"stats", jobs.Stats},
	}
	for _, period := range []string{"hourly", "daily", "weekly", "monthly"} {
		checks = append(checks, struct {
			name  string
			value any
		}{"inbound display reset " + period, jobs.InboundTrafficReset[period]})
	}
	for _, check := range checks {
		if missingFleetHandler(check.value) {
			return fmt.Errorf("%w: %s", ErrFleetRoleServices, check.name)
		}
	}
	manager, err := runtime.NewFleetManager(s.fleetServices.Runtime)
	if err != nil {
		return err
	}
	runtime.SetManager(manager)
	return nil
}

func (s *Server) startFleetTasks() {
	jobs := s.fleetServices.Jobs
	_, _ = s.cron.AddJob("@hourly", &job.ActivityPruneJob{})
	_, _ = s.cron.AddJob("@every 5m", job.NewClientHistoryJob())
	_, _ = s.cron.AddJob("@every 1m", job.NewBackupJob())
	_, _ = s.cron.AddJob("@every 1m", jobs.GroupMembership)
	_, _ = s.cron.AddJob(cadenceOutboundSub, jobs.OutboundSubscription)
	for period, spec := range map[string]string{"hourly": "@hourly", "daily": "@daily", "weekly": "@weekly", "monthly": "@daily"} {
		_, _ = s.cron.AddJob(spec, jobs.InboundTrafficReset[period])
	}
	s.rescheduleSettingJobs()
}

func (s *Server) ldapJob() cron.Job {
	if s.role.ManagementOnly() {
		return s.fleetServices.Jobs.LDAP
	}
	return job.NewLdapSyncJob()
}

func (s *Server) statsNotifyJob() cron.Job {
	if s.role.ManagementOnly() {
		return s.fleetServices.Jobs.Stats
	}
	return job.NewStatsNotifyJob()
}

func (s *Server) startRoleTasks(restartXray bool, loc *time.Location) {
	if s.role.ManagementOnly() {
		s.startFleetTasks()
		return
	}
	s.startTask(restartXray, loc)
}
