package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
Every AmneziaWG fix in the tree is invisible on a box that never built the module.

install.sh registers it through DKMS; update.sh did not mention it at all, so an
operator who installed before AmneziaWG shipped and upgraded the ordinary way
never got the module, however many times they updated. The protocol is simply
absent from their panel, with no explanation — and the panel cannot compensate,
because amneziawg.Core.Preflight only probes for the module, it never builds one.

Guarded rather than trusted to review: the two scripts drifted apart once
already, and nothing else in the build notices.
*/
func TestBothInstallScriptsBuildTheAmneziaWGModule(t *testing.T) {
	root := repoRoot(t)

	for _, script := range []string{"install.sh", "update.sh"} {
		t.Run(script, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, script))
			if err != nil {
				t.Fatalf("read %s: %v", script, err)
			}
			text := string(body)

			if !strings.Contains(text, "awg-dkms.sh install") {
				t.Fatalf("%s never runs awg-dkms.sh, so AmneziaWG is missing on every host it touches", script)
			}
			// Non-fatal by design: a host with no kernel headers must still get a
			// working panel with every other protocol.
			if strings.Contains(text, "_fail") && strings.Contains(text, "_fail \"AmneziaWG") {
				t.Fatalf("%s treats a failed AmneziaWG build as fatal; it is optional by design", script)
			}
		})
	}
}

/*
And the build has to notice a re-vendored source.

awg-dkms.sh short-circuits on `dkms status`, which keys on the running kernel and
MODULE_VERSION — and upstream pins that at 1.0.0 across every release. So a panel
update that ships new module sources would leave the old .ko registered and
loaded, silently rejecting whatever device attributes the new panel sends.
*/
func TestTheModuleIsRebuiltWhenTheVendoredSourceChanges(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "awg-dkms.sh"))
	if err != nil {
		t.Fatalf("read awg-dkms.sh: %v", err)
	}
	text := string(body)

	for _, want := range []string{
		"source_fingerprint", // the content hash exists
		"STAMP_FILE",         // and is persisted across runs
		"want_fingerprint",   // and is compared
		`sha256sum`,          // by content, not mtime
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("awg-dkms.sh no longer carries %q, so a re-vendored source is not rebuilt", want)
		}
	}
	// The stamp must be written only after a successful load, or a failed build
	// marks itself done and never retries.
	stamp := strings.Index(text, `> "$STAMP_FILE"`)
	loaded := strings.Index(text, `echo "$MODULE_NAME" > /etc/modules-load.d/amneziawg.conf`)
	if stamp < 0 || loaded < 0 || stamp < loaded {
		t.Fatal("the source fingerprint is stamped before the module is known to load; a failed build would never be retried")
	}
	// And dropped on removal, or a reinstall skips the build entirely.
	if !strings.Contains(text, `rm -f /etc/modules-load.d/amneziawg.conf "$STAMP_FILE"`) {
		t.Fatal("removing the module leaves the fingerprint behind, so reinstalling it does nothing")
	}
}
