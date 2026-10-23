package localrpc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var ErrPeer = errors.New("local runtime peer rejected")

type listener struct {
	net.Listener
	uid uint32
}

func Listen(ctx context.Context, path string, allowedUID uint32) (net.Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrPeer
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, ErrPeer
		}
		probe, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", path)
		if err == nil {
			_ = probe.Close()
			return nil, errors.New("local runtime socket already active")
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, err
		}
		if err := removeStaleSocket(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	l, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = l.Close()
		return nil, err
	}
	return &listener{Listener: l, uid: allowedUID}, nil
}

func (l *listener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if verifyPeer(connection, l.uid) == nil {
			return connection, nil
		}
		_ = connection.Close()
	}
}

func Client(path string, serverUID uint32) (*http.Client, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrPeer
	}
	transport := &http.Transport{
		MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: time.Minute,
		ResponseHeaderTimeout: time.Minute, MaxResponseHeaderBytes: 16 << 10,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", path)
			if err != nil {
				return nil, err
			}
			if err := verifyPeer(connection, serverUID); err != nil {
				_ = connection.Close()
				return nil, err
			}
			return connection, nil
		},
	}
	return &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrPeer }}, nil
}
