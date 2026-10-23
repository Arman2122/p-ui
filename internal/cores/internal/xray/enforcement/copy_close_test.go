package enforcement

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/singbridge"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type checkedBufferClose struct {
	net.Conn
	reading            atomic.Bool
	releasedDuringRead atomic.Bool
	releases           atomic.Int32
	started            chan struct{}
	once               sync.Once
}

type halfClosedPipe struct {
	net.Conn
	writer net.Conn
}

func (c *halfClosedPipe) Write(data []byte) (int, error) { return c.writer.Write(data) }
func (c *halfClosedPipe) CloseWrite() error              { return c.writer.Close() }
func (c *halfClosedPipe) Close() error                   { return errors.Join(c.Conn.Close(), c.writer.Close()) }

func (c *halfClosedPipe) SetDeadline(value time.Time) error {
	return errors.Join(c.Conn.SetDeadline(value), c.writer.SetDeadline(value))
}

func TestXrayStreamCopyPreservesResponseAfterClientHalfClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	aRead, bWrite := net.Pipe()
	bRead, aWrite := net.Pipe()
	raw := &halfClosedPipe{Conn: aRead, writer: aWrite}
	peer := &halfClosedPipe{Conn: bRead, writer: bWrite}
	defer raw.Close()
	defer peer.Close()
	_ = raw.SetDeadline(time.Now().Add(3 * time.Second))
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	connection := &checkedBufferClose{Conn: raw, started: make(chan struct{})}
	requests, requestWriter := pipe.New()
	responses, responseWriter := pipe.New()
	defer common.Interrupt(requests)
	defer common.Interrupt(responses)
	finished := make(chan error, 1)
	go func() {
		finished <- singbridge.CopyConn(ctx, connection, &transport.Link{Reader: responses, Writer: requestWriter}, connection)
	}()
	destination := make(chan error, 1)
	go func() {
		request, err := io.ReadAll(&buf.BufferedReader{Reader: requests})
		if err == nil && !bytes.Equal(request, []byte("request")) {
			err = errors.New("half-close request differs")
		}
		if err == nil {
			buffer := buf.New()
			_, _ = buffer.Write([]byte("response-after-eof"))
			err = responseWriter.WriteMultiBuffer(buf.MultiBuffer{buffer})
		}
		_ = common.Close(responseWriter)
		destination <- err
	}()
	if _, err := peer.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	if err := peer.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(peer)
	if err != nil || !bytes.Equal(response, []byte("response-after-eof")) {
		t.Fatalf("client half-close discarded pending response: %q %v", response, err)
	}
	if err := <-destination; err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if connection.releasedDuringRead.Load() || connection.releases.Load() != 1 {
		t.Fatal("half-close released live protocol buffers")
	}
}

func (c *checkedBufferClose) Upstream() any { return c.Conn }
func (c *checkedBufferClose) Read(data []byte) (int, error) {
	c.reading.Store(true)
	defer c.reading.Store(false)
	c.once.Do(func() { close(c.started) })
	return c.Conn.Read(data)
}

func (c *checkedBufferClose) Close() error {
	c.releases.Add(1)
	if c.reading.Load() {
		c.releasedDuringRead.Store(true)
	}
	return c.Conn.Close()
}

type interruptibleCloseReader struct {
	done chan struct{}
	once sync.Once
}

func (r *interruptibleCloseReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	<-r.done
	return nil, io.ErrClosedPipe
}
func (r *interruptibleCloseReader) Interrupt() { r.once.Do(func() { close(r.done) }) }

func TestXrayStreamCopyJoinsReadsBeforeReleasingBuffers(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	connection := &checkedBufferClose{Conn: left, started: make(chan struct{})}
	reader := &interruptibleCloseReader{done: make(chan struct{})}
	defer reader.Interrupt()
	result := make(chan error, 1)
	go func() {
		result <- singbridge.CopyConn(ctx, connection, &transport.Link{Reader: reader, Writer: buf.NewWriter(io.Discard)}, connection)
	}()
	select {
	case <-connection.started:
	case <-time.After(time.Second):
		t.Fatal("copy reader did not start")
	}
	cancel()
	reader.Interrupt()
	select {
	case err := <-result:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("copy cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("copy cancellation did not join readers")
	}
	if connection.releasedDuringRead.Load() || connection.releases.Load() != 1 {
		t.Fatalf("stream buffers released before copy tasks joined: active=%v releases=%d", connection.releasedDuringRead.Load(), connection.releases.Load())
	}
}
