package web

import (
	"errors"
	"testing"

	"github.com/robfig/cron/v3"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

type fleetRoleRuntime struct{ runtime.Runtime }

func fleetRoleTestServices() FleetRoleServices {
	job := cron.FuncJob(func() {})
	return FleetRoleServices{
		Runtime: &fleetRoleRuntime{},
		Jobs: FleetRoleJobs{
			GroupMembership: job, LDAP: job, OutboundSubscription: job, Stats: job,
			InboundTrafficReset: map[string]cron.Job{"hourly": job, "daily": job, "weekly": job, "monthly": job},
		},
	}
}

func TestFleetStartupRejectsIncompleteServicesBeforeDatabase(t *testing.T) {
	t.Setenv("PUI_DB_DSN", "")
	for _, role := range []string{"master", "combined"} {
		t.Run(role, func(t *testing.T) {
			t.Setenv("PUI_ROLE", role)
			for _, start := range []func(*Server) error{(*Server).Start, (*Server).StartPanelOnly} {
				s := NewServer()
				if err := start(s); !errors.Is(err, ErrFleetRoleServices) {
					t.Fatalf("missing role services reached startup: %v", err)
				}
				if s.supervisor != nil || s.cores != nil || s.cron != nil || s.localRuntimeStarted {
					t.Fatal("incomplete fleet startup initialized local execution")
				}
				s.cancel()
			}
		})
	}
	t.Setenv("PUI_ROLE", "node")
	s := NewServer()
	defer s.cancel()
	if err := s.Start(); !errors.Is(err, config.ErrNodeRole) {
		t.Fatalf("node startup did not fail before database access: %v", err)
	}
}

func TestFleetRoleRequiresEveryOwnedHandler(t *testing.T) {
	t.Setenv("PUI_ROLE", "master")
	tests := []struct {
		name string
		omit func(*FleetRoleServices)
	}{
		{"runtime", func(s *FleetRoleServices) { s.Runtime = nil }},
		{"typed nil runtime", func(s *FleetRoleServices) { s.Runtime = (*fleetRoleRuntime)(nil) }},
		{"groups", func(s *FleetRoleServices) { s.Jobs.GroupMembership = nil }},
		{"ldap", func(s *FleetRoleServices) { s.Jobs.LDAP = nil }},
		{"outbound", func(s *FleetRoleServices) { s.Jobs.OutboundSubscription = nil }},
		{"stats", func(s *FleetRoleServices) { s.Jobs.Stats = nil }},
		{"inbound display reset", func(s *FleetRoleServices) { delete(s.Jobs.InboundTrafficReset, "monthly") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			services := fleetRoleTestServices()
			tt.omit(&services)
			s := NewServer(WithFleetRoleServices(services))
			defer s.cancel()
			if err := s.prepareRole(); !errors.Is(err, ErrFleetRoleServices) {
				t.Fatalf("missing owned handler accepted: %v", err)
			}
		})
	}
}

func TestCombinedRoleUsesFleetDispatcherWithoutLocalCore(t *testing.T) {
	t.Setenv("PUI_ROLE", "combined")
	previous := runtime.GetManager()
	t.Cleanup(func() { runtime.SetManager(previous) })
	services := fleetRoleTestServices()
	s := NewServer(WithFleetRoleServices(services))
	defer s.cancel()
	if err := s.prepareRole(); err != nil {
		t.Fatal(err)
	}
	dispatch, err := runtime.GetManager().RuntimeFor(nil)
	if err != nil || dispatch != services.Runtime {
		t.Fatalf("combined did not select fleet dispatcher: %v", err)
	}
	if s.cores != nil || s.supervisor != nil || s.localRuntimeStarted {
		t.Fatal("combined constructed a local execution path")
	}
	if err := s.RestartXray(); err == nil || err.Error() != "fleet roles manage core restarts through node operations" {
		t.Fatalf("combined permitted local Xray restart: %v", err)
	}
}
