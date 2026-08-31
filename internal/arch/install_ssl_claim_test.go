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

/*
And update.sh must not announce https either.

Its config_after_update guarded the https line on `[[ -n "${SSL_HOST}" ]]`,
which looks like the same repair and is dead: EVERY branch of that script's
prompt_and_setup_ssl sets SSL_HOST — the skip-SSL branch, the invalid-choice
branch and both certificate failure paths all set it to the server IP. So the
else branch was unreachable, and an operator who declined SSL, or whose
certificate failed, was told "SSL Certificate: Enabled and configured" and
handed an https:// URL that refuses to connect. The URL was a hardcoded
https:// besides, so even the branch that meant to say http could not have.

Same shape as install.sh above: the scheme starts at http, only the three paths
that really install a certificate raise it, and the report follows it. F165.
*/
func TestUpdateOnlyClaimsHttpsWhenACertificateWasInstalled(t *testing.T) {
	update := readRepoScript(t, "update.sh")
	body := shellFunctionBody(t, update, "prompt_and_setup_ssl")

	first := strings.Index(body, "SSL_SCHEME=")
	if first < 0 {
		t.Fatal("update.sh's prompt_and_setup_ssl never sets SSL_SCHEME, so nothing downstream can tell a certificate from a refusal")
	}
	if !strings.HasPrefix(body[first:], `SSL_SCHEME="http"`) {
		t.Errorf("prompt_and_setup_ssl opens at %q rather than http; the failure branches fall through without touching it",
			strings.TrimSpace(body[first:first+40]))
	}
	if got := strings.Count(body, `SSL_SCHEME="https"`); got < 3 {
		t.Errorf("%d paths raise the scheme to https, want the 3 that install a certificate", got)
	}

	report := shellFunctionBody(t, update, "config_after_update")
	if strings.Contains(report, `if [[ -n "${SSL_HOST}" ]]; then`) {
		t.Error("config_after_update still decides on SSL_HOST, which every branch of prompt_and_setup_ssl sets — including the one that declines SSL outright, so the http message is unreachable")
	}
	if !strings.Contains(report, `if [[ "${SSL_SCHEME}" == "https" ]]; then`) {
		t.Error("config_after_update does not branch on the scheme that was actually reached")
	}
	if strings.Contains(report, "Access URL: https://${SSL_HOST}") {
		t.Error("the access URL hardcodes https, so the branch that means to report http cannot")
	}
}

/*
And a custom certificate the panel REFUSES must not raise the scheme either.

Cases 1 and 2 branch on their issuer's exit status; case 3 discarded `p-ui
cert`'s. That binary refuses a key that does not match its certificate, an
expired file, an unreadable PEM -- and the installer printed a green tick, set
https and restarted, so the panel served plain HTTP on an https URL the operator
had just been told was ready, with the reason thrown away to /dev/null.

The operator's answer to the open question was re-prompt, so the failure loops
back for new paths with an explicit way out to no-SSL. Non-interactive gives up
instead: `read` returns EOF there, and a retry loop around one would spin.
*/
func TestARefusedCustomCertificateDoesNotClaimHttps(t *testing.T) {
	body := shellFunctionBody(t, readRepoScript(t, "install.sh"), "prompt_and_setup_ssl")

	custom := body[strings.Index(body, "3)"):]
	if idx := strings.Index(custom, "\n        4)"); idx > 0 {
		custom = custom[:idx]
	}
	if !strings.Contains(custom, "p-ui cert") {
		t.Fatal("the custom-certificate case no longer calls p-ui cert; this guard is now vacuous")
	}

	// The status has to be read. Discarding it into /dev/null is the defect.
	if strings.Contains(custom, `-webCertKey "$custom_key" > /dev/null 2>&1`) {
		t.Error("the custom-certificate case still throws away p-ui cert's exit status and its reason, so a refused certificate is reported as success")
	}
	if !strings.Contains(custom, "cert_applied") {
		t.Error("nothing in the custom-certificate case records whether the certificate was actually applied")
	}

	// https must be reachable only through that flag.
	scheme := strings.Index(custom, `SSL_SCHEME="https"`)
	if scheme < 0 {
		t.Fatal("the custom-certificate case never raises the scheme, so a working certificate is under-reported")
	}
	guard := strings.LastIndex(custom[:scheme], `if [[ "$cert_applied" == "1" ]]; then`)
	if guard < 0 {
		t.Error("the custom-certificate case raises the scheme to https without first checking that the certificate applied")
	}

	// A retry loop with no reader spins forever; the non-interactive path must
	// leave it rather than read EOF in a `while true`.
	if !strings.Contains(custom, `if [[ "$NONINTERACTIVE" == "1" ]]; then`) {
		t.Error("the retry loop has no non-interactive escape, so an automated install spins on a read that returns EOF")
	}
}

/*
update.sh carries the same case and had the same defect.

The two scripts drift whenever one is repaired alone -- which is why the guard
above has an update.sh twin, and why this does too. It gates on the tty rather
than on NONINTERACTIVE: that variable does not exist in update.sh, and a guard
written against it would be permanently false, leaving the retry loop to spin on
a read that returns EOF.
*/
func TestUpdateAlsoRefusesToClaimHttpsForARefusedCertificate(t *testing.T) {
	body := shellFunctionBody(t, readRepoScript(t, "update.sh"), "prompt_and_setup_ssl")

	custom := body[strings.Index(body, "3)"):]
	if idx := strings.Index(custom, "\n        4)"); idx > 0 {
		custom = custom[:idx]
	}
	if !strings.Contains(custom, "p-ui cert") {
		t.Fatal("update.sh's custom-certificate case no longer calls p-ui cert; this guard is now vacuous")
	}
	if strings.Contains(custom, `-webCertKey "$custom_key" > /dev/null 2>&1`) {
		t.Error("update.sh still throws away p-ui cert's exit status, so a refused certificate is reported as success")
	}
	if !strings.Contains(custom, "cert_applied") {
		t.Error("nothing in update.sh's custom-certificate case records whether the certificate applied")
	}
	if !strings.Contains(custom, `if [[ ! -t 0 ]]; then`) {
		t.Error("update.sh's retry loop has no escape for a run with no tty, so it spins on a read that returns EOF")
	}
}
