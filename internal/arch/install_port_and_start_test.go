package arch

import (
	"strings"
	"testing"
)

/*
The installer must not hand the panel a port something else already holds.

is_port_in_use has lived in this file since the ACME listener needed it, and the
random panel port was drawn straight out of shuf without it. Roughly one install
in a few hundred lands on an occupied port; the panel then fails to bind at every
start, and the installer has already printed that port as the access URL and
written it into install-result.env, so nothing anywhere names the real reason.

A port the operator typed themselves is still theirs -- it is not silently
replaced -- but an occupied one is said out loud. F164.
*/
func TestTheInstallerDrawsAPortNothingIsListeningOn(t *testing.T) {
	install := readRepoScript(t, "install.sh")

	if strings.Contains(install, "config_port=$(shuf -i") {
		t.Error("the panel port is still drawn straight from shuf, with no availability check, while is_port_in_use sits in the same file")
	}
	if !strings.Contains(install, "random_free_port() {") {
		t.Fatal("random_free_port is gone; this guard is now vacuous")
	}
	body := shellFunctionBody(t, install, "random_free_port")
	if !strings.Contains(body, "is_port_in_use") {
		t.Error("random_free_port does not ask whether the port is free, which is the whole of its job")
	}
	if !strings.Contains(body, "while") {
		t.Error("random_free_port draws once and gives up; a single collision then reinstates the bug it exists to remove")
	}
}

/*
And it must not announce a panel that never came up.

`systemctl start` returns as soon as the process is forked, so a panel that dies
on a bad DSN, an occupied port or a broken unit leaves systemd inactive a moment
later. The installer went straight on to "installation finished, it is running
now", printed an access URL for it, and persisted the whole lot into
install-result.env for the MOTD to repeat -- every one of them false, with the
journal the only place the reason existed and nothing pointing at it. F164.
*/
func TestTheInstallerChecksThePanelActuallyStarted(t *testing.T) {
	install := readRepoScript(t, "install.sh")

	start := strings.Index(install, "systemctl start p-ui\n")
	if start < 0 {
		t.Fatal("the install-time start was renamed; this guard is now vacuous")
	}
	after := install[start:]
	if !strings.Contains(after, "systemctl is-active --quiet p-ui") {
		t.Error("nothing checks the unit after the start, so a panel that never came up is announced as running")
	}
	if !strings.Contains(after, "journalctl -u p-ui") {
		t.Error("a failed start prints no journal, leaving the operator with a panel that is not there and no reason given")
	}
	if !strings.Contains(install, `if [[ "${PANEL_START_FAILED:-0}" == "1" ]]; then`) {
		t.Error("the closing message does not depend on whether the panel started")
	}
}
