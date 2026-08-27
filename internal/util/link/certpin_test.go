package link

import (
	"strings"
	"testing"
)

/*
A SHA-256 certificate pin reaches xray-core in one form only: comma-separated
hex, 32 bytes each, colons permitted. Most tooling prints a pin in base64, so
pasting one is the natural mistake -- and the panel used to forgive it for
Hysteria and nowhere else, so the same paste worked on one inbound and produced
a config the core refuses on another. Ledger L21.

This table moved here from internal/sub when the normalisation stopped being
Hysteria's. Its last two cases are the load-bearing ones: something that is
neither hex nor a 32-byte base64 passes through unchanged, so the core reports
the operator's typo rather than the panel swallowing it.
*/
func TestNormalizePinHex(t *testing.T) {
	const hexPin = "c847dd2395d0978c0780b8201c4b289a8b281597d47c275f2d77d3f96d8de9c4"

	cases := []struct {
		name string
		in   string
		want string
	}{
		// Std base64 (xray-core's native TLS format / the panel generate button)
		// must be re-encoded to the hex form Hysteria2 clients expect (#4818).
		{"std base64", "yEfdI5XQl4wHgLggHEsomosoFZfUfCdfLXfT+W2N6cQ=", hexPin},
		// A manually pasted hex fingerprint passes through (lowercased).
		{"hex passthrough", hexPin, hexPin},
		{"uppercase hex lowercased", strings.ToUpper(hexPin), hexPin},
		// openssl x509 -fingerprint -sha256 emits colon-separated hex.
		{"colon hex stripped", "C8:47:DD:23:95:D0:97:8C:07:80:B8:20:1C:4B:28:9A:8B:28:15:97:D4:7C:27:5F:2D:77:D3:F9:6D:8D:E9:C4", hexPin},
		{"surrounding whitespace trimmed", "  " + hexPin + "  ", hexPin},
		// URL-safe base64 with the same 32 bytes decodes identically.
		{"url-safe base64", "yEfdI5XQl4wHgLggHEsomosoFZfUfCdfLXfT-W2N6cQ=", hexPin},
		// Garbage that is neither valid hex nor a 32-byte base64 is left as-is
		// rather than silently dropped.
		{"unrecognized passthrough", "not-a-pin", "not-a-pin"},
		{"empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizePinHex(tc.in); got != tc.want {
				t.Fatalf("NormalizePinHex(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The list form is what both the pcs link param and the core's own field carry.
func TestNormalizePinList(t *testing.T) {
	const hexPin = "c847dd2395d0978c0780b8201c4b289a8b281597d47c275f2d77d3f96d8de9c4"
	const b64Pin = "yEfdI5XQl4wHgLggHEsomosoFZfUfCdfLXfT+W2N6cQ="

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"one base64", b64Pin, hexPin},
		{"two, mixed forms", b64Pin + "," + hexPin, hexPin + "," + hexPin},
		{"spacing preserved as trimmed values", b64Pin + " , " + hexPin, hexPin + "," + hexPin},
		{"an unrecognised entry does not take the others with it", b64Pin + ",not-a-pin", hexPin + ",not-a-pin"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizePinList(tc.in); got != tc.want {
				t.Fatalf("NormalizePinList(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// And the importer applies it, which is the half that decides whether the
// generated outbound is a config the core will load.
func TestAnImportedLinkCarriesItsPinAsHex(t *testing.T) {
	const hexPin = "c847dd2395d0978c0780b8201c4b289a8b281597d47c275f2d77d3f96d8de9c4"
	const b64Pin = "yEfdI5XQl4wHgLggHEsomosoFZfUfCdfLXfT%2BW2N6cQ%3D"

	out, err := ParseLink("vless://11111111-2222-4333-8444-555555555555@example.test:443" +
		"?type=tcp&security=tls&sni=example.test&pcs=" + b64Pin + "#pin")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	stream, _ := out.Outbound["streamSettings"].(map[string]any)
	tls, _ := stream["tlsSettings"].(map[string]any)
	if got, _ := tls["pinnedPeerCertSha256"].(string); got != hexPin {
		t.Fatalf("pinnedPeerCertSha256 = %q, want the hex form the core reads; a base64 pin makes it refuse the whole config", got)
	}
}
