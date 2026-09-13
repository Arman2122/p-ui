package ssh

import (
	"sync/atomic"
	"testing"
	"time"
)

/*
A refused login had no consequence past a toast: a guesser could try passwords
at MaxAuthTries per connection for as long as it liked. A source that fails
five times inside five minutes is now closed before its handshake for ten, and
the right password from that source waits like a wrong one.
*/
func TestASourceThatKeepsFailingIsShutOut(t *testing.T) {
	var skew atomic.Int64
	base, wasClock := time.Now(), throttleClock
	throttleClock = func() time.Time { return base.Add(time.Duration(skew.Load())) }
	t.Cleanup(func() { throttleClock = wasClock })
	_, inst := startCore(t, `{"transport":"tcp"}`, userN(0))

	for i := 0; i < throttleFailures; i++ {
		if client, err := dialTCP(t, inst, "user0", "wrong"); err == nil {
			client.Close()
			t.Fatal("a wrong password logged in")
		}
	}
	if client, err := dialTCP(t, inst, "user0", "secret-0"); err == nil {
		client.Close()
		t.Fatal("the right password logged in from a source that had just failed five times")
	}
	skew.Store(int64(throttleBan + time.Second))
	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatalf("after the ban a right password must log in: %v", err)
	}
	client.Close()
	// A login clears the slate: four more failures are not a ban.
	for i := 0; i < throttleFailures-1; i++ {
		_, _ = dialTCP(t, inst, "user0", "wrong")
	}
	if client, err := dialTCP(t, inst, "user0", "secret-0"); err != nil {
		t.Fatalf("four failures after a login shut the source out: %v", err)
	} else {
		client.Close()
	}
}
