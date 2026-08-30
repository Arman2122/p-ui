package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
Every HTTP client the panel dials OUT with either guards its dial or has a
stated reason not to.

Three clients fetch a URL an administrator typed. Each already validates that
URL against the private-address policy before the request -- and then dials with
a bare dialer, so the name is resolved a second time, unchecked. A hostname that
answered publicly at check time and privately at dial time reaches the panel's
own metadata service, its loopback admin port, or anything else on the LAN.

The subscription client had the other half of the same hole: the URL was checked
once and a REDIRECT is a second URL nobody checked at all.

Guarded through fasthttp's DialTimeout, never Dial. fasthttp consults
DialTimeout first and the Dial branch discards the per-request timeout entirely,
which would stretch the traffic job's three-second inform budget to the ten-second
default and make the poll skip.

The Telegram client's PROXY branch is deliberately unguarded: resolving a proxied
request locally would route around the proxy the operator configured. F355.
*/
func TestOutboundHttpClientsGuardTheirDial(t *testing.T) {
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}

	t.Run("the traffic job's inform client", func(t *testing.T) {
		src := read("internal/web/job/xray_traffic_job.go")
		client := between(t, src, "var externalInformClient = &fasthttp.Client{", "\n}")
		if !strings.Contains(client, "netsafe.SSRFGuardedDialContext") {
			t.Error("the inform client dials unguarded, so a hostname that passed the public-URL check can resolve to a private address at dial time")
		}
		if !strings.Contains(client, "DialTimeout:") {
			t.Error("the guard is installed on Dial rather than DialTimeout; fasthttp prefers DialTimeout and the Dial branch drops the request timeout, stretching the 3s inform budget to 10s")
		}
	})

	t.Run("the telegram client", func(t *testing.T) {
		src := read("internal/web/service/tgbot/tgbot.go")
		if !strings.Contains(src, "netsafe.SSRFGuardedDialContext") {
			t.Error("the Telegram client dials unguarded on its direct branch")
		}
		// And the proxy branch must NOT be guarded.
		proxy := between(t, src, `if strings.HasPrefix(proxyUrl, "socks5://")`, "\t} else {")
		if strings.Contains(proxy, "netsafe.") {
			t.Error("the guard was installed on the proxy branch, which resolves locally and routes around the proxy the operator configured")
		}
	})

	t.Run("the subscription client", func(t *testing.T) {
		src := read("internal/sub/external_subscription.go")
		client := between(t, src, "var subscriptionHTTPClient = &http.Client{", "\n}")
		if !strings.Contains(client, "CheckRedirect") {
			t.Error("the subscription client follows redirects without re-checking the destination; the URL was validated once and a redirect is a second URL nobody checked")
		}
		if !strings.Contains(client, "SanitizePublicHTTPURL") {
			t.Error("the redirect guard does not reuse the panel's own URL policy, so it will drift from it")
		}
	})
}

func between(t *testing.T, src, open, close string) string {
	t.Helper()
	i := strings.Index(src, open)
	if i < 0 {
		t.Fatalf("%q not found; this guard is now vacuous", open)
	}
	rest := src[i:]
	if j := strings.Index(rest, close); j >= 0 {
		return rest[:j]
	}
	return rest
}
