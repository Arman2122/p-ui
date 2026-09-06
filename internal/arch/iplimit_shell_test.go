package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

/*
The legacy-jail removal must not damage configuration it does not own.

Two of the old menu's fail2ban paths deleted other people's work. "Uninstall
Fail2ban and IP Limit" ran `rm -rf /etc/fail2ban`, taking every jail, filter and
.local override the operator wrote, while the option above it already removed
p-ui's three files surgically. And iplimit_remove_conflicts deleted from
[p-ui-ipl] to the first BLANK line, which an INI file whose next section follows
immediately does not have, so a routine update ate [sshd] and [recidive].

The removal now lives in three scripts, so each copy is held to the same three
files. Shell has no other test leverage, and both failures are silent.
*/
func TestTheLegacyJailRemovalOnlyDeletesWhatPuiOwns(t *testing.T) {
	own := []string{
		"/etc/fail2ban/jail.d/p-ui-ipl.conf",
		"/etc/fail2ban/filter.d/p-ui-ipl.conf",
		"/etc/fail2ban/action.d/p-ui-ipl.conf",
	}
	touched := regexp.MustCompile(`/etc/fail2ban/[^\s;"']+`)
	for _, script := range []string{"p-ui.sh", "install.sh", "update.sh"} {
		body := shellFunctionBody(t, readShellScript(t, script), "remove_legacy_iplimit_jail")
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "rm -rf") {
				t.Fatalf("%s: remove_legacy_iplimit_jail runs %q; the jail is three files, not a tree", script, strings.TrimSpace(line))
			}
		}
		for _, path := range touched.FindAllString(body, -1) {
			if !slices.Contains(own, path) {
				t.Fatalf("%s: remove_legacy_iplimit_jail touches %s, which p-ui does not own", script, path)
			}
		}
		for _, path := range own {
			if !strings.Contains(body, path) {
				t.Fatalf("%s: remove_legacy_iplimit_jail no longer names %s; the jail would be left half-installed", script, path)
			}
		}
	}

	conflicts := shellFunctionBody(t, readPuiScript(t), "iplimit_remove_conflicts")
	if strings.Contains(conflicts, `,/^$/d`) {
		t.Fatal("iplimit_remove_conflicts deletes from [p-ui-ipl] to the first blank line: with no blank line before the next section that runs to EOF and eats the operator's other jails")
	}
	if !strings.Contains(conflicts, "p-ui-ipl") {
		t.Fatal("iplimit_remove_conflicts no longer names the jail it removes; this guard is now vacuous")
	}
}

/*
And the jail must be configured from the panel's own environment.

p-ui.sh never sourced /etc/default/p-ui — the only source statements in the
whole script were /etc/os-release. So from a plain login shell "Change Ban
Duration" rewrote the action file with no PUI_DB_DSN (losing the panel-port
exemption) and log_folder fell back to /var/log/p-ui while the panel may write
elsewhere, leaving a jail tailing a file nobody writes.

The ORDER is the load-bearing part: every one of those variables reads an
environment the load has to have already performed.
*/
func TestThePuiScriptLoadsItsEnvironmentBeforeDerivingPaths(t *testing.T) {
	text := readPuiScript(t)

	call := strings.Index(text, "\nload_pui_env\n")
	if call < 0 {
		t.Fatal("p-ui.sh does not call load_pui_env at top level: the menu runs with none of PUI_DB_DSN, PUI_LOG_FOLDER or PUI_MAIN_FOLDER set")
	}
	for _, derived := range []string{
		`pui_folder="${PUI_MAIN_FOLDER`,
		`log_folder="${PUI_LOG_FOLDER`,
	} {
		at := strings.Index(text, derived)
		if at < 0 {
			t.Fatalf("%s was renamed; this guard is now vacuous", derived)
		}
		if at < call {
			t.Fatalf("%s is assigned before load_pui_env runs, so it cannot see the panel's environment", derived)
		}
	}
}

/*
And fail2ban comes from the distribution, not from PyPI.

The Ubuntu branch ran `pip install pyasynchat --break-system-packages` — the
only step in the whole fail2ban install that left the distro archive, on a
project whose users are frequently on networks where PyPI is unreachable. It
worked around LP#2055114, fixed in 1.0.2-3ubuntu0.1 (2024-06-27): the shipped
package vendors fail2ban/compat/asynchat.py and Depends on python3-pyasyncore.
*/
func TestFail2banIsInstalledFromTheDistroOnly(t *testing.T) {
	text := readPuiScript(t)
	for _, offender := range []string{"pyasynchat", "break-system-packages", "pip install"} {
		if strings.Contains(text, offender) {
			t.Fatalf("p-ui.sh reaches outside the distro archive (%q); the archive package already carries what fail2ban needs", offender)
		}
	}
}

func readPuiScript(t *testing.T) string {
	t.Helper()
	return readShellScript(t, "p-ui.sh")
}

func readShellScript(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}
