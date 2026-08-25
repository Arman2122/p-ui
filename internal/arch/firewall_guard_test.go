package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
Enabling a firewall must not be able to lock the operator out.

p-ui.sh had two ways to turn ufw on: install_firewall, which allowed a
hardcoded 2053/2096 that install.sh's shuf-assigned port made wrong on nearly
every box, and menu option 5, which was a bare `ufw enable` with no rules at
all. Either one default-denies incoming on the next packet, and the SSH session
that ran it is the one that dies.

Both entries now read the real ports and refuse unless the operator's own SSH
port is among them. This is shell, so nothing else in the build would notice
if a later edit routed around the guard — that is what this pins.
*/
func TestEveryFirewallEnableGoesThroughTheSshGuard(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "p-ui.sh"))
	if err != nil {
		t.Fatalf("read p-ui.sh: %v", err)
	}
	text := string(body)

	install := shellFunctionBody(t, text, "install_firewall")
	confirm := strings.Index(install, "confirm_firewall_rules ")
	enable := strings.Index(install, "ufw --force enable")
	if enable < 0 {
		t.Fatal("install_firewall no longer enables the firewall; this guard is now vacuous")
	}
	if confirm < 0 {
		t.Fatal("install_firewall enables the firewall without confirm_firewall_rules: nothing checks that the operator's SSH port survives")
	}
	if confirm > enable {
		t.Fatal("install_firewall enables the firewall before confirming the rules: the refusal cannot prevent a lockout it runs after")
	}

	// The menu is the second entry, and it was the bare one. Every enable it
	// offers must go through install_firewall rather than call ufw itself.
	menu := shellFunctionBody(t, text, "firewall_menu")
	for _, bare := range []string{"ufw enable", "ufw --force enable"} {
		if strings.Contains(menu, bare) {
			t.Fatalf("firewall_menu calls %q directly: an enable with no rules locks the operator out whatever the panel's ports are", bare)
		}
	}
	if !strings.Contains(menu, "install_firewall") {
		t.Fatal("firewall_menu no longer reaches install_firewall; this guard is now vacuous")
	}
}

/*
And an SSH port the script could not determine is a refusal, not a default.

sshd is socket-activated on Ubuntu 24.04, so sshd_config's Port can be a port
nothing is bound to. Assuming 22 there enables a firewall that blocks the real
one; refusing costs a support ticket instead of a site visit.
*/
func TestAnUnknownSshPortRefusesRatherThanGuesses(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "p-ui.sh"))
	if err != nil {
		t.Fatalf("read p-ui.sh: %v", err)
	}

	confirm := shellFunctionBody(t, string(body), "confirm_firewall_rules")
	unknown := strings.Index(confirm, `if [[ -z "${ssh_port}" ]]; then`)
	if unknown < 0 {
		t.Fatal("confirm_firewall_rules no longer tests for an undetermined SSH port: an empty port matches no rule and the refusal it should hit is the wrong one")
	}
	if !strings.Contains(confirm[unknown:], "return 1") {
		t.Fatal("an undetermined SSH port does not refuse; the firewall would be enabled without a rule for it")
	}
}

// shellFunctionBody returns one shell function's body, from its opening line to
// the next top-level closing brace.
func shellFunctionBody(t *testing.T, text, name string) string {
	t.Helper()
	opener := name + "() {"
	start := strings.Index(text, opener)
	if start < 0 {
		t.Fatalf("%s was renamed or removed; this guard is now vacuous", name)
	}
	rest := text[start+len(opener):]
	if end := strings.Index(rest, "\n}"); end >= 0 {
		return rest[:end]
	}
	return rest
}
