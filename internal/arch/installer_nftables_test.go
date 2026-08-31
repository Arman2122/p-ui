package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

/*
Both installers must install nftables.

The panel writes its own `inet p-ui-nat` table so traffic forwarded out of an L3
inbound leaves carrying the host's own source. EnsureMasquerade tolerates a host
with no nft binary -- it has to, the panel still routes without the rule -- so
on a box without the package the apply is simply skipped and every packet a
wgkernel or awgkernel tunnel forwards is dropped upstream, while the device is
up and handshakes keep completing.

update.sh is guarded as well as install.sh on purpose: a panel installed before
these inbounds existed has no nft, and an upgrade is the only pass that would
ever hand it one.
*/
func TestInstallersInstallNftables(t *testing.T) {
	root := repoRoot(t)
	aptInstall := regexp.MustCompile(`apt-get install[^\n]*`)

	for _, script := range []string{"install.sh", "update.sh"} {
		t.Run(script, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, script))
			if err != nil {
				t.Fatalf("read %s: %v", script, err)
			}
			lines := aptInstall.FindAllString(string(body), -1)
			if len(lines) == 0 {
				t.Fatalf("%s no longer installs any package; this guard is now vacuous", script)
			}
			for _, line := range lines {
				if strings.Contains(line, "nftables") {
					return
				}
			}
			t.Fatalf("%s installs packages but never nftables, so an L3 inbound there "+
				"forwards traffic it cannot source-translate: %q", script, lines)
		})
	}
}
