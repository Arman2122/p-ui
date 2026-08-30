package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/logger"

	"github.com/op/go-logging"
)

/*
LDAP without TLS must be reported, the way every other posture is.

The panel offers two LDAP transports: plain ldap:// and implicit ldaps://.
There is no StartTLS anywhere in the LDAP client, so an unencrypted connection
cannot be upgraded after the fact -- unlike SMTP, which opportunistically
upgrades. The shipped default port is 389.

So with ldapUseTLS off, every account identifier the sync reads crosses the
network in clear text on every run of the schedule; if a bind DN is configured
its password goes the same way; and if panel sign-in through LDAP is on, so does
every administrator password.

Reported, never refused -- the same contract as the two postures beside it.
Refusing to start would lock out an operator whose only administrator account is
in the directory, which is the failure family the lockout work exists to prevent.

The clauses are gated on their own preconditions rather than printed as one
block: a message that claims a bind password is exposed on a panel that has no
bind DN is a message operators learn to ignore. F376.
*/
func TestLdapWithoutTlsIsReported(t *testing.T) {
	initTestDB(t)
	logger.InitLogger(logging.INFO)

	// Through the service, not a bare Save: Setting is keyed on Id, so a raw
	// Save INSERTS a second row for the same key instead of updating it.
	svc := &SettingService{}
	seed := func(kv map[string]string) {
		t.Helper()
		for k, v := range kv {
			if err := svc.saveSetting(k, v); err != nil {
				t.Fatalf("seed %s: %v", k, err)
			}
		}
	}

	// The log buffer is process-wide and cumulative, so take the most recent
	// cleartext line. GetLogs returns NEWEST FIRST, so that is the first match.
	posture := func() string {
		svc.LogAuthPosture()
		for _, line := range logger.GetLogs(1000, "error") {
			if strings.Contains(line, "ldapUseTLS off") {
				return line
			}
		}
		return ""
	}

	t.Run("cleartext with a bind DN and panel sign-in names all three", func(t *testing.T) {
		seed(map[string]string{
			"ldapEnable":      "true",
			"ldapUseTLS":      "false",
			"ldapBindDN":      "cn=svc,dc=example,dc=com",
			"ldapAdminFilter": "(memberOf=cn=admins,dc=example,dc=com)",
		})
		out := posture()
		if !strings.Contains(out, "ldapUseTLS off") {
			t.Fatalf("LDAP running in cleartext was not reported at all:\n%s", out)
		}
		if !strings.Contains(out, "bind DN is configured") {
			t.Errorf("the bind password exposure was not named although a bind DN is set:\n%s", out)
		}
		if !strings.Contains(out, "administrator password") {
			t.Errorf("panel sign-in through LDAP is on and administrator passwords were not named:\n%s", out)
		}
	})

	t.Run("no bind DN and no panel sign-in claims neither", func(t *testing.T) {
		seed(map[string]string{
			"ldapEnable":      "true",
			"ldapUseTLS":      "false",
			"ldapBindDN":      "",
			"ldapAdminFilter": "",
		})
		out := posture()
		if !strings.Contains(out, "ldapUseTLS off") {
			t.Fatalf("the base cleartext warning must still fire:\n%s", out)
		}
		if strings.Contains(out, "bind DN is configured") {
			t.Errorf("a bind password was claimed exposed on a panel with no bind DN; operators learn to ignore messages like that:\n%s", out)
		}
		if strings.Contains(out, "administrator password") {
			t.Errorf("administrator passwords were claimed exposed although panel sign-in through LDAP is off:\n%s", out)
		}
	})

	t.Run("ldaps says nothing", func(t *testing.T) {
		seed(map[string]string{"ldapEnable": "true", "ldapUseTLS": "true", "ldapAdminFilter": "(x=y)"})
		// Nothing NEW is added, which with a cumulative buffer means the last
		// cleartext line is the one an earlier subtest left behind.
		before := posture()
		if out := posture(); out != before {
			t.Errorf("an encrypted LDAP connection was reported as cleartext:\n%s", out)
		}
	})
}
