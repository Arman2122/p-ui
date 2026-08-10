package cores

import (
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

// TestClientCredentials pins the declarations service code now decides from:
// wireguard losing allowedIPs would stop the panel minting client keys.
func TestClientCredentials(t *testing.T) {
	cases := []struct {
		kind core.Kind
		want []string
	}{
		{
			kind: "wireguard",
			want: []string{core.CredPrivateKey, core.CredPublicKey, core.CredPreSharedKey, core.CredAllowedIPs},
		},
		{kind: "mtproto", want: []string{core.CredSecret, core.CredAdTag}},
		{kind: "vmess", want: []string{core.CredUUID, core.CredSecurity}},
		{kind: "http", want: nil},
		{kind: "ocserv", want: nil},
	}

	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			got := ClientCredentials(tc.kind)
			if !slices.Equal(got, tc.want) {
				t.Errorf("ClientCredentials(%q) = %v, want %v", tc.kind, got, tc.want)
			}
		})
	}
}
