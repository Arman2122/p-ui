package arch

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

/*
The Xray-core the panel is compiled against is the Xray-core it ships.

go.mod pins the library at a pseudo-version of an upstream commit and
release.yml downloads a tagged binary; the config types come from one and the
process runs the other, and nothing kept the two on the same release. This
guard compares the dates the two encode: v1.YYMMDD.N-0.YYYYMMDDhhmmss-hash
against vYY.M.D. F322.
*/
func TestXrayPinMatchesTheShippedBinary(t *testing.T) {
	root := repoRoot(t)
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}

	pin := regexp.MustCompile(`github\.com/xtls/xray-core v1\.(\d{6})\.\d+(?:-0\.(\d{8})\d{6}-[0-9a-f]+)?`).FindStringSubmatch(read("go.mod"))
	if pin == nil {
		t.Fatal("go.mod no longer pins github.com/xtls/xray-core in the v1.YYMMDD form this guard reads")
	}
	pinned := "20" + pin[1]
	if pin[2] != "" {
		pinned = pin[2]
	}

	tag := regexp.MustCompile(`Xray-core/releases/download/v(\d+)\.(\d+)\.(\d+)/`).FindStringSubmatch(read(filepath.Join(".github", "workflows", "release.yml")))
	if tag == nil {
		t.Fatal("release.yml no longer downloads a tagged Xray-core release")
	}
	y, _ := strconv.Atoi(tag[1])
	m, _ := strconv.Atoi(tag[2])
	d, _ := strconv.Atoi(tag[3])
	shipped := fmt.Sprintf("20%02d%02d%02d", y, m, d)

	if pinned != shipped {
		t.Fatalf("go.mod compiles against the Xray-core of %s but release.yml ships v%s.%s.%s (%s): bump both together, the config types and the binary must come from one upstream release", pinned, tag[1], tag[2], tag[3], shipped)
	}
}
