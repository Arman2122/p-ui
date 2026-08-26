package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

/*
A client with no transport must not quietly lose the guards it was given.

`NewProxiedHTTPClient` answers an unparseable panel egress proxy by logging and
returning `&http.Client{Timeout: timeout}` — a client with a nil `Transport`,
which means `http.DefaultTransport`. The subscription fetcher then asked
`client.Transport.(*http.Transport)`, got `ok == false`, and skipped the whole
block. Two things went with it:

  - `allowInsecure`, which the operator ticked on this subscription, so a
    self-signed endpoint failed with a certificate error and nothing said why;
  - `netsafe.SSRFGuardedDialContext`, which the no-proxy branch installs and
    which is what closes the DNS-rebinding gap that `SanitizePublicHTTPURL`
    cannot — it validates a hostname, the dialer validates the resolved IP.

So an invalid value in one setting silently disabled a security control on a
different one. F363.
*/

func TestAFetchClientWithNoTransportStillCarriesItsGuards(t *testing.T) {
	// Exactly what the invalid-proxy fallback produces.
	resolved := subscriptionFetchTransport(nil, true)

	transport, ok := resolved.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("resolved to %T, want an *http.Transport", resolved)
	}
	if transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("allowInsecure was dropped: the operator ticked it on this subscription and the fetch verifies anyway")
	}
	if transport.DialContext == nil {
		t.Fatal("no dialer installed, so the fetch uses the default one and the DNS-rebinding guard is gone")
	}

	// Not just any dialer: the one that refuses a private address.
	if _, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:9"); err == nil {
		t.Fatal("the dialer connected to a loopback address; it is not the guarded one")
	} else if !strings.Contains(err.Error(), "blocked private/internal address") {
		t.Fatalf("the dialer refused for the wrong reason (%v); it is not the guarded one", err)
	}
}

// And the guarded dialer arrives even when the caller did not ask for insecure,
// which is the security half on its own.
func TestAFetchClientWithNoTransportIsGuardedWithoutAllowInsecure(t *testing.T) {
	transport, ok := subscriptionFetchTransport(nil, false).(*http.Transport)
	if !ok || transport == nil {
		t.Fatal("resolved to something that is not an *http.Transport")
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("verification was disabled without being asked for")
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:9"); err == nil ||
		!strings.Contains(err.Error(), "blocked private/internal address") {
		t.Fatalf("the dialer is not the guarded one: %v", err)
	}
}

/*
And a transport that was supplied is used as it is.

A valid proxy builds a real transport whose Proxy or DialContext reaches the
proxy; substituting the guarded dialer there would route around the proxy the
operator configured, so the existing one has to survive untouched when nothing
needs changing.
*/
func TestASuppliedTransportIsLeftAlone(t *testing.T) {
	supplied := &http.Transport{DisableCompression: true}

	if got := subscriptionFetchTransport(supplied, false); got != http.RoundTripper(supplied) {
		t.Fatal("a supplied transport was replaced even though nothing needed changing")
	}

	cloned, ok := subscriptionFetchTransport(supplied, true).(*http.Transport)
	if !ok {
		t.Fatal("allowInsecure resolved to something that is not an *http.Transport")
	}
	if cloned == supplied {
		t.Fatal("allowInsecure mutated the caller's transport instead of cloning it")
	}
	if !cloned.DisableCompression {
		t.Fatal("the clone lost the supplied transport's settings")
	}
	// Clone() initialises the receiver's TLS config as a side effect, so the
	// property to hold is the one that matters, not that the field stayed nil.
	if supplied.TLSClientConfig != nil && supplied.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("the caller's transport was left skipping verification; every other user of it now skips it too")
	}
}
