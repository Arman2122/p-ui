package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
A listener configured for HTTPS that ends up serving cleartext must say so.

Both servers bind the socket BEFORE deciding on TLS, then either wrap it or
leave it. When tls.LoadX509KeyPair fails they fall through and serve plain HTTP
on the very port HTTPS was configured on. The panel is up, and reports itself
up, because it is — just not encrypted. A truncated certbot renewal is enough.

Three things go with it, none of them obvious from the outside: session cookies
stop being Secure, HSTS is not sent, and on the panel the node mTLS client
certificate verification is never configured.

Reported, never repaired — the same contract as LogAuthPosture. Refusing to boot
would recreate the lockout family F224/F394 exists to prevent, and a certbot race
would then take the panel down on every restart until someone edited the
database by hand.

Asserted on the source of the failure branch, which is the idiom this package
already uses for exactly this family (see TestEverySettingThatCanLockAnOperator
OutHasACliEscape). A behavioural test would need Start() split so the listener
construction is callable alone, which is a larger change than the fix.
*/
func TestACertThatFailsToLoadIsReportedNotHidden(t *testing.T) {
	cases := []struct {
		file string
		why  string
	}{
		{"internal/web/web.go", "the panel serving cleartext also drops HSTS and node mTLS"},
		{"internal/sub/sub.go", "the subscription server serving cleartext hands out every client's credentials in the clear"},
	}

	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(c.file)))
			if err != nil {
				t.Fatalf("read %s: %v", c.file, err)
			}
			source := string(body)

			// LastIndex, not Index: web.go also probes the pair in
			// isDirectHTTPSConfigured, and the branch that matters is the one
			// that decides whether the LISTENER is wrapped.
			load := strings.LastIndex(source, "tls.LoadX509KeyPair")
			if load < 0 {
				t.Fatalf("%s no longer loads a key pair; this guard is now vacuous", c.file)
			}
			// The failure branch is the `} else {` that closes the `err == nil` arm.
			branchStart := strings.Index(source[load:], "\n\t\t} else {")
			if branchStart < 0 {
				t.Fatalf("%s restructured its cert-load branch; this guard is now vacuous", c.file)
			}
			rest := source[load+branchStart:]
			branchEnd := strings.Index(rest[len("\n\t\t} else {"):], "\n\t\t}")
			if branchEnd < 0 {
				t.Fatalf("cannot find the end of the cert-load failure branch in %s", c.file)
			}
			branch := rest[:len("\n\t\t} else {")+branchEnd]

			if !strings.Contains(branch, "TLSPostureLogPrefix") {
				t.Errorf("%s falls back to cleartext without the greppable posture line, so the operator has no way to know (%s):\n%s",
					c.file, c.why, branch)
			}
			// The other half of the contract: it must still boot. A cert that
			// cannot load is exactly when an operator needs the panel to reach
			// the settings that fix it.
			for _, fatal := range []string{"return err", "log.Fatal", "os.Exit", "panic("} {
				if strings.Contains(branch, fatal) {
					t.Errorf("%s now refuses to start when the cert fails to load (%q); that is the lockout F224/F394 exists to prevent",
						c.file, fatal)
				}
			}
		})
	}
}
