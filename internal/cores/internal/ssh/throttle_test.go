package ssh

import (
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
	c, inst := startCore(t, `{"transport":"tcp"}`, userN(0))
	clock := time.Now()
	c.mu.Lock()
	srv := c.instances[inst.ID].srv
	c.mu.Unlock()
	srv.throttle.now = func() time.Time { return clock }

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
	clock = clock.Add(throttleBan + time.Second)
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
