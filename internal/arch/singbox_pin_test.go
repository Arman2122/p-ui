package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

/*
The sing-box the panel drives is the sing-box it ships. release.yml compiles
upstream's tagged source and the core package assumes that release's config
schema and API; nothing else keeps the two on the same tag, so this guard
compares the version each spells out.
*/
func TestSingboxPinMatchesTheShippedBinary(t *testing.T) {
	root := repoRoot(t)
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}
	shipped := regexp.MustCompile(`SINGBOX_VER="v([^"]+)"`).FindStringSubmatch(read(filepath.Join(".github", "workflows", "release.yml")))
	if shipped == nil {
		t.Fatal(`release.yml no longer declares SINGBOX_VER="vX.Y.Z"`)
	}
	compiled := regexp.MustCompile(`const Version = "([^"]+)"`).FindStringSubmatch(read(filepath.Join("internal", "cores", "internal", "singbox", "version.go")))
	if compiled == nil {
		t.Fatal("internal/cores/internal/singbox/version.go no longer declares const Version")
	}
	if shipped[1] != compiled[1] {
		t.Fatalf("release.yml builds sing-box v%s but the core expects %s; bump both together", shipped[1], compiled[1])
	}
}
