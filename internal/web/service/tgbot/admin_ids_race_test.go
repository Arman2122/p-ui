package tgbot

import (
	"sync"
	"testing"
)

/*
The Telegram admin list is read while it is being replaced.

adminIds is a plain package-level slice. Both writers take tgBotMutex; the
readers did not. The load-bearing one is the authorization gate --
slices.Contains(adminIds, tgId) -- which decides whether an incoming Telegram
message is allowed to administer the panel, and it ran with no synchronization
at all while a settings save was publishing a new slice.

A torn read there is not a crash, it is an authorization answer computed from a
slice header and a backing array that do not belong together: the wrong length
against the wrong data. It can say yes to someone it should refuse, and no to an
admin it should admit, and it happens exactly when the admin list is being
changed -- which is the moment the answer matters most.

The broadcast loops had the same problem with an extra edge: sendBackupData
ranged over adminIds and then indexed len(adminIds) separately, so a shrink
between the two could index past the end.

Run under `go test -race`. Without the accessor the detector reports a data race
on adminIds; the assertions here cannot see it on their own, and the test says so
rather than pretending otherwise. F292.
*/
func TestTheAdminGateAndTheWritersDoNotRace(t *testing.T) {
	t.Cleanup(func() {
		tgBotMutex.Lock()
		adminIds = nil
		tgBotMutex.Unlock()
	})

	const rounds = 500
	var wg sync.WaitGroup

	// The writer: exactly what a settings save does.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range rounds {
			next := make([]int64, 0, 4)
			for j := range i%4 + 1 {
				next = append(next, int64(1000+j))
			}
			tgBotMutex.Lock()
			adminIds = next
			tgBotMutex.Unlock()
		}
	}()

	// The reader: the authorization gate, on every inbound Telegram update.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range rounds {
			_ = checkAdmin(1000)
		}
	}()

	// And the broadcast loop, which reads the slice twice per iteration.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range rounds {
			admins := currentAdminIds()
			for i := range admins {
				if i >= len(admins) {
					t.Errorf("the loop index ran past the slice it is ranging over")
				}
			}
		}
	}()

	wg.Wait()
}
