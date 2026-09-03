package ssh

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Arman2122/p-ui/internal/util/crypto"
)

const bare101 = "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"

// listen opens the instance's transport and feeds every accepted connection to
// the server as a plain net.Conn. The SSH layer never learns which it came over.
func listen(inst listenSpec, s *server) (net.Listener, error) {
	addr := net.JoinHostPort(inst.listen, strconv.Itoa(inst.port))
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}
	switch s.settings.Transport {
	case TransportTCP:
		s.serve(ln)
		return ln, nil
	case TransportWSS:
		pair, err := tls.LoadX509KeyPair(s.settings.CertFile, s.settings.KeyFile)
		if err != nil {
			_ = ln.Close()
			return nil, err
		}
		reloader := crypto.NewCertReloader(s.settings.CertFile, s.settings.KeyFile, pair)
		ln = tls.NewListener(ln, &tls.Config{GetCertificate: reloader.GetCertificate, MinVersion: tls.VersionTLS12})
		fallthrough
	default:
		serveHTTP(ln, s)
		return ln, nil
	}
}

type listenSpec struct {
	listen string
	port   int
}

// serveHTTP answers the two WebSocket dialects on one port. A request carrying
// Sec-WebSocket-Key is a real client, or a CDN, and gets RFC 6455 framing; one
// without is an HTTP-Injector-class client that wants a bare 101 and then raw
// bytes on the same socket.
func serveHTTP(ln net.Listener, s *server) {
	s.ln = ln
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.settings.Path != "" && r.URL.Path != s.settings.Path {
				http.NotFound(w, r)
				return
			}
			if r.Header.Get("Sec-WebSocket-Key") != "" {
				ws, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				s.handle(&wsConn{ws: ws})
				return
			}
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "upgrade unavailable", http.StatusInternalServerError)
				return
			}
			raw, brw, err := hj.Hijack()
			if err != nil {
				return
			}
			if _, err := io.WriteString(raw, bare101); err != nil {
				_ = raw.Close()
				return
			}
			s.handle(&bufferedConn{Conn: raw, r: brw.Reader})
		}),
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = srv.Serve(ln)
	}()
	// Serve returns only when the listener closes, which stop does; the
	// in-flight connections are the server's own and are closed with it.
}

// bufferedConn returns what the HTTP reader already pulled off the socket before
// the raw bytes: without it the client's first SSH packet can vanish.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// wsConn presents a framed WebSocket as a net.Conn: every SSH write is one
// binary message, and reads drain messages in order.
type wsConn struct {
	ws   *websocket.Conn
	rest io.Reader
}

func (c *wsConn) Read(p []byte) (int, error) {
	for {
		if c.rest != nil {
			n, err := c.rest.Read(p)
			if err == io.EOF {
				c.rest = nil
				if n == 0 {
					continue
				}
				return n, nil
			}
			return n, err
		}
		kind, r, err := c.ws.NextReader()
		if err != nil {
			return 0, err
		}
		if kind != websocket.BinaryMessage && kind != websocket.TextMessage {
			continue
		}
		c.rest = r
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error         { return c.ws.Close() }
func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }
func (c *wsConn) SetDeadline(t time.Time) error {
	return errors.Join(c.ws.SetReadDeadline(t), c.ws.SetWriteDeadline(t))
}
func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }
