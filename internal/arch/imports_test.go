package arch

import (
	"slices"
	"sort"
	"strings"
	"testing"
)

/*
Import fences for the multi-core refactor.

Two of these have zero violations today and are frozen here while that is still
true — once concrete cores start landing, every one of them becomes a negotiation
rather than a fact. The third pins the one coupling that currently blocks the
rest, so it cannot spread before P0 removes it.

Enforced by parsing imports rather than by a depguard rule because a lint rule
can be suppressed with //nolint at the call site, and because these travel with
`make test-go` on every developer machine.
*/

const (
	xrayCoreVendor = "github.com/xtls/xray-core"
	modulePrefix   = "github.com/Arman2122/p-ui"
)

// coreEnginePrefixes are the packages that wrap a protocol engine. internal/cores
// is where the refactor puts new ones; listing it now costs nothing and means the
// fences already cover the first core that lands there.
var coreEnginePrefixes = []string{
	"internal/xray/",
	"internal/mtproto/",
	"internal/wireguard/",
	"internal/cores/",
}

func isCoreEngine(rel string) bool {
	for _, prefix := range coreEnginePrefixes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// TestXrayCoreVendorIsFenced keeps the vendored core behind its wrapper. A second
// core cannot be introduced cleanly while any part of the panel can reach into
// Xray's own types, and today nothing does.
func TestXrayCoreVendorIsFenced(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	checked := 0
	for _, src := range parseNonTestGo(t, root) {
		if isCoreEngine(src.Rel) {
			continue
		}
		checked++
		for _, path := range importPaths(src.File) {
			if strings.HasPrefix(path, xrayCoreVendor) {
				violations = append(violations, src.Rel+" imports "+path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("checked no files outside the core engines; the fence is vacuous")
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("%s — only the core wrapper packages may import the Xray vendor; go through the core's own interface instead", v)
	}
}

// TestCoreEnginesDoNotImportTheWebLayer keeps cores as leaves. A core that knows
// about services or controllers cannot be moved under internal/cores/ later, and
// cannot be exercised by a conformance suite without dragging the panel in.
func TestCoreEnginesDoNotImportTheWebLayer(t *testing.T) {
	root := repoRoot(t)
	forbidden := []string{
		modulePrefix + "/internal/web",
		modulePrefix + "/internal/sub",
	}
	var violations []string
	checked := 0
	for _, src := range parseNonTestGo(t, root) {
		if !isCoreEngine(src.Rel) {
			continue
		}
		checked++
		for _, path := range importPaths(src.File) {
			for _, bad := range forbidden {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					violations = append(violations, src.Rel+" imports "+path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no core engine files to check; coreEnginePrefixes no longer matches the tree")
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("%s — a core must not depend on the panel; publish an event or widen the core interface instead", v)
	}
}

// isStdlib reports whether an import path is a standard library package: every
// external path starts with a domain, so its first segment carries a dot.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// TestPolicyRulesImportOnlyStdlib makes "the rules cannot name a protocol" structural
// rather than a convention: a package that imports nothing with one cannot name one.
func TestPolicyRulesImportOnlyStdlib(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	checked := 0
	for _, src := range parseNonTestGo(t, root) {
		if !strings.HasPrefix(src.Rel, "internal/policy/") {
			continue
		}
		checked++
		for _, path := range importPaths(src.File) {
			if !isStdlib(path) {
				violations = append(violations, src.Rel+" imports "+path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("parsed no files under internal/policy; the rules fence is vacuous")
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("%s — internal/policy is stdlib only; convert to its own value types at the service layer instead of importing the thing that has them", v)
	}
}

// shapingAllowed is everything the mechanism package may reach for: the kernel,
// the log, and itself. x/sys carries the ETH_P_* constants netlink does not, and
// is the same dependency internal/egress's own plane already has.
var shapingAllowed = []string{
	"github.com/vishvananda/netlink",
	"github.com/vishvananda/netns",
	"golang.org/x/sys/unix",
	modulePrefix + "/internal/logger",
	modulePrefix + "/internal/shaping",
}

/*
TestShapingImportsAreFenced keeps the mechanism ignorant.

internal/shaping drives qdiscs, classes and filters and must never learn what an
email, a Kind, a tier or a quota is — so it may not import internal/core,
internal/database, internal/web, or internal/policy, which holds the rules it
exists to be the mechanism for. Seeded at zero while that is still free.
*/
func TestShapingImportsAreFenced(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	checked := 0
	for _, src := range parseNonTestGo(t, root) {
		if !strings.HasPrefix(src.Rel, "internal/shaping/") {
			continue
		}
		checked++
		for _, path := range importPaths(src.File) {
			if isStdlib(path) || slices.Contains(shapingAllowed, path) {
				continue
			}
			violations = append(violations, src.Rel+" imports "+path)
		}
	}
	if checked == 0 {
		t.Fatal("parsed no files under internal/shaping; the mechanism fence is vacuous")
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("%s — internal/shaping is the mechanism and knows only devices, selectors and bits per second; take the value as an argument instead of importing the thing that has it", v)
	}
}

// TestDataLayerDoesNotImportACore is the fence P0 unlocked. internal/database
// imported internal/xray for ClientTraffic and InboundConfig, so every future core
// transitively depended on Xray through the model layer and no fence was
// expressible. Both types now live in internal/core — the contract, not a core.
func TestDataLayerDoesNotImportACore(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	checked := 0
	for _, src := range parseNonTestGo(t, root) {
		if !strings.HasPrefix(src.Rel, "internal/database/") {
			continue
		}
		checked++
		for _, path := range importPaths(src.File) {
			// Trailing slash so a package path matches a directory prefix.
			if isCoreEngine(strings.TrimPrefix(path, modulePrefix+"/") + "/") {
				violations = append(violations, src.Rel+" imports "+path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("checked no files under internal/database; the fence is vacuous")
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("%s — the data layer must not import a core; put the shared type in internal/core instead, as ClientTraffic and InboundConfig now are", v)
	}
}
