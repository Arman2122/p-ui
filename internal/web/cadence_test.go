package web

import (
	"testing"

	"github.com/robfig/cron/v3"
)

// All centralized background-job cadences must remain valid cron specs. This is
// the guard for the "single tuning surface" refactor: editing a cadence to an
// invalid spec fails here instead of silently dropping a job at startup.
//
// NOTE: package web embeds the built frontend (//go:embed all:dist), so this
// test compiles only after `npm run build` has populated web/dist — the normal
// repo build flow.
func TestJobCadencesAreValidCronSpecs(t *testing.T) {
	cadences := map[string]string{
		"cadenceXrayRunning":   cadenceXrayRunning,
		"cadenceXrayRestart":   cadenceXrayRestart,
		"cadenceXrayTraffic":   cadenceXrayTraffic,
		"cadenceCoreSupervise": cadenceCoreSupervise,
		"cadenceCorePolicy":    cadenceCorePolicy,
		"cadenceNodeHeartbeat": cadenceNodeHeartbeat,
		"cadenceNodeTraffic":   cadenceNodeTraffic,
		"cadenceOutboundSub":   cadenceOutboundSub,
		"cadenceCheckHash":     cadenceCheckHash,
		"cadenceCPUAlarm":      cadenceCPUAlarm,
	}
	for name, spec := range cadences {
		// The SCHEDULER's parser, not ParseStandard: it runs WithSeconds, so a
		// five-field spec passes ParseStandard and is rejected at startup.
		if _, err := cronSpecParser.Parse(spec); err != nil {
			t.Errorf("%s = %q is not a valid cron spec: %v", name, spec, err)
		}
	}
}

/*
An unparseable LDAP schedule must fall back, not silently disable LDAP.

The scheduler is built WithSeconds, so it wants six fields — and every cron
tutorial, and the panel's own settings field, invites the five-field form. A
five-field spec is rejected by AddJob, whose error was discarded with `_, _ =`.

The job was then never scheduled at all, and LDAP off-boarding is fail-open when
it does not run: every account disabled in the directory keeps working on the
panel, forever, with nothing in the UI or the log to say the sync had stopped.

The seeded default is what it falls back to, and the failure is reported at
Error. F373.
*/
func TestAnUnparseableLdapScheduleFallsBackInsteadOfVanishing(t *testing.T) {
	if _, err := cronSpecParser.Parse(defaultLdapSyncCron); err != nil {
		t.Fatalf("the fallback spec itself does not parse: %v", err)
	}

	// The exact shape an operator types from any cron reference.
	const fiveField = "*/5 * * * *"
	if _, err := cronSpecParser.Parse(fiveField); err == nil {
		t.Fatal("a five-field spec now parses; this test can no longer see the bug")
	}

	c := cron.New(cron.WithSeconds())
	scheduleLdapSync(c, fiveField, cron.FuncJob(func() {}))

	if n := len(c.Entries()); n != 1 {
		t.Fatalf("LDAP sync ended up with %d scheduled entries, want 1: an unparseable spec left off-boarding running never, and a disabled directory account keeps working indefinitely", n)
	}
}

// And a spec that IS valid must be used as given, not replaced by the fallback.
func TestAValidLdapScheduleIsHonoured(t *testing.T) {
	c := cron.New(cron.WithSeconds())
	scheduleLdapSync(c, "@every 30m", cron.FuncJob(func() {}))

	if n := len(c.Entries()); n != 1 {
		t.Fatalf("a valid spec produced %d entries, want 1", n)
	}
}
