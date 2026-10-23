package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func providerFetchSpec(address string) FleetProviderFetchSpec {
	return FleetProviderFetchSpec{Version: 1, URL: address, Format: "auto", IntervalSeconds: 60}
}

func requireProviderFailure(t *testing.T, err error, code string) {
	t.Helper()
	var failure fleetProviderFailure
	if !errors.Is(err, ErrFleetProviderFetch) || !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("refresh error = %v, want %s", err, code)
	}
}

func TestFleetProviderFetchPrivatePolicyAndRedaction(t *testing.T) {
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.URL.Path == "/fail" {
			http.Error(w, "response-secret", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, "fixture")
	}))
	defer server.Close()
	spec := providerFetchSpec(server.URL + "/feed?token=query-secret")
	_, err := fetchFleetProvider(t.Context(), spec, fleetProviderNetwork{})
	requireProviderFailure(t, err, "PRIVATE_TARGET")
	if received.Load() != 0 || strings.Contains(err.Error(), "secret") {
		t.Fatal("private fetch reached server or exposed credentials")
	}
	spec.AllowPrivate = true
	body, err := fetchFleetProvider(t.Context(), spec, fleetProviderNetwork{})
	if err != nil || string(body) != "fixture" || received.Load() != 1 {
		t.Fatalf("authorized private fetch: received=%d err=%v", received.Load(), err)
	}
	spec.URL = server.URL + "/fail?token=query-secret"
	_, err = fetchFleetProvider(t.Context(), spec, fleetProviderNetwork{})
	requireProviderFailure(t, err, "HTTP_STATUS_403")
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("failure exposed source URL or response body")
	}
}

func TestFleetProviderFetchChecksEveryResolvedAddressAndRedirect(t *testing.T) {
	var hosts, hardware []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)
		hardware = append(hardware, r.Header.Get("X-HWID"))
		if r.Host == "source.example" {
			http.Redirect(w, r, "http://redirect.example/feed", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "fixture")
	}))
	defer server.Close()
	var resolved, dialed []string
	network := fleetProviderNetwork{
		resolve: func(_ context.Context, host string) ([]net.IPAddr, error) {
			resolved = append(resolved, host)
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}}, nil
		},
		dial: func(ctx context.Context, kind, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return (&net.Dialer{}).DialContext(ctx, kind, server.Listener.Addr().String())
		},
	}
	spec := providerFetchSpec("http://source.example/feed")
	spec.HWID = "explicit-fixture"
	body, err := fetchFleetProvider(t.Context(), spec, network)
	if err != nil || string(body) != "fixture" || !slices.Equal(resolved, []string{"source.example", "redirect.example"}) ||
		!slices.Equal(dialed, []string{"203.0.113.7:80", "203.0.113.7:80"}) || !slices.Equal(hardware, []string{"explicit-fixture", ""}) {
		t.Fatalf("guarded redirect: resolved=%v dialed=%v hardware=%v err=%v", resolved, dialed, hardware, err)
	}
	if !slices.Equal(hosts, []string{"source.example", "redirect.example"}) {
		t.Fatal("checked IP changed HTTP host identity")
	}
	dialed = nil
	network.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}
	_, err = fetchFleetProvider(t.Context(), spec, network)
	requireProviderFailure(t, err, "PRIVATE_TARGET")
	if len(dialed) != 0 {
		t.Fatal("mixed private/public resolution dialed before validating all addresses")
	}
}

func TestFleetProviderFetchBoundsAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, strings.Repeat("x", maxFleetProviderBytes+1))
	}))
	defer server.Close()
	spec := providerFetchSpec(server.URL)
	spec.AllowPrivate = true
	_, err := fetchFleetProvider(t.Context(), spec, fleetProviderNetwork{})
	requireProviderFailure(t, err, "BODY_TOO_LARGE")
	spec.URL += "/wait"
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = fetchFleetProvider(ctx, spec, fleetProviderNetwork{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled fetch: %v", err)
	}
}

func TestFleetProviderFetchRedirectPortDoesNotShareIdentity(t *testing.T) {
	var identity string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity = r.Header.Get("X-HWID")
		_, _ = io.WriteString(w, "fixture")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	spec := providerFetchSpec(source.URL)
	spec.AllowPrivate, spec.HWID = true, "scoped-fixture-identity"
	if _, err := fetchFleetProvider(t.Context(), spec, fleetProviderNetwork{}); err != nil {
		t.Fatal(err)
	}
	if identity != "" {
		t.Fatal("cross-port redirect exposed provider-scoped identity")
	}
}

