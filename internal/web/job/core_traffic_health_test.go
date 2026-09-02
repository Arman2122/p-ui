package job

import (
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/web/service"
)

/*
A core that stops reporting traffic is visible, not silent.

The MTProto scrape returned a bare bool and the core returned no error, and
the job logged the few errors it did get at Debug. A sidecar whose /stats had
been unreadable for a day billed nobody and told nobody. The core now names
the sidecars it could not read, and the job records every core's last good
poll and last error where the core view can show them, logging the
transition once instead of every tick. Feature gap A2.
*/
func TestACoreWhoseCollectionFailsIsRecordedAndRecovers(t *testing.T) {
	failing := &fakeCore{id: "flaky", userErr: errors.New("stats unreadable for mtp-1")}
	Cores = registryOf(t, failing)
	t.Cleanup(func() { Cores = nil })

	collectCoreTraffic()
	if _, lastErr := service.CoreTrafficHealth("flaky"); lastErr == "" {
		t.Fatal("a failing collection left no trace: the operator cannot see that nobody on this core is being billed")
	}

	failing.userErr = nil
	collectCoreTraffic()
	seen, lastErr := service.CoreTrafficHealth("flaky")
	if lastErr != "" || seen.IsZero() {
		t.Fatalf("after a good poll: lastErr=%q seen=%v, want the error cleared and the time recorded", lastErr, seen)
	}
}
