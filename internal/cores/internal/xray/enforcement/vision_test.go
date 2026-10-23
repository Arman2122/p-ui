package enforcement

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

type tlsBoundary struct {
	net.Conn
	uploaded, downloaded atomic.Uint64
}

func (c *tlsBoundary) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	c.downloaded.Add(uint64(n))
	return n, err
}

func (c *tlsBoundary) Write(data []byte) (int, error) {
	n, err := c.Conn.Write(data)
	c.uploaded.Add(uint64(n))
	return n, err
}

type visionFixture struct {
	account     *limits.Account
	connections []*tls.Conn
	boundaries  []*tlsBoundary
	armed       chan struct{}
	release     chan struct{}
}

func visionDestination(t *testing.T, armed, release chan struct{}) (string, *tls.Config) {
	t.Helper()
	server, _ := protocolTLS(t)
	material := server["certificates"].([]map[string]any)[0]
	certificate := []byte(strings.Join(material["certificate"].([]string), "\n"))
	secret := []byte(strings.Join(material["key"].([]string), "\n"))
	pair, err := tls.X509KeyPair(certificate, secret)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		t.Fatal("test destination certificate was not trusted")
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, SessionTicketsDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var live sync.Map
	wg.Go(func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			live.Store(connection, true)
			wg.Go(func() {
				defer live.Delete(connection)
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
				for {
					var header [5]byte
					if _, err := io.ReadFull(connection, header[:]); err != nil {
						return
					}
					n := int(binary.BigEndian.Uint32(header[1:]))
					if n > 1<<20 {
						return
					}
					data := bytes.Repeat([]byte("v"), n)
					switch header[0] {
					case 'E':
						if _, err := io.ReadFull(connection, data); err != nil {
							return
						}
					case 'D':
					case 'P':
						select {
						case armed <- struct{}{}:
						case <-t.Context().Done():
							return
						}
						select {
						case <-release:
						case <-t.Context().Done():
							return
						}
					default:
						return
					}
					if _, err := connection.Write(data); err != nil {
						return
					}
				}
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		live.Range(func(key, value any) bool { _ = key.(net.Conn).Close(); return true })
		wg.Wait()
	})
	return listener.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, ServerName: "runtime.test", RootCAs: roots, DynamicRecordSizingDisabled: true}
}

