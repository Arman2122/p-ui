package arch

import (
	"os/exec"
	"strings"
	"testing"
)

/*
A script the release tarball ships must be executable in git, and nothing may
gate on that bit before setting it.

awg-dkms.sh was tracked 100644. release.yml builds the tarball with `cp`, which
carries the checked-out mode, so every host received a non-executable script --
and both install.sh and update.sh gated the AmneziaWG build on
`[[ -x awg-dkms.sh ]]`. That test was false on every host, forever, so the
kernel module was never built anywhere and the panel honestly reported the core
unavailable. The `chmod +x` that would have repaired it sat INSIDE the block the
gate guarded.

p-ui.sh is tracked 644 too and works, because its chmod is unconditional. That
is the difference this guard pins: either ship the bit, or set it before you
test it -- and preferably both.
*/
/*
Asked of git, and SKIPPED where there is no git to ask.

The mode is a property of the index, not of the working tree -- on Windows every
file reads 0755 on disk, which is exactly why this shipped wrong. A checkout
copied without .git (the VM's rsync tree) can still run every other guard.
*/
func gitFileMode(t *testing.T, path string) (string, bool) {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-s", "--", path)
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", false
	}
	return fields[0], true
}

// Every script the release archive copies in and then executes.
var shippedExecutables = []string{"awg-dkms.sh"}

func TestShippedScriptsAreExecutableInGit(t *testing.T) {
	for _, name := range shippedExecutables {
		mode, ok := gitFileMode(t, name)
		if !ok {
			t.Skip("no git index here; this guard reads the tracked mode, not the file on disk")
		}
		if mode != "100755" {
			t.Errorf(
				"%s is tracked %s, but release.yml copies it into the tarball with `cp`,\n"+
					"which carries that mode. Every host then receives it non-executable.\n"+
					"Fix with: git update-index --chmod=+x %s",
				name, mode, name)
		}
	}
}

/*
And the installers must not TEST the bit before setting it.

Gating on -x makes the repair unreachable: a host that already has a
non-executable copy -- every host installed before this was fixed -- can never
be repaired by an upgrade, because the upgrade skips the block containing the
chmod. Gate on -f, chmod, then run.
*/
func TestTheInstallersDoNotGateOnTheExecutableBitTheyThenSet(t *testing.T) {
	for _, script := range []string{"install.sh", "update.sh"} {
		body := readRepoScript(t, script)
		for _, name := range shippedExecutables {
			for _, gate := range []string{"[[ -x " + name, "[[ -x ${pui_folder}/" + name} {
				if strings.Contains(body, gate) {
					t.Errorf(
						"%s gates on `%s ...`, and sets that bit inside the block it guards.\n"+
							"A host whose tarball delivered the script non-executable can never be\n"+
							"repaired by an upgrade. Gate on -f instead, chmod, then run.",
						script, gate)
				}
			}
		}
	}
}

/*
An upgrade must refresh the vendored payload, not rebuild last release's copy.

update.sh built from ${pui_folder}/amneziawg without ever replacing it, so a
panel installed before AmneziaWG shipped had nothing to build however often it
updated -- and one that did have it compiled the OLD source forever, while the
release notes said the module had moved on.
*/
func TestTheUpgradeRefreshesTheVendoredModuleSource(t *testing.T) {
	body := readRepoScript(t, "update.sh")
	if !strings.Contains(body, "cp -a amneziawg ${pui_folder}/amneziawg") {
		t.Error("update.sh never copies the new release's amneziawg source over the installed one, so an upgrade rebuilds the previous release's module")
	}
	if !strings.Contains(body, "cp -f awg-dkms.sh ${pui_folder}/awg-dkms.sh") {
		t.Error("update.sh never refreshes awg-dkms.sh, so a fix to that script never reaches an existing install")
	}
}
