package network

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/logger"
)

type failingListener struct{ err error }

func (l failingListener) Accept() (net.Conn, error) { return nil, l.err }
func (failingListener) Close() error                { return nil }
func (failingListener) Addr() net.Addr              { return &net.TCPAddr{} }

func TestServeHTTPReportsUnexpectedFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		closed    bool
		wantError bool
	}{
		{"listener failure", errors.New("accept failed"), false, true},
		{"normal shutdown", errors.New("accept must not run"), true, false},
		{"wrapped shutdown", fmt.Errorf("listener: %w", http.ErrServerClosed), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &http.Server{}
			if tc.closed {
				if err := server.Close(); err != nil {
					t.Fatal(err)
				}
			}
			name := t.Name()
			countErrors := func() int {
				count := 0
				for _, line := range logger.GetLogs(100, "error") {
					if strings.Contains(line, name+" stopped unexpectedly: ") {
						if !strings.HasSuffix(line, name+" stopped unexpectedly: "+tc.err.Error()) {
							t.Fatalf("error log lost its listener failure: %s", line)
						}
						count++
					}
				}
				return count
			}
			before := countErrors()
			ServeHTTP(server, failingListener{err: tc.err}, name)
			want := 0
			if tc.wantError {
				want = 1
			}
			if got := countErrors() - before; got != want {
				t.Fatalf("recorded %d errors, want %d", got, want)
			}
		})
	}
}
