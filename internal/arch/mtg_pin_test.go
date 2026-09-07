package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

/*
The mtg-proxy the panel ships is one its own preflight accepts. release.yml
downloads a pinned release and internal/mtproto refuses anything older than
MinimumVersion at boot, so a pin behind the minimum (or "latest", which can be)
bundles a sidecar the panel rejects and takes every MTProto inbound offline on
the next update. The pin may run ahead of the minimum; it may not fall behind.
*/
func TestMtgPinIsNotOlderThanThePanelMinimum(t *testing.T) {
	root := repoRoot(t)
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}
	shipped := regexp.MustCompile(`MTG_VER="v(\d+)\.(\d+)\.(\d+)"`).FindStringSubmatch(read(filepath.Join(".github", "workflows", "release.yml")))
	if shipped == nil {
		t.Fatal(`release.yml no longer pins MTG_VER="vX.Y.Z": "latest" can be older than the sidecar the panel refuses at preflight`)
	}
	minimum := regexp.MustCompile(`const MinimumVersion = "(\d+)\.(\d+)\.(\d+)"`).FindStringSubmatch(read(filepath.Join("internal", "mtproto", "process.go")))
	if minimum == nil {
		t.Fatal("internal/mtproto/process.go no longer declares const MinimumVersion as X.Y.Z")
	}
	for i := 1; i <= 3; i++ {
		s, _ := strconv.Atoi(shipped[i])
		m, _ := strconv.Atoi(minimum[i])
		if s > m {
			return
		}
		if s < m {
			t.Fatalf("release.yml ships mtg-proxy v%s.%s.%s but the panel refuses anything older than %s.%s.%s at preflight; move the pin forward",
				shipped[1], shipped[2], shipped[3], minimum[1], minimum[2], minimum[3])
		}
	}
}
