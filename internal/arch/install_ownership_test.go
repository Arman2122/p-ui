package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

/*
The release archive is built by a non-root CI user, and both scripts extract it
as root into /usr/local/p-ui. GNU tar as root restores the archive's uid/gid, so
a plain `tar zxvf` leaves every binary the panel execs as root — xray, mtg — plus
the geo assets and the management script owned by that uid. On a stock Debian or
Ubuntu box that is 1001, the second ordinary account: whoever holds it can
replace what root runs.

Two independent guarantees, because they fail in different places: the flag stops
it happening, and the chown repairs a tree that already carries it.
*/
func TestInstallScriptsDoNotHandTheTreeToTheArchivesUid(t *testing.T) {
	root := repoRoot(t)

	// Matches the release-archive extraction specifically, not the read-only
	// `tar tzf` / `tar xzf -O` probes that inspect it without unpacking.
	extract := regexp.MustCompile(`tar\s+[^\n|]*-?z?xv?f\s+p-ui-linux`)

	for _, script := range []string{"install.sh", "update.sh"} {
		t.Run(script, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, script))
			if err != nil {
				t.Fatalf("read %s: %v", script, err)
			}
			text := string(body)

			found := extract.FindAllString(text, -1)
			if len(found) == 0 {
				t.Fatalf("%s no longer extracts the release archive; this guard is now vacuous", script)
			}
			for _, line := range found {
				if !strings.Contains(line, "--no-same-owner") {
					t.Fatalf("%s extracts the release archive without --no-same-owner (%q): as root that restores the archive's uid onto every binary the panel execs",
						script, line)
				}
			}

			if !strings.Contains(text, "chown -R root:root ${pui_folder}") {
				t.Fatalf("%s never chowns the install tree: a tree that already carries the archive's uid is never repaired",
					script)
			}
		})
	}
}
