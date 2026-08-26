package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
The depletion and renewal paths dispatch by core, never straight to Xray.

Three call sites here used `s.xrayApi.DelInbound(tag)`,
`s.xrayApi.RemoveUser(tag, email)` and `s.xrayApi.AddUser(...)` with no filter on
protocol. Every depleted local inbound had its tag sent to Xray whatever core
served it, so a depleted MTProto or kernel WireGuard client was struck from the
database and left connected in the core that actually serves them — while the
failed Xray call set needRestart and bounced the whole core on every poll.

The audit filed this as multi-NODE (F044, F086, F110, F121, F240, F247, F339 —
seven findings over three sites). It is not: these queries have always been
node-scoped and the client path has always split local from remote. It is
multi-CORE, and routing through runtime.Runtime is what resolves the owner.

This is a ratchet on files already converted, not a general fence: ~35 files
under internal/web legitimately import internal/xray for its config types, and
the state-changing API shares that package with them, so no import rule can
express the difference. Naming the converted files keeps them converted.
*/
func TestDepletionPathsDoNotReachPastTheRuntime(t *testing.T) {
	root := repoRoot(t)
	converted := []string{
		filepath.Join("internal", "web", "service", "inbound_disable.go"),
		filepath.Join("internal", "web", "service", "inbound_traffic.go"),
	}

	// Parsed, not grepped. The first version of this guard scanned text and
	// flagged its own doc comment, which is the same blindness that let a
	// declared-and-never-emitted diagnostic survive for months (ledger L6/L7).
	for _, rel := range converted {
		path := filepath.Join(root, rel)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			selector, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			inner, ok := selector.X.(*ast.SelectorExpr)
			if !ok || inner.Sel.Name != "xrayApi" {
				return true
			}
			t.Errorf("%s:%d calls the Xray API directly (.%s). Dispatch through runtime.Runtime, which resolves the core that serves the inbound: a tag sent straight to Xray is a no-op for every other core, and the client stays connected.",
				rel, fset.Position(selector.Pos()).Line, selector.Sel.Name)
			return true
		})
	}
}

/*
And InboundService carries no Xray handle to reach for.

The field was the affordance: as long as `s.xrayApi` existed, the shortest path
from a new depletion rule to a working demo was to use it, and the reviewer had
to notice. Removing it makes the wrong thing unavailable rather than discouraged.
*/
func TestTheInboundServiceHoldsNoXrayHandle(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "internal", "web", "service", "inbound.go"))
	if err != nil {
		t.Fatalf("read inbound.go: %v", err)
	}

	struct_, _, found := strings.Cut(string(body), "\n}\n")
	if !found || !strings.Contains(struct_, "type InboundService struct") {
		t.Fatal("InboundService is no longer the first declaration in inbound.go; this guard needs re-aiming")
	}
	if strings.Contains(struct_, "xrayApi") {
		t.Fatal("InboundService holds an Xray API handle again: every depletion rule written from here will reach for it, and it is wrong for every core but one")
	}
}
