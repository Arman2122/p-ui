package mtproto

import (
	"strings"
	"testing"
)

// An old sidecar takes every limit the panel writes and applies none of them,
// silently; it is refused at preflight by name. A source build says "dev" and
// is newer than any release by construction, so it is let through.
func TestCheckVersionRefusesAnOldRelease(t *testing.T) {
	for _, tc := range []struct {
		version string
		ok      bool
	}{
		{"1.3.0", false},
		{"v1.8.0", false},
		{"1.9.0", true},
		{"1.10.0", true},
		{"2.0.0", true},
		{"dev", true},
		{"9.9.9", true},
		{"", true},
	} {
		err := checkVersion(tc.version)
		if (err == nil) != tc.ok {
			t.Fatalf("checkVersion(%q) = %v, want ok=%v", tc.version, err, tc.ok)
		}
		if err != nil && (!strings.Contains(err.Error(), tc.version) || !strings.Contains(err.Error(), MinimumVersion)) {
			t.Fatalf("the refusal must name both versions, got %v", err)
		}
	}
}
