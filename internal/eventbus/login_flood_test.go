package eventbus

import (
	"strconv"
	"testing"
	"time"
)

/*
login.attempt was exempt from the cooldown, on both subscribers.

/login is unauthenticated, so anyone on the internet could loop requests against
it and have the panel mail its operator and message every Telegram admin once
per round trip, indefinitely. The Telegram half is the worse one: exceeding the
bot's own rate limit gets it throttled, which silently kills xray.crash and
node.down too — a denial of monitoring, not just noise.

The exemption is gone. What replaces it cannot key on the source, which is the
one part of the event an attacker chooses.
*/
func TestALoginFloodIsHeldToOneNotification(t *testing.T) {
	limiter := NewRateLimiter(time.Minute)
	allowed := 0
	for i := range 500 {
		// A new address every request, which is what a flood looks like and what
		// keying on Source would have treated as 500 unrelated events.
		event := Event{
			Type:   EventLoginAttempt,
			Source: netFor(i),
			Data:   &LoginEventData{IP: netFor(i), Status: "fail"},
		}
		if limiter.Allow(event.Type, LimiterKey(event)) {
			allowed++
		}
	}
	if allowed != 1 {
		t.Fatalf("%d of 500 rotating-source attempts got through the cooldown, want 1", allowed)
	}
}

/*
And the reason the bucket is the outcome rather than a constant: an operator
signing in while a flood is running must still be told about it. One shared
bucket would have let the attacker's failures swallow the one login event that
matters.
*/
func TestARealSignInIsNotSwallowedByTheFlood(t *testing.T) {
	limiter := NewRateLimiter(time.Minute)
	for i := range 100 {
		event := Event{Type: EventLoginAttempt, Source: netFor(i), Data: &LoginEventData{Status: "fail"}}
		limiter.Allow(event.Type, LimiterKey(event))
	}

	success := Event{Type: EventLoginAttempt, Source: "203.0.113.7", Data: &LoginEventData{Status: "success"}}
	if !limiter.Allow(success.Type, LimiterKey(success)) {
		t.Fatal("a successful sign-in was suppressed by the failed attempts flooding alongside it")
	}
}

// Every other event still keys on its source, so two cores crashing are two
// notifications rather than one.
func TestOtherEventsStillKeyOnTheirSource(t *testing.T) {
	limiter := NewRateLimiter(time.Minute)
	for _, source := range []string{"node-a", "node-b"} {
		event := Event{Type: EventXrayCrash, Source: source}
		if !limiter.Allow(event.Type, LimiterKey(event)) {
			t.Fatalf("the crash on %s was suppressed; it is a different source from the other", source)
		}
	}
	repeat := Event{Type: EventXrayCrash, Source: "node-a"}
	if limiter.Allow(repeat.Type, LimiterKey(repeat)) {
		t.Fatal("the same source crashed twice inside the cooldown and both were reported")
	}
}

// A login event with no data at all must not become an un-bucketed event.
func TestALoginAttemptWithNoDataStillHasABucket(t *testing.T) {
	limiter := NewRateLimiter(time.Minute)
	bare := Event{Type: EventLoginAttempt, Source: "198.51.100.1"}
	if !limiter.Allow(bare.Type, LimiterKey(bare)) {
		t.Fatal("the first one should pass")
	}
	other := Event{Type: EventLoginAttempt, Source: "198.51.100.2"}
	if limiter.Allow(other.Type, LimiterKey(other)) {
		t.Fatal("a second data-less attempt from a different address got its own bucket")
	}
}

// A distinct address per request, which is the point: an attacker rotating
// source addresses is exactly what keying on Source failed to bucket.
func netFor(i int) string {
	return "198.51." + strconv.Itoa(i/256) + "." + strconv.Itoa(i%256)
}
