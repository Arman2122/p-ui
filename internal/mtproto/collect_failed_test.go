package mtproto

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

// A running sidecar whose /stats cannot be read is named, not skipped in silence.
func TestCollectTrafficNamesTheSidecarItCouldNotRead(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a real child process to stand in for a running sidecar")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start a stand-in process: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	mgr := &Manager{procs: map[int]*managed{}, swept: true}
	mgr.procs[4] = &managed{proc: &Process{cmd: cmd}, tag: "mtp-4", apiPort: serverPort(t, srv), counter: core.NewCounter()}

	_, _, failed := mgr.CollectTraffic()
	if len(failed) != 1 || failed[0] != "mtp-4" {
		t.Fatalf("failed = %v, want [mtp-4]: a scrape that fails silently is a client that is not billed", failed)
	}
}
