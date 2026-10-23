package runtime

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/netsafe"
)

func TestCachedMtlsClientReloadsChangedProvider(t *testing.T) {
	cert := masterCertForTest(t)
	SetMasterClientCertProvider(func() (tls.Certificate, error) { return cert, nil })
	t.Cleanup(func() { SetMasterClientCertProvider(nil) })
	client, err := HTTPClientForNode(&model.Node{Scheme: "https", TlsVerifyMode: "mtls"}, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	do := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(netsafe.ContextWithAllowPrivate(context.Background(), true), http.MethodGet, srv.URL, nil)
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
	var calls atomic.Int64
	SetMasterClientCertProvider(func() (tls.Certificate, error) {
		calls.Add(1)
		return cert, nil
	})
	resp, err := do()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if got := calls.Load(); got != 1 {
		t.Fatalf("cached client read replacement provider %d times, want 1", got)
	}
	rejected := errors.New("replacement certificate rejected")
	SetMasterClientCertProvider(func() (tls.Certificate, error) { return tls.Certificate{}, rejected })
	if resp, err := do(); !errors.Is(err, rejected) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Fatalf("cached client kept an old credential: error = %v, want replacement failure", err)
	}
}