func newVisionFixture(t *testing.T) *visionFixture {
	t.Helper()
	fixture := &visionFixture{armed: make(chan struct{}, 1), release: make(chan struct{})}
	var err error
	fixture.account, err = limits.New(limits.Policy{Term: "vision-term", Revision: 1, Enabled: true, QuotaUnlimited: true, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	destination, clientTLS := visionDestination(t, fixture.armed, fixture.release)
	for _, raw := range configuredTLSVLESSConnections(t, fixture.account, "xtls-rprx-vision", destination) {
		boundary := &tlsBoundary{Conn: raw}
		connection := tls.Client(boundary, clientTLS.Clone())
		if err := connection.HandshakeContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		if connection.ConnectionState().Version != tls.VersionTLS13 {
			t.Fatal("destination did not negotiate TLS1.3")
		}
		fixture.connections = append(fixture.connections, connection)
		fixture.boundaries = append(fixture.boundaries, boundary)
		t.Cleanup(func() { _ = connection.Close() })
	}
	for range 8 {
		for _, connection := range fixture.connections {
			if _, err := visionExchange(connection, 'E', 32<<10); err != nil {
				t.Fatal(err)
			}
		}
	}
	fixture.exactCounters(t)
	return fixture
}

func visionExchange(connection *tls.Conn, mode byte, size int) (int, error) {
	header := binary.BigEndian.AppendUint32([]byte{mode}, uint32(size))
	if mode == 'E' {
		header = append(header, bytes.Repeat([]byte("v"), size)...)
	}
	if _, err := connection.Write(header); err != nil {
		return 0, err
	}
	data := make([]byte, size)
	n, err := io.ReadFull(connection, data)
	if err == nil && !bytes.Equal(data, bytes.Repeat([]byte("v"), size)) {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func (f *visionFixture) exactCounters(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		var uploaded, downloaded uint64
		for _, boundary := range f.boundaries {
			uploaded += boundary.uploaded.Load()
			downloaded += boundary.downloaded.Load()
		}
		got := f.account.Snapshot()
		if got.AdmittedUpload == uploaded && got.MeasuredUpload == uploaded && got.AdmittedDownload == downloaded && got.MeasuredDownload == downloaded {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Vision TLS stream accounting=%+v boundary upload=%d download=%d", got, uploaded, downloaded)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestVLESSVisionTLS13Interoperability(t *testing.T) {
	t.Run("transfer and close", func(t *testing.T) {
		f := newVisionFixture(t)
		for _, connection := range f.connections {
			if err := connection.Close(); err != nil {
				t.Fatalf("regular TLS close: %v", err)
			}
		}
	})
	t.Run("shared quota", func(t *testing.T) {
		f := newVisionFixture(t)
		before := f.account.Snapshot()
		policy := before.Policy
		policy.Revision++
		policy.QuotaUnlimited = false
		policy.CreditCeiling = before.AdmittedUpload + before.AdmittedDownload + 128<<10
		if err := f.account.Apply(policy); err != nil {
			t.Fatal(err)
		}
		type result struct {
			n   int
			err error
		}
		results := make(chan result, len(f.connections))
		for _, connection := range f.connections {
			go func() { n, err := visionExchange(connection, 'D', 256<<10); results <- result{n, err} }()
		}
		total := 0
		for range f.connections {
			result := <-results
			total += result.n
			if result.err == nil {
				t.Fatal("configured Vision quota did not exhaust")
			}
		}
		if total > 128<<10 {
			t.Fatalf("Vision quota delivered %d application bytes beyond remaining authority", total)
		}
		after := f.account.Snapshot()
		if after.AdmittedUpload+after.AdmittedDownload > policy.CreditCeiling || after.MeasuredUpload+after.MeasuredDownload > policy.CreditCeiling {
			t.Fatalf("Vision quota counters exceeded ceiling: %+v", after)
		}
	})
	for _, direction := range []string{"upload", "download"} {
		t.Run("shared "+direction+" rate", func(t *testing.T) {
			f := newVisionFixture(t)
			before := f.account.Snapshot()
			policy := before.Policy
			policy.Revision++
			rate := limits.Rate{Mode: limits.Limited, BitsPerSecond: 1 << 20, BurstBytes: 4096}
			if direction == "upload" {
				policy.Upload = rate
			} else {
				policy.Download = rate
			}
			if err := f.account.Apply(policy); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			results := make(chan error, len(f.connections))
			for _, connection := range f.connections {
				go func() { _, err := visionExchange(connection, 'E', 64<<10); results <- err }()
			}
			for range f.connections {
				if err := <-results; err != nil {
					t.Fatal(err)
				}
			}
			elapsed := time.Since(started)
			f.exactCounters(t)
			after := f.account.Snapshot()
			wireBytes := after.AdmittedUpload - before.AdmittedUpload
			if direction == "download" {
				wireBytes = after.AdmittedDownload - before.AdmittedDownload
			}
			minimum := time.Duration((wireBytes - uint64(rate.BurstBytes)) * uint64(time.Second) / (rate.BitsPerSecond / 8))
			if elapsed+50*time.Millisecond < minimum {
				t.Fatalf("configured Vision %s rate: %d TLS bytes in %s, minimum %s", direction, wireBytes, elapsed, minimum)
			}
		})
	}
	t.Run("configured revoke", func(t *testing.T) {
		f := newVisionFixture(t)
		header := binary.BigEndian.AppendUint32([]byte{'P'}, 64<<10)
		if _, err := f.connections[0].Write(header); err != nil {
			t.Fatal(err)
		}
		select {
		case <-f.armed:
		case <-time.After(2 * time.Second):
			t.Fatal("local destination did not receive the command")
		}
		before := f.account.Snapshot()
		policy := before.Policy
		policy.Revision++
		policy.Enabled = false
		if err := f.account.Apply(policy); err != nil {
			t.Fatal(err)
		}
		close(f.release)
		var one [1]byte
		if n, err := io.ReadFull(f.connections[0], one[:]); n != 0 || err == nil {
			t.Fatalf("revoked Vision connection delivered %d bytes: %v", n, err)
		}
		after := f.account.Snapshot()
		if after.AdmittedDownload != before.AdmittedDownload || after.MeasuredDownload != before.MeasuredDownload {
			t.Fatalf("revoked Vision counters grew: before=%+v after=%+v", before, after)
		}
	})
}
