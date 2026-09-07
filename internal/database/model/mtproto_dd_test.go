package model

import "testing"

// A WEB client refuses an ee secret before it opens a socket; it wants the same
// key as dd with no domain, and nothing else is a WEB secret.
func TestMtprotoDDSecret(t *testing.T) {
	const ee = "ee8196fe6ed8b637d001f91d6952cfcdf07777772e636c6f7564666c6172652e636f6d"
	for _, tc := range []struct{ in, want string }{
		{ee, "dd8196fe6ed8b637d001f91d6952cfcdf0"},
		{"7oGW_m7YtjfQAfkdaVLPzfB3d3cuY2xvdWRmbGFyZS5jb20", "dd8196fe6ed8b637d001f91d6952cfcdf0"},
		{"dd8196fe6ed8b637d001f91d6952cfcdf0", ""},
		{"ee8196", ""},
		{"", ""},
		{"not hex", ""},
	} {
		if got := MtprotoDDSecret(tc.in); got != tc.want {
			t.Fatalf("MtprotoDDSecret(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