func TestFleetProviderFetchTLSRequiresExplicitPermission(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "fixture") }))
	defer server.Close()
	spec := providerFetchSpec(server.URL)
	spec.AllowPrivate = true
	_, err := fetchFleetProvider(t.Context(), spec, fleetProviderNetwork{})
	requireProviderFailure(t, err, "FETCH_FAILED")
	spec.AllowInsecure = true
	if body, err := fetchFleetProvider(t.Context(), spec, fleetProviderNetwork{}); err != nil || string(body) != "fixture" {
		t.Fatalf("explicit TLS fixture permission: %v", err)
	}
}

func providerTags(t *testing.T, content fleetProviderContent) []string {
	t.Helper()
	var entries []struct{ Tag string }
	if err := json.Unmarshal(content.Outbounds, &entries); err != nil {
		t.Fatal(err)
	}
	result := make([]string, len(entries))
	for i, entry := range entries {
		result[i] = entry.Tag
	}
	return result
}

func providerIdentities(t *testing.T, content fleetProviderContent) map[string]string {
	t.Helper()
	var result map[string]string
	if err := json.Unmarshal(content.Identities, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFleetProviderParserStrictAndStableIdentity(t *testing.T) {
	providerID := uuid.NewString()
	spec := providerFetchSpec("https://provider.example/feed")
	a := "trojan://fixture-a@edge.example:443?security=tls&sni=edge.example#Alpha"
	b := "trojan://fixture-b@other.example:443?security=tls&sni=other.example#Beta"
	first, err := parseFleetProvider(providerID, spec, []byte(a+"\n"+b), nil)
	if err != nil || first.Count != 2 {
		t.Fatalf("initial feed: count=%d err=%v", first.Count, err)
	}
	oldTags := providerTags(t, first)
	ledger := providerIdentities(t, first)
	reordered, err := parseFleetProvider(providerID, spec, []byte(strings.ReplaceAll(b+"\n"+a, "#Alpha", "#Renamed")), ledger)
	if err != nil || !slices.Equal(providerTags(t, reordered), []string{oldTags[1], oldTags[0]}) {
		t.Fatalf("reorder/rename changed stable identity: %v", err)
	}
	rotated, err := parseFleetProvider(providerID, spec, []byte(strings.Replace(a, "fixture-a", "fixture-rotated", 1)+"\n"+b), ledger)
	if err != nil {
		t.Fatal(err)
	}
	newTags := providerTags(t, rotated)
	if newTags[0] == oldTags[0] || newTags[1] != oldTags[1] || len(providerIdentities(t, rotated)) != 3 {
		t.Fatal("credential rotation reused old identity or disturbed unrelated peer")
	}
	for _, tc := range []struct{ name, body, code string }{
		{"bad-middle", a + "\nmalformed\n" + b, "INVALID_FEED"},
		{"duplicate-account", a + "\n" + strings.Replace(a, "#Alpha", "#Other", 1), "AMBIGUOUS_IDENTITY"},
		{"unqualified", "hysteria2://fixture@edge.example:443/?sni=edge.example", "UNQUALIFIED_PROTOCOL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := parseFleetProvider(providerID, spec, []byte(tc.body), ledger)
			requireProviderFailure(t, err, tc.code)
			if content.Count != 0 || len(content.Outbounds) != 0 || len(ledger) != 2 {
				t.Fatal("failed feed returned partial graph or changed previous identities")
			}
		})
	}
	for _, format := range []string{"auto", "base64"} {
		spec.Format = format
		encoded := base64.StdEncoding.EncodeToString([]byte(a + "\n" + b))
		content, err := parseFleetProvider(providerID, spec, []byte(encoded), ledger)
		if err != nil || !slices.Equal(providerTags(t, content), oldTags) {
			t.Fatalf("base64 %s: %v", format, err)
		}
	}
}

func TestFleetProviderParserExplicitEmptyRevision(t *testing.T) {
	spec := providerFetchSpec("https://provider.example/feed")
	for _, body := range []string{"", " \r\n", "# empty subscription\n# operator removed all entries"} {
		content, err := parseFleetProvider(uuid.NewString(), spec, []byte(body), nil)
		if err != nil || content.Count != 0 || string(content.Outbounds) != "[]" {
			t.Fatalf("empty feed should persist explicit empty cache: count=%d err=%v", content.Count, err)
		}
	}
}
