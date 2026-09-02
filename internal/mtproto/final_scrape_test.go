package mtproto

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
The bytes a client moved since the last scrape must survive the process that
counted them.

mtg keeps its per-secret counters in memory and exposes them only over /stats.
Remove, StopAll and a structural restart all stopped the process without a
final read, so every byte since the previous CollectTraffic -- up to a whole
polling interval, for every client on that inbound -- was never billed. F090.

The process here is a zero Process that reports not running: what is under
test is the scrape and the hand-off, not mtg.
*/
func TestAStoppedProcessIsBilledForItsLastInterval(t *testing.T) {
	var mu sync.Mutex
	up, down := int64(1_000), int64(2_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, `{"started_at":"boot-1","users":{"alice":{"connections":1,"bytes_in":%d,"bytes_out":%d}}}`, up, down)
	}))
	defer srv.Close()

	mgr := &Manager{procs: map[int]*managed{}, swept: true}
	counter := core.NewCounter()
	mgr.procs[9] = &managed{proc: &Process{}, tag: "mtp-9", apiPort: serverPort(t, srv), counter: counter}

	// The baseline a periodic scrape would have set. CollectTraffic itself
	// skips a process that is not running, and this one never was.
	if deltas := counter.Observe("boot-1", map[string]int64{"alice" + upKey: up, "alice" + downKey: down}); len(deltas) != 0 {
		t.Fatalf("the opening observation billed %+v", deltas)
	}
	mu.Lock()
	up, down = 1_500, 2_500
	mu.Unlock()

	mgr.Remove(9)
	if _, still := mgr.procs[9]; still {
		t.Fatal("Remove left the process in the map")
	}

	deltas, _, _ := mgr.CollectTraffic()
	var total int64
	for _, d := range deltas {
		if d.Email != "alice" || d.Tag != "mtp-9" {
			t.Fatalf("delta attributed to %q on %q", d.Email, d.Tag)
		}
		total += d.Up + d.Down
	}
	if total != 1_000 {
		t.Fatalf("billed %d bytes after the stop, want the 1000 moved since the last scrape", total)
	}

	// Handed over once, never again.
	if again, _, _ := mgr.CollectTraffic(); len(again) != 0 {
		t.Fatalf("the final interval was billed a second time: %+v", again)
	}
}
