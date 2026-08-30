package job

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
LDAP may only re-enable clients it actually governs.

The job folded EVERY inbound's clients into one panel-wide email index and then
decided enable/disable straight off it, with no test of whether the client is on
any of the inbounds the operator placed under LDAP control. So an identity the
directory allows was queued for enabling on every configured tag, wherever it
actually lived.

The consequence is that an administrator has no way to disable such a client.
They switch it off; the sync -- default every minute -- switches it back on;
they switch it off again. There is no workaround short of turning LDAP off
entirely, and nothing in the panel explains what is undoing their change.

The DISABLE direction deliberately stays panel-wide: narrowing it would fail
OPEN, leaving an off-boarded account working wherever it sits outside the
configured tags. That is a scope decision, and it is deferred rather than taken
here. F369.
*/
func TestLdapOnlyEnablesClientsOnTheInboundsItGoverns(t *testing.T) {
	flags := map[string]bool{
		"governed@corp":   true,
		"elsewhere@corp":  true,
		"offboarded@corp": false,
	}
	allClients := map[string]*model.Client{
		"governed@corp":   {Email: "governed@corp", Enable: false},
		"elsewhere@corp":  {Email: "elsewhere@corp", Enable: false},
		"offboarded@corp": {Email: "offboarded@corp", Enable: true},
	}
	membership := map[string]map[string]bool{
		"ldap-inbound": {"governed@corp": true, "offboarded@corp": true},
		"other":        {"elsewhere@corp": true},
	}

	got := planLdapChanges(flags, allClients, membership, []string{"ldap-inbound"})

	if !contains(got.enable["ldap-inbound"], "governed@corp") {
		t.Errorf("a disabled client the directory allows, ON the governed inbound, was not enabled: %v", got.enable)
	}
	if contains(got.enable["ldap-inbound"], "elsewhere@corp") {
		t.Errorf(`LDAP queued an enable for a client that is not on any inbound it governs.
An administrator disabling that client is overruled on the next tick, every minute, with no way to stop it: %v`, got.enable)
	}

	// The other direction is unchanged and must stay that way.
	if !contains(got.disable["ldap-inbound"], "offboarded@corp") {
		t.Errorf("an off-boarded client was not disabled: %v", got.disable)
	}
}

func TestLdapStillCreatesAnUnknownIdentity(t *testing.T) {
	got := planLdapChanges(
		map[string]bool{"new@corp": true, "denied@corp": false},
		map[string]*model.Client{},
		map[string]map[string]bool{"ldap-inbound": {}},
		[]string{"ldap-inbound"},
	)
	if !contains(got.create, "new@corp") {
		t.Errorf("an allowed identity with no client anywhere was not queued for creation: %v", got.create)
	}
	if contains(got.create, "denied@corp") {
		t.Errorf("an identity the directory denies was queued for creation: %v", got.create)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
