package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
Core daemons run as p-ui-core, and QUIC inbounds need the socket buffers the
drop-in raises. Both scripts must provide them: a host updated from an older
release has neither, and a daemon core whose unit names a missing user fails to
start with nothing in the panel saying why.
*/
func TestBothInstallersProvideTheCoreUserAndTheQuicBuffers(t *testing.T) {
	root := repoRoot(t)
	for _, script := range []string{"install.sh", "update.sh"} {
		t.Run(script, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, script))
			if err != nil {
				t.Fatalf("read %s: %v", script, err)
			}
			text := string(body)
			for _, want := range []string{
				"useradd --system --no-create-home --home-dir /var/lib/p-ui --shell /usr/sbin/nologin --user-group p-ui-core",
				"/etc/sysctl.d/99-p-ui-quic.conf",
				"net.core.rmem_max=7500000",
				"\n    ensure_core_user\n",
				"\n    install_quic_sysctl\n",
				"apt-get install -y -q openvpn openvpn-auth-radius",
				"\n    install_openvpn_packages\n",
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("%s lacks %q", script, want)
				}
			}
		})
	}
}
