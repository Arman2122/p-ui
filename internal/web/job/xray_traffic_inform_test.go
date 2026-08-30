package job

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/util/netsafe"

	"github.com/valyala/fasthttp"
)

func stallingListener(t *testing.T) (addr string, release func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				<-done
				_ = c.Close()
			}(conn)
			go func(c net.Conn) { _, _ = io.Copy(io.Discard, c) }(conn)
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func TestExternalInformClient_StalledReceiverTimesOut(t *testing.T) {
	// The dial is SSRF-guarded now, and this fixture is a loopback listener.
	// Allow private for this test only; the refusal itself is covered below.
	prev := informDialContext
	informDialContext = func() context.Context {
		return netsafe.ContextWithAllowPrivate(context.Background(), true)
	}
	t.Cleanup(func() { informDialContext = prev })

	addr, release := stallingListener(t)
	defer release()

	request := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(request)
	request.Header.SetMethod("POST")
	request.Header.SetContentType("application/json; charset=UTF-8")
	request.SetBody([]byte(`{"clientTraffics":[],"inboundTraffics":[]}`))
	request.SetRequestURI("http://" + addr + "/inform")
	request.Header.SetConnectionClose()

	response := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(response)

	start := time.Now()
	err := externalInformClient.DoTimeout(request, response, externalInformTimeout)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a receiver that never answers must produce an error, not a completed request")
	}
	if !errors.Is(err, fasthttp.ErrTimeout) {
		t.Errorf("want a timeout error, got %v", err)
	}
	if elapsed > externalInformTimeout+2*time.Second {
		t.Errorf("call took %v, must be bounded by %v", elapsed, externalInformTimeout)
	}
}

func TestExternalInformClient_HasDeadlines(t *testing.T) {
	if externalInformClient.ReadTimeout <= 0 || externalInformClient.WriteTimeout <= 0 {
		t.Fatal("the traffic-notify client must carry read and write deadlines")
	}
	if externalInformTimeout >= 5*time.Second {
		t.Errorf("the call budget (%v) must stay under the 5s traffic poll cadence", externalInformTimeout)
	}
}

func TestInformSkippedWhenNothingToReport(t *testing.T) {
	j := &XrayTrafficJob{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		j.informTrafficToExternalAPI(nil, nil)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("an empty payload must return before touching settings or the network")
	}
}

/*
And the same client refuses a private address at dial time.

The inform URL is already checked against the public-address policy before the
request, so a private one cannot be configured. What the dial guard adds is the
window between that check and the connection: a hostname that answers publicly
when the panel validates it and privately a moment later reaches the panel's own
loopback, its metadata service, or anything else on the LAN.

Loopback stands in for "private" here; netsafe applies one rule to the whole
private range. F355.
*/
func TestExternalInformClient_RefusesAPrivateAddressAtDialTime(t *testing.T) {
	addr, release := stallingListener(t)
	defer release()

	request := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(request)
	request.Header.SetMethod("POST")
	request.SetRequestURI("http://" + addr + "/inform")
	response := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(response)

	err := externalInformClient.DoTimeout(request, response, externalInformTimeout)
	if err == nil {
		t.Fatal("the inform client connected to a private address; a hostname that resolves privately after the URL check reaches the panel's own loopback")
	}
	if !strings.Contains(err.Error(), "blocked private") {
		t.Errorf("the refusal did not come from the dial guard, so this proves nothing about it: %v", err)
	}
}
