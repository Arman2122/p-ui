package mtproto

import "testing"

/*
matchesBinary decides whether a process gets SIGKILL, so the case that matters
most is the one it must refuse: a same-named mtg living somewhere else. Matching
on the base name killed a live sidecar from another install's bin folder.

The " (deleted)" cases are the reason the base name was used originally — Linux
marks /proc/<pid>/exe that way once an update replaces the binary, and the
orphan sweep still has to recognise its own process afterwards.
*/
func TestMatchesBinary(t *testing.T) {
	const want = "/usr/local/p-ui/bin/mtg-linux-amd64"
	tests := []struct {
		name      string
		exePath   string
		argv0Path string
		match     bool
	}{
		{
			name:    "our own binary",
			exePath: want,
			match:   true,
		},
		{
			name:    "another install's mtg of the same name",
			exePath: "/root/smoke/bin/mtg-linux-amd64",
			match:   false,
		},
		{
			name:    "our binary after an update replaced it",
			exePath: want + " (deleted)",
			match:   true,
		},
		{
			name:      "exe unreadable, argv[0] answers",
			argv0Path: want,
			match:     true,
		},
		{
			name:      "exe unreadable and argv[0] is another install",
			argv0Path: "/opt/other/bin/mtg-linux-amd64",
			match:     false,
		},
		{
			name:  "nothing readable",
			match: false,
		},
		{
			name:    "a different binary entirely",
			exePath: "/usr/local/p-ui/bin/xray-linux-amd64",
			match:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesBinary(tt.exePath, tt.argv0Path, want); got != tt.match {
				t.Fatalf("matchesBinary(%q, %q) = %v, want %v", tt.exePath, tt.argv0Path, got, tt.match)
			}
		})
	}
}
