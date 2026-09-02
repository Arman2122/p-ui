package controller

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// The response flags every debt a save left: a node to sync, a restart to take.
func TestPendingObjCarriesBothFlags(t *testing.T) {
	if pendingObj(false, false) != nil {
		t.Fatal("nothing pending must be nil, so the page shows the plain success toast")
	}
	both, _ := pendingObj(true, true).(gin.H)
	if both["nodePending"] != true || both["restartPending"] != true {
		t.Fatalf("pendingObj(true, true) = %v", both)
	}
	restartOnly, _ := pendingObj(false, true).(gin.H)
	if _, has := restartOnly["nodePending"]; has || restartOnly["restartPending"] != true {
		t.Fatalf("pendingObj(false, true) = %v", restartOnly)
	}
}
