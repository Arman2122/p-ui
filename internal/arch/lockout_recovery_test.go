package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
A misconfiguration must never be repairable only from the database.

Two settings could lock an operator out of the interface that would fix them:

  - webDomain. A value that does not match how the panel is reached makes every
    request 403 after a restart, including the settings page. There was no CLI
    flag for it, so the only recovery was editing the database by hand. F394.
  - twoFactorEnable with no secret, which -resetTwoFactor already covered and
    which is pinned here so the pair cannot drift.

Asserted on main.go's flag registrations, because the property is "an operator
with a root shell has a documented way back" and the flag IS that way.
*/
func TestEverySettingThatCanLockAnOperatorOutHasACliEscape(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	source := string(body)

	escapes := map[string]string{
		"webDomain":       "a Listen Domain that does not match how the panel is reached 403s every request, settings page included",
		"resetTwoFactor":  "2FA enabled with no secret refuses every login",
		"ldapAdminFilter": "LDAP panel sign-in is off unless this names who may administer",
		"webBasePath":     "a base path nobody knows makes the panel unreachable",
		"port":            "a port that is not what the operator is connecting to",
	}
	for flag, why := range escapes {
		if !strings.Contains(source, `settingCmd.StringVar(&`) && !strings.Contains(source, `settingCmd.BoolVar(&`) {
			t.Fatal("the setting command no longer registers flags; this guard is now vacuous")
		}
		if !strings.Contains(source, `"`+flag+`"`) {
			t.Fatalf("p-ui setting has no -%s flag: %s, and the only way back would be editing the database by hand", flag, why)
		}
	}
}

/*
And a secondary listener that cannot bind must not kill the panel.

subServer.Start's failure was log.Fatalf, reached AFTER the panel's own HTTP
server is already serving — so a port conflict on the subscription port took
down the one interface an operator would use to change that port, and did it
again on every boot. F224.
*/
func TestTheSubscriptionServerCannotTakeThePanelDown(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	source := string(body)

	start := strings.Index(source, "subServer.Start()")
	if start < 0 {
		t.Fatal("main.go no longer starts a subscription server; this guard is now vacuous")
	}
	// The handling that follows the call, up to the next blank-line boundary.
	tail := source[start:]
	if end := strings.Index(tail, "\n\n"); end >= 0 {
		tail = tail[:end]
	}
	if strings.Contains(tail, "log.Fatal") {
		t.Fatal("a subscription server that cannot bind still kills the panel: the panel is the interface an operator would use to change that port, and it goes down on every boot until someone edits the database")
	}
	if !strings.Contains(tail, "logger.Error") {
		t.Fatal("the failure is swallowed entirely; an operator whose subscription links stopped working has nothing to find")
	}
}
