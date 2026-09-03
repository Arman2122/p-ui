package crypto

import (
	"crypto/tls"
	"os"
	"sync"
	"time"
)

// CertReloader hands every handshake the pair currently on disk, re-reading the
// files when either changes, so a renewed certificate needs no panel restart.
type CertReloader struct {
	certFile, keyFile string

	mu       sync.Mutex
	cert     *tls.Certificate
	certMod  time.Time
	keyMod   time.Time
	checked  time.Time
	interval time.Duration
	lastErr  error
}

// NewCertReloader starts from a pair the caller already loaded and validated;
// that keeps the listener's own "does the pair load at all" gate in charge.
func NewCertReloader(certFile, keyFile string, initial tls.Certificate) *CertReloader {
	r := &CertReloader{certFile: certFile, keyFile: keyFile, cert: &initial, interval: 2 * time.Second}
	r.certMod, r.keyMod, _ = modTimes(certFile, keyFile)
	r.checked = time.Now()
	return r
}

// GetCertificate is the tls.Config callback. A pair that fails to load, which is
// what a half-written renewal looks like, keeps the last good pair serving.
func (r *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Sub(r.checked) < r.interval {
		return r.cert, nil
	}
	r.checked = now
	certMod, keyMod, err := modTimes(r.certFile, r.keyFile)
	if err != nil {
		r.lastErr = err
		return r.cert, nil
	}
	if certMod.Equal(r.certMod) && keyMod.Equal(r.keyMod) {
		return r.cert, nil
	}
	pair, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		r.lastErr = err
		return r.cert, nil
	}
	r.cert, r.certMod, r.keyMod, r.lastErr = &pair, certMod, keyMod, nil
	return r.cert, nil
}

// Err reports why the pair on disk is not the pair being served, or nil.
func (r *CertReloader) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

func modTimes(certFile, keyFile string) (time.Time, time.Time, error) {
	cert, err := os.Stat(certFile)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	key, err := os.Stat(keyFile)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return cert.ModTime(), key.ModTime(), nil
}
