package arch

import (
	"strings"
	"testing"
)

/*
Re-running the PostgreSQL setup must not rotate a password the panel is using.

pg_install_local minted a fresh password on entry and ALTERed the role to it
unconditionally -- including the existing_owner branch, which picks up a role
this run did not create. pg_install_server_action only asks whether to apply the
new DSN afterwards, so declining left the panel's stored PUI_DB_DSN carrying the
old password. Pooled connections survive that, so nothing looks wrong until the
next restart, when the panel fails authentication with no visible cause. The
only gate on the way in is a confirm whose text is about idempotence -- "ensures
the pui database/user exist" -- and never mentions a password at all.

install.sh's mirror is exempt: it reuses an existing DSN when one is set and
always writes the DSN it provisioned, so a rotation there always ships with the
credential that matches it.

Shell has no other test leverage. The behaviour was proved separately by driving
pg_install_local against stubbed psql/sudo: unfixed it issues ALTER USER and
returns a new DSN, fixed it issues none and returns the panel's working one.
F160.
*/
func TestReRunningPgSetupKeepsAWorkingPassword(t *testing.T) {
	text := readPuiScript(t)
	body := shellFunctionBody(t, text, "pg_install_local")

	alter := "ALTER USER"
	if !strings.Contains(body, alter) {
		t.Fatal("pg_install_local no longer sets a role password at all; this guard is now vacuous")
	}

	// The ALTER must sit inside the else of the reuse branch, not at statement
	// level where every re-run reaches it.
	reuse := strings.Index(body, "keep_dsn=$(pg_stored_dsn_if_working")
	if reuse < 0 {
		t.Fatal("pg_install_local no longer consults the panel's stored DSN before rotating; a re-run over a live panel schedules an outage for its next restart")
	}
	if strings.Index(body, alter) < reuse {
		t.Fatal("pg_install_local ALTERs the role password before it checks whether the panel is already authenticating with it")
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "sudo -u postgres psql -c") && strings.Contains(trimmed, alter) {
			// Guarded lines are indented inside the if/else; an unguarded one is
			// at the function's own 4-space level.
			if strings.HasPrefix(line, "    sudo") {
				t.Fatal("the ALTER USER is back at statement level in pg_install_local: every re-run rotates the password the running panel holds")
			}
		}
	}

	// And the helper must actually verify, not merely read the file.
	helper := shellFunctionBody(t, text, "pg_stored_dsn_if_working")
	if !strings.Contains(helper, "current_database()") {
		t.Fatal("pg_stored_dsn_if_working no longer connects with the stored DSN; a DSN that merely exists is not proof the password still works")
	}

	// Declining the DSN prompt must say what declining costs.
	action := shellFunctionBody(t, text, "pg_install_server_action")
	if !strings.Contains(action, "NEXT restart") {
		t.Fatal("pg_install_server_action no longer warns that a rotated password strands the panel at its next restart; an operator whose DSN lives outside the env file gets no other signal")
	}
}
