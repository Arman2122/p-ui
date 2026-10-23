package mtproto

import (
	"context"
	"net"
	"sync"
)

type memoryBackend struct {
	connections chan net.Conn
	done        chan struct{}
	once        sync.Once
}

type attributedConnection struct {
	net.Conn
	peer net.Addr
}

func (c attributedConnection) RemoteAddr() net.Addr { return c.peer }
func (c attributedConnection) LocalAddr() net.Addr  { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{connections: make(chan net.Conn), done: make(chan struct{})}
}

func (b *memoryBackend) Dial(ctx context.Context, peer net.Addr) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	case <-b.done:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	case b.connections <- attributedConnection{Conn: server, peer: peer}:
		return client, nil
	}
}

func (b *memoryBackend) Accept() (net.Conn, error) {
	select {
	case <-b.done:
		return nil, net.ErrClosed
	case connection := <-b.connections:
		return connection, nil
	}
}

func (*memoryBackend) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (b *memoryBackend) Close() error { b.once.Do(func() { close(b.done) }); return nil }

type trackedListener struct {
	net.Listener
	mu          sync.Mutex
	closed      bool
	connections map[*trackedConnection]bool
}

type trackedConnection struct {
	net.Conn
	listener *trackedListener
}

func (l *trackedListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tracked := &trackedConnection{Conn: connection, listener: l}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		_ = connection.Close()
		return nil, net.ErrClosed
	}
	l.connections[tracked] = true
	return tracked, nil
}

func (l *trackedListener) Close() error {
	err := l.Listener.Close()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	for connection := range l.connections {
		_ = connection.Conn.Close()
		delete(l.connections, connection)
	}
	return err
}

func (c *trackedConnection) Close() error {
	err := c.Conn.Close()
	c.listener.mu.Lock()
	delete(c.listener.connections, c)
	c.listener.mu.Unlock()
	return err
}
