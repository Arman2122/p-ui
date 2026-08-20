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

So the pre-flight has to be asked BEFORE anything is stopped. Moved after the
stop, or dropped for being redundant with the save-time checks, it type-checks,
still passes every functional test, and quietly restores the outage — the
save-time checks cannot see a hand-edited template, a routing rule or an
outbound at all.
*/
const (
	restartFile     = "internal/web/service/xray.go"
	restartFunc     = "RestartXray"
	preflightCall   = "TestConfig"
	processStopCall = "Stop"
)

func TestRestartAsksTheCoreBeforeStoppingTheRunningOne(t *testing.T) {
	body := funcBody(t, restartFile, restartFunc)

	var preflightAt, stopAt []token.Pos
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
		}
		return true
	})

	if len(preflightAt) == 0 {
		t.Fatalf("%s.%s no longer calls xray.%s: a config the core refuses again takes every inbound down instead of being refused",
			restartFile, restartFunc, preflightCall)
	}
	if len(stopAt) == 0 {
		t.Fatalf("%s.%s no longer stops the running process; this guard is now vacuous", restartFile, restartFunc)
	}
	for _, stop := range stopAt {
		if stop < preflightAt[0] {
			t.Fatalf("%s.%s stops the running core before xray.%s answers: a rejected config leaves the box with no core at all",
				restartFile, restartFunc, preflightCall)
		}
	}
}
