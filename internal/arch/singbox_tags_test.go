package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The tags release.yml builds sing-box with are the ones the updater requires
// of a release it installs; a copy drifting would refuse every update.
func TestSingboxBuildTagsMatchTheReleaseWorkflow(t *testing.T) {
	root := repoRoot(t)
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}
	shipped := regexp.MustCompile(`-tags ([a-z0-9_,]+)`).FindStringSubmatch(read(filepath.Join(".github", "workflows", "release.yml")))
	if shipped == nil {
		t.Fatal("release.yml no longer builds sing-box with -tags")
	}
	required := regexp.MustCompile(`RequiredBuildTags = "([^"]+)"`).FindStringSubmatch(read(filepath.Join("internal", "cores", "internal", "singbox", "updates.go")))
	if required == nil {
		t.Fatal("internal/cores/internal/singbox/updates.go no longer declares RequiredBuildTags")
	}
	built, wanted := strings.Split(shipped[1], ","), strings.Split(required[1], ",")
	slices.Sort(built)
	slices.Sort(wanted)
	if !slices.Equal(built, wanted) {
		t.Fatalf("release.yml builds sing-box with %v but the updater requires %v; change both together", built, wanted)
	}
}
