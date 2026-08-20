package arch

import (
	"go/ast"
	"go/token"
	"testing"
)

/*
The running core is protected by ORDER, and nothing else protects it.

xray-core loads a config whole: one malformed inbound, routing rule or outbound
aborts core.New, and the panel has already stopped the process it was replacing.
Every inbound of every protocol then stops answering, the bad row is persisted,
and each retry reproduces it exactly.

So the pre-flight has to be asked BEFORE anything is applied and before anything
is stopped. Moved after either, or dropped for being redundant with the
save-time checks, it type-checks, still passes every functional test, and
quietly restores the outage — the save-time checks cannot see a hand-edited
template, a routing rule or an outbound at all.

The hot-apply path counts as "applied": it pushes operations one at a time over
gRPC and, when one fails, leaves a core matching neither its own snapshot nor
the desired config, relying on the restart underneath to clean that up. Asked
after it, the pre-flight can refuse the very restart that repairs it.
*/
const (
	restartFile     = "internal/web/service/xray.go"
	restartFunc     = "RestartXray"
	preflightCall   = "TestConfig"
	processStopCall = "Stop"
	hotApplyCall    = "tryHotApply"
)

func TestRestartAsksTheCoreBeforeStoppingTheRunningOne(t *testing.T) {
	body := funcBody(t, restartFile, restartFunc)

	var preflightAt, stopAt, hotApplyAt []token.Pos
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, _ := sel.X.(*ast.Ident)
		switch {
		case sel.Sel.Name == preflightCall && receiver != nil && receiver.Name == "xray":
			preflightAt = append(preflightAt, call.Pos())
		case sel.Sel.Name == processStopCall && receiver != nil && receiver.Name == "process":
			stopAt = append(stopAt, call.Pos())
		case sel.Sel.Name == hotApplyCall:
			hotApplyAt = append(hotApplyAt, call.Pos())
		}
		return true
	})

	if len(preflightAt) == 0 {
		t.Fatalf("%s.%s no longer calls xray.%s: a config the core refuses again takes every inbound down instead of being refused",
			restartFile, restartFunc, preflightCall)
	}
	for _, after := range []struct {
		name   string
		at     []token.Pos
		damage string
	}{
		{processStopCall, stopAt, "a rejected config leaves the box with no core at all"},
		{hotApplyCall, hotApplyAt, "a rejected config is pushed op by op first, and the restart that would repair a half-applied core is the thing being refused"},
	} {
		if len(after.at) == 0 {
			t.Fatalf("%s.%s no longer calls %s; this guard is now vacuous", restartFile, restartFunc, after.name)
		}
		for _, at := range after.at {
			if at < preflightAt[0] {
				t.Fatalf("%s.%s reaches %s before xray.%s answers: %s",
					restartFile, restartFunc, after.name, preflightCall, after.damage)
			}
		}
	}
}
