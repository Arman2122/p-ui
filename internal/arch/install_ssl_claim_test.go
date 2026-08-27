package arch

import (
	"strings"
	"testing"
)

/*
The installer must not announce https on a panel that has no certificate.

prompt_and_setup_ssl set SSL_SCHEME="https" on entry and only case 4 -- the
operator declining SSL outright -- ever set it back. Both certificate FAILURE
branches print a red line, set SSL_HOST to the server IP and fall through with
the scheme still https. The installer then prints an https:// access URL, prints
"SSL Certificate: Enabled and configured", and persists scheme=https into
/etc/p-ui/install-result.env for cloud-init and the MOTD to repeat -- while the
panel serves plain HTTP and that URL refuses to connect.

update.sh's equivalent block was repaired during the campaign and now only
claims what happened, which is what makes this a leftover rather than a choice.

So the scheme starts at http and each of the three paths that really installs a
certificate raises it. Proved by driving prompt_and_setup_ssl with a stubbed
ssl_cert_issue: failing, it now ends at http and used to end at https; and
succeeding, it still ends at https. F225.
*/
func TestTheInstallerOnlyClaimsHttpsWhenACertificateWasInstalled(t *testing.T) {
	install := readRepoScript(t, "install.sh")
	body := shellFunctionBody(t, install, "prompt_and_setup_ssl")

	first := strings.Index(body, "SSL_SCHEME=")
	if first < 0 {
		t.Fatal("prompt_and_setup_ssl no longer sets SSL_SCHEME; this guard is now vacuous")
	}
	if !strings.HasPrefix(body[first:], `SSL_SCHEME="http"`) {
		t.Errorf("prompt_and_setup_ssl opens by assuming https: %q. Both certificate failure branches fall through without touching it, so the installer announces an https URL for a panel serving plain HTTP",
			strings.TrimSpace(body[first:first+40]))
	}
	if strings.Count(body, `SSL_SCHEME="https"`) < 3 {
		t.Error("fewer than three paths raise the scheme to https; one of the three that installs a certificate now under-reports it")
	}

	// And the file cloud-init and the MOTD read must not default the other way.
	for _, bare := range []string{`: "${SSL_SCHEME:=https}"`} {
		if strings.Contains(install, bare) && !strings.Contains(install, `if [[ -n "${existing_cert}" ]]; then`) {
			t.Errorf("install.sh still defaults the persisted scheme to https with %s and nothing derives it from a real certificate", bare)
		}
	}
}
