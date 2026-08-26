package web

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

/*
A supervise tick must not outlive the cores it supervises.

`CoreSuperviseJob` runs every ten seconds, reads the desired instance set from
the database, and calls `Reconcile` on every non-panel-converged core — which
starts whatever it finds stopped. It takes `context.Background()`, so cancelling
the server's context does not reach it.

Shutdown stopped the cores first and the scheduler second, and `cron.Stop()`
only prevents NEW runs: a tick already inside `Reconcile` finishes afterwards and
relaunches the sidecars that were just stopped. They then outlive the panel,
holding the ports the next start needs. F262.
*/

func TestStopCronWaitsForAJobAlreadyRunning(t *testing.T) {
	var finished atomic.Bool
	release := make(chan struct{})
	started := make(chan struct{})

	c := cron.New(cron.WithSeconds())
	if _, err := c.AddFunc("* * * * * *", func() {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		finished.Store(true)
	}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	c.Start()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the job never ran; this test is checking the wrong thing")
	}

	// Let the job finish shortly after the drain begins, so a stopCron that does
	// not wait returns first and a stopCron that waits returns second.
	go func() {
		time.Sleep(150 * time.Millisecond)
		close(release)
	}()

	(&Server{cron: c}).stopCron()

	if !finished.Load() {
		t.Fatal("shutdown returned while a cron job was still running: a supervise tick in flight relaunches the cores that were just stopped, and the sidecars outlive the panel")
	}
}

// A nil scheduler is the StopPanelOnly-before-Start case and must not panic.
func TestStopCronToleratesNoScheduler(t *testing.T) {
	(&Server{}).stopCron()
}

/*
And the ordering itself, which the test above cannot express: stopping the
scheduler has to come before stopping the cores, or the drain merely waits for
the tick that is busy undoing the shutdown.

Asserted on the source because reproducing it needs a live registry of
supervised cores and a ten-second scheduler; the property is "these two
statements are in this order", which is exactly what the source says.
*/
func TestTheSchedulerIsStoppedBeforeTheCores(t *testing.T) {
	body, err := os.ReadFile("web.go")
	if err != nil {
		t.Fatalf("read web.go: %v", err)
	}
	source := string(body)

	start := strings.Index(source, "func (s *Server) stop(stopXray bool, stopTgBot bool) error {")
	if start < 0 {
		t.Fatal("the shutdown function was renamed; this guard is now vacuous")
	}
	end := strings.Index(source[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of the shutdown function; this guard is now vacuous")
	}
	stop := source[start : start+end]

	cronAt := strings.Index(stop, "s.stopCron()")
	coresAt := strings.Index(stop, "s.stopCores()")
	if cronAt < 0 || coresAt < 0 {
		t.Fatal("shutdown no longer stops the scheduler or the cores; this guard is now vacuous")
	}
	if cronAt > coresAt {
		t.Fatal("shutdown stops the cores before the scheduler: the supervise job runs every 10s and relaunches whatever it finds stopped, so the sidecars come back up and outlive the panel")
	}
}
