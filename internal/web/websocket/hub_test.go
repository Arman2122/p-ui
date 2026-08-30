package websocket

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/op/go-logging"

	puilogger "github.com/Arman2122/p-ui/internal/logger"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("PUI_LOG_FOLDER", os.TempDir())
	puilogger.InitLogger(logging.ERROR)
	os.Exit(m.Run())
}

func TestNewClient_HasBufferedSendChannel(t *testing.T) {
	c := NewClient("client-1")
	if c.ID != "client-1" {
		t.Fatalf("ID = %q, want client-1", c.ID)
	}
	if cap(c.Send) != clientSendQueue {
		t.Fatalf("Send cap = %d, want %d", cap(c.Send), clientSendQueue)
	}
}

func TestHub_NilReceiver_DoesNotPanic(t *testing.T) {
	var h *Hub
	if h.GetClientCount() != 0 {
		t.Fatal("nil hub GetClientCount should return 0")
	}
	h.Broadcast(MessageTypeStatus, "anything")
	h.Register(NewClient("x"))
	h.Unregister(NewClient("x"))
	h.Stop()
}

func TestHub_BroadcastDropsWhenNoClients(t *testing.T) {
	h := NewHub()
	defer h.Stop()
	go h.Run()

	h.Broadcast(MessageTypeStatus, "payload")

	select {
	case <-h.broadcast:
		t.Fatal("Broadcast should drop when client count is zero")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHub_BroadcastDropsNilPayload(t *testing.T) {
	h := NewHub()
	defer h.Stop()
	go h.Run()

	c := NewClient("c1")
	h.Register(c)
	waitClientCount(t, h, 1)

	h.Broadcast(MessageTypeStatus, nil)

	select {
	case <-c.Send:
		t.Fatal("nil payload should be dropped, not delivered")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHub_BroadcastDeliversToClient(t *testing.T) {
	h := NewHub()
	defer h.Stop()
	go h.Run()

	c := NewClient("c1")
	h.Register(c)
	waitClientCount(t, h, 1)

	h.Broadcast(MessageTypeStatus, map[string]string{"k": "v"})

	select {
	case raw := <-c.Send:
		var m Message
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("payload is not valid JSON: %v\n%s", err, raw)
		}
		if m.Type != MessageTypeStatus {
			t.Fatalf("Type = %q, want %q", m.Type, MessageTypeStatus)
		}
		if m.Time == 0 {
			t.Fatal("Time should be set to a non-zero unix-millis value")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for broadcast to reach client")
	}
}

func TestHub_UnregisterClosesSendAndDecrementsCount(t *testing.T) {
	h := NewHub()
	defer h.Stop()
	go h.Run()

	c := NewClient("c1")
	h.Register(c)
	waitClientCount(t, h, 1)

	h.Unregister(c)
	waitClientCount(t, h, 0)

	select {
	case _, ok := <-c.Send:
		if ok {
			t.Fatal("expected Send channel to be closed after Unregister")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Send channel was not closed after Unregister")
	}
}

func TestHub_StopClosesAllClients(t *testing.T) {
	h := NewHub()
	go h.Run()

	c1 := NewClient("c1")
	c2 := NewClient("c2")
	h.Register(c1)
	h.Register(c2)
	waitClientCount(t, h, 2)

	h.Stop()

	for _, c := range []*Client{c1, c2} {
		select {
		case _, ok := <-c.Send:
			if ok {
				t.Fatalf("client %s Send should be closed after Stop", c.ID)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("client %s Send not closed after Stop", c.ID)
		}
	}
}

func TestHub_ShouldThrottle(t *testing.T) {
	h := NewHub()
	defer h.Stop()

	if h.shouldThrottle(MessageTypeStatus) {
		t.Fatal("non-gated message type should never throttle")
	}
	if h.shouldThrottle(MessageTypeStatus) {
		t.Fatal("non-gated message type should never throttle on second call")
	}

	if h.shouldThrottle(MessageTypeTraffic) {
		t.Fatal("first call for gated type should not throttle")
	}
	if !h.shouldThrottle(MessageTypeTraffic) {
		t.Fatal("immediate second call for gated type should throttle")
	}
}

func TestHub_ShouldThrottle_DistinctTypesIndependent(t *testing.T) {
	h := NewHub()
	defer h.Stop()

	if h.shouldThrottle(MessageTypeTraffic) {
		t.Fatal("first Traffic call should not throttle")
	}
	if h.shouldThrottle(MessageTypeInbounds) {
		t.Fatal("first Inbounds call should not throttle even after Traffic")
	}
}

func TestTrySend_SucceedsWithRoom(t *testing.T) {
	c := &Client{ID: "c", Send: make(chan []byte, 1)}
	if !trySend(c, []byte("hi")) {
		t.Fatal("trySend should succeed when buffer has room")
	}
}

func TestTrySend_FailsWhenFull(t *testing.T) {
	c := &Client{ID: "c", Send: make(chan []byte, 1)}
	c.Send <- []byte("first")
	if trySend(c, []byte("second")) {
		t.Fatal("trySend should fail when buffer is full")
	}
}

func TestTrySend_FailsOnClosedChannel(t *testing.T) {
	c := &Client{ID: "c", Send: make(chan []byte, 1)}
	close(c.Send)
	if trySend(c, []byte("after-close")) {
		t.Fatal("trySend should fail (not panic) when channel is closed")
	}
}

func TestHub_FanoutEvictsSlowClient(t *testing.T) {
	h := NewHub()
	defer h.Stop()
	go h.Run()

	slow := &Client{ID: "slow", Send: make(chan []byte, 1)}
	slow.Send <- []byte("buffer-already-full")
	h.Register(slow)
	waitClientCount(t, h, 1)

	h.Broadcast(MessageTypeStatus, "payload")
	waitClientCount(t, h, 0)

	select {
	case _, ok := <-slow.Send:
		if ok {
			_, ok = <-slow.Send
			if ok {
				t.Fatal("slow client Send should eventually be closed by fanout eviction")
			}
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("slow client Send channel was not closed")
	}
}

func TestHub_ConcurrentRegisterUnregister(t *testing.T) {
	h := NewHub()
	defer h.Stop()
	go h.Run()

	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c := NewClient("c")
			h.Register(c)
			h.Unregister(c)
		}(i)
	}
	wg.Wait()
	waitClientCount(t, h, 0)
}

func waitClientCount(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h.GetClientCount() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("client count never reached %d (last seen %d)", want, h.GetClientCount())
}

/*
A throttled mutation push must leave something behind.

Broadcast's throttle drops any gated type that lands within minBroadcastInterval
of the previous one, and its doc justified that with "the next legitimate
mutation will push the fresh state". That holds for traffic and clientStats,
which a collection tick repeats a second or two later. It does not hold for
inbounds and outbounds: nothing repeats them, so two saves inside the same
250 ms window left every OTHER open session showing the old list until something
unrelated changed or the operator reloaded the page.

An invalidate is the same signal an oversized payload already sends and costs
the client one REST refetch. F344.
*/
func TestHub_ThrottledMutationPushFallsBackToInvalidate(t *testing.T) {
	h := NewHub()
	defer h.Stop()
	go h.Run()

	c := NewClient("c1")
	h.Register(c)
	waitClientCount(t, h, 1)

	// The first push goes out in full and arms the throttle.
	h.Broadcast(MessageTypeInbounds, []string{"first"})
	if m := readMessage(t, c); m.Type != MessageTypeInbounds {
		t.Fatalf("first push Type = %q, want %q", m.Type, MessageTypeInbounds)
	}

	// The second lands inside the window and used to vanish.
	h.Broadcast(MessageTypeInbounds, []string{"second"})
	m := readMessage(t, c)
	if m.Type != MessageTypeInvalidate {
		t.Fatalf("second push Type = %q, want %q: a dropped inbounds push is never repeated, so every other session keeps the old list", m.Type, MessageTypeInvalidate)
	}
}

// And a timer-driven type still just drops: its next tick carries the whole
// state, so an invalidate there would be a REST refetch for nothing.
func TestHub_ThrottledTimerPushStillDrops(t *testing.T) {
	h := NewHub()
	defer h.Stop()
	go h.Run()

	c := NewClient("c1")
	h.Register(c)
	waitClientCount(t, h, 1)

	h.Broadcast(MessageTypeTraffic, map[string]int{"up": 1})
	if m := readMessage(t, c); m.Type != MessageTypeTraffic {
		t.Fatalf("first push Type = %q, want %q", m.Type, MessageTypeTraffic)
	}

	h.Broadcast(MessageTypeTraffic, map[string]int{"up": 2})
	select {
	case raw := <-c.Send:
		t.Fatalf("a throttled traffic push produced %s; the next collection tick already carries it", raw)
	case <-time.After(200 * time.Millisecond):
	}
}

func readMessage(t *testing.T, c *Client) Message {
	t.Helper()
	select {
	case raw := <-c.Send:
		var m Message
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("payload is not valid JSON: %v\n%s", err, raw)
		}
		return m
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for a message")
	}
	return Message{}
}
