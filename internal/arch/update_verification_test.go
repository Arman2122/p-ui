package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
Nothing that runs as root may come from the tip of main, and no release archive
may be extracted unverified.

Three places ran `main/update.sh` as root on every host -- the panel's Update
button, `p-ui update`, and node remote-update -- and the script then pulled the
release archive with no digest. A commit on main, or a tampered CDN object, was
code execution on every panel. F277.

The updater now ships as an asset of the release it installs, every archive has
a digest beside it, and both scripts refuse an archive they cannot match. These
guards pin each half by text, so a convenience edit cannot quietly restore the
old path.
*/
func TestUpdatePathIsPinnedToTheRelease(t *testing.T) {
	root := repoRoot(t)

	for _, file := range []string{"p-ui.sh", "update.sh", "install.sh", "internal/web/service/panel/panel.go"} {
		t.Run(file, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			if strings.Contains(string(body), "/main/update.sh") {
				t.Fatalf("%s fetches update.sh from the main branch again: that script runs as root on every host, and main is a branch anyone with push can move", file)
			}
		})
	}
}

func TestInstallersVerifyTheReleaseArchive(t *testing.T) {
	root := repoRoot(t)

	for _, script := range []string{"install.sh", "update.sh"} {
		t.Run(script, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, script))
			if err != nil {
				t.Fatalf("read %s: %v", script, err)
			}
			text := string(body)
			if !strings.Contains(text, "verify_release_archive() {") {
				t.Fatalf("%s no longer defines verify_release_archive; this guard is now vacuous", script)
			}
			if strings.Count(text, "verify_release_archive \"") == 0 {
				t.Fatalf("%s defines verify_release_archive and never calls it, so an archive is extracted unverified", script)
			}
			// Fail-closed is the property: a digest the script cannot fetch must
			// be a refusal with an explicit escape, never a silent pass.
			if !strings.Contains(text, "PUI_SKIP_CHECKSUM") {
				t.Fatalf("%s lost its explicit checksum escape, which means a missing digest is either fatal with no recourse or silently ignored", script)
			}
		})
	}
}

func TestReleasePublishesDigestsAndTheUpdater(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	text := string(body)

	for _, want := range []string{
		"asset_name: p-ui-linux-${{ matrix.platform }}.tar.gz.sha256",
		"asset_name: update.sh",
		"dev-artifacts/*.sha256 update.sh",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("release.yml no longer contains %q: the scripts verify against an asset the release then does not publish, so every install refuses", want)
		}
	}
}
