package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
An update must not tear the panel down before it has what it needs.

update.sh stopped p-ui, disabled it, deleted the systemd unit and reloaded the
daemon — and only then fetched the management script from GitHub. On a host that
cannot reach raw.githubusercontent, which is a great many of the hosts this
panel is installed on, one 403 left the box with the panel stopped, no unit and
no autostart, in a state that survives a reboot and needs manual systemctl
surgery over SSH.

install.sh has always staged that file from the release archive it already
downloaded, before touching the running install. This pins that update.sh does
the same, because the two drifted apart once and nothing in the build noticed.
*/
func TestTheUpdaterStagesItsScriptBeforeTearingDown(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "update.sh"))
	if err != nil {
		t.Fatalf("read update.sh: %v", err)
	}
	// Scoped to the update function: the script stops the panel in several
	// places, and comparing against the first one anywhere answers nothing.
	text := updateFunctionBody(t, string(body))

	stage := strings.Index(text, `pui_script_temp="/usr/bin/p-ui-temp.`)
	stop := strings.Index(text, "systemctl stop p-ui > /dev/null 2>&1")
	if stage < 0 || stop < 0 {
		t.Fatal("update_p-ui no longer stages a script or no longer stops the panel; this guard is now vacuous")
	}
	if stage > stop {
		t.Fatal("update.sh stages its management script after stopping the panel: a fetch that fails there leaves the box torn down with no unit and no autostart")
	}

	// And a failed refresh must not be fatal — the old copy still works.
	if strings.Contains(text, `_fail "ERROR: Failed to download p-ui.sh script`) {
		t.Fatal("a failed p-ui.sh refresh still aborts the update; the panel is mid-upgrade and the existing script is serviceable")
	}
}

/*
And an unattended update must not reconfigure TLS.

prompt_and_setup_ssl reads from stdin. At EOF the read returns empty and the
branch below it forces the IP-certificate path — so a scripted update on a panel
behind the operator's own reverse proxy contended port 80 with it, issued a
six-day certificate and flipped the panel to direct TLS, unasked.
*/
func TestTheUpdaterOnlyReconfiguresTlsInteractively(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "update.sh"))
	if err != nil {
		t.Fatalf("read update.sh: %v", err)
	}
	text := string(body)

	call := strings.Index(text, "prompt_and_setup_ssl ")
	if call < 0 {
		t.Fatal("update.sh no longer offers SSL setup; this guard is now vacuous")
	}
	// The gate is the interactive test on the branch that reaches the prompt.
	if !strings.Contains(text, `if [[ -z "$existing_cert" && -t 0 ]]; then`) {
		t.Fatal("the SSL prompt is not gated on an interactive session: at EOF the read returns empty and the IP-certificate branch runs unasked")
	}
}

// updateFunctionBody returns update_p-ui's body, from its opening line to the
// next top-level closing brace.
func updateFunctionBody(t *testing.T, text string) string {
	t.Helper()
	const opener = "update_p-ui() {"
	start := strings.Index(text, opener)
	if start < 0 {
		t.Fatal("update_p-ui was renamed; this guard is now vacuous")
	}
	rest := text[start+len(opener):]
	if end := strings.Index(rest, "\n}"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// A bare `rm <folder> -f` cannot remove a directory: it fails with EISDIR, the
// error was discarded, and the line read as "wipe the install folder" while the
// per-file removals below it did the actual work. The folder is kept on
// purpose -- it holds geo data and the module source -- so the line was
// misleading twice over. F166.
func TestTheUpdaterDoesNotPretendToWipeTheInstallFolder(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "update.sh"))
	if err != nil {
		t.Fatalf("read update.sh: %v", err)
	}
	text := updateFunctionBody(t, string(body))
	if strings.Contains(text, "rm ${pui_folder} -f") {
		t.Fatal("update.sh carries `rm ${pui_folder} -f`: a non-recursive rm of a directory is a silent no-op that reads as a wipe")
	}
}
