package mtproto

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Arman2122/mtg-proxy/mtglib"
	"github.com/Arman2122/mtg-proxy/nodeweb"
	proxyproto "github.com/pires/go-proxyproto"

	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/privatefile"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type WEB = configuration.MTProtoWEB

type webRuntime struct {
	origin         *nodeweb.Origin
	proxy          *mtglib.Proxy
	backend        *memoryBackend
	server         *http.Server
	listener       *trackedListener
	done           sync.WaitGroup
	exited         atomic.Bool
	once           sync.Once
	certificate    atomic.Pointer[tls.Certificate]
	coverTransport *http.Transport
}

func webSecrets(secrets mtglib.SecretConfig) []mtglib.Secret {
	names := make([]string, 0, len(secrets.Secrets))
	for name := range secrets.Secrets {
		names = append(names, name)
	}
	slices.Sort(names)
	profiles := make([]mtglib.Secret, 0, len(names))
	for _, name := range names {
		profiles = append(profiles, secrets.Secrets[name])
	}
	return profiles
}

func validateWEB(inbound Inbound) error {
	web := inbound.WEB
	if web == nil {
		return nil
	}
	host, port, err := net.SplitHostPort(web.Bind)
	ip, ipErr := netip.ParseAddr(host)
	number, portErr := strconv.Atoi(port)
	canonical, hostErr := nodeweb.CanonicalHost(web.Host)
	cover, coverErr := url.Parse(web.CoverURL)
	certificate, certificateErr := web.Certificate()
	if certificateErr != nil {
		return certificateErr
	}
	if err != nil || ipErr != nil || ip.Is4In6() || ip.Zone() != "" || ip.IsMulticast() || portErr != nil || number < 1 || number > 65535 ||
		web.Bind != net.JoinHostPort(ip.String(), strconv.Itoa(number)) ||
		hostErr != nil || canonical != web.Host || coverErr != nil || cover.Scheme != "https" || cover.Hostname() == "" || cover.User != nil ||
		web.MaxSessions < 0 || web.MaxSessions > 16384 || web.MaxStreams < 0 || web.MaxStreams > 512 || certificate == nil && !ip.IsLoopback() || certificate != nil && web.PublicPort == 0 {
		return errors.New("invalid WEB relay origin or bounds")
	}
	if err := (nodeweb.Limits{MaxPayload: web.MaxPayload, BatchBytes: web.BatchBytes}).Validate(); err != nil {
		return err
	}
	secrets, err := secretsOf(inbound)
	if err != nil {
		return err
	}
	profiles := webSecrets(secrets)
	signer, err := nodeweb.NewSigner(make([]byte, 32))
	if err != nil {
		return err
	}
	_, err = nodeweb.New(nodeweb.Options{Host: web.Host, PublicPort: web.PublicPort, Secrets: profiles, Signer: signer, Cover: http.NotFoundHandler()})
	return err
}

func webSigner(directory, inboundID string, anchors ...*state.Store) (*nodeweb.Signer, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("WEB relay requires private durable runtime storage")
	}
	id := sha256.Sum256([]byte(inboundID))
	name := "web-" + hex.EncodeToString(id[:])
	path := filepath.Join(directory, name+".key")
	var anchor *state.Store
	var previous []byte
	if len(anchors) > 1 {
		return nil, state.ErrIdentity
	}
	if len(anchors) == 1 {
		anchor = anchors[0]
	}
	if anchor != nil {
		var err error
		previous, err = anchor.MaterialDigest(name)
		if err != nil {
			return nil, err
		}
	}
	file, err := privatefile.Open(path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		if previous != nil {
			return nil, state.ErrIdentity
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		file, err = os.CreateTemp(directory, ".web-key-")
		if err != nil {
			return nil, err
		}
		temporary := file.Name()
		defer os.Remove(temporary)
		_, writeErr := file.Write(key)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return nil, err
		}
		if err := privatefile.RenameNew(temporary, path); err != nil {
			if errors.Is(err, os.ErrExist) {
				return webSigner(directory, inboundID, anchors...)
			}
			return nil, err
		}
		if err := privatefile.SyncDirectory(directory); err != nil {
			return nil, err
		}
		return webSigner(directory, inboundID, anchors...)
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	key, err := io.ReadAll(io.LimitReader(file, 33))
	if err != nil {
		return nil, err
	}
	if err := privatefile.SyncDirectory(directory); err != nil {
		return nil, err
	}
	signer, err := nodeweb.NewSigner(key)
	if err != nil {
		return nil, err
	}
	if anchor != nil {
		digest := sha256.Sum256(key)
		if err := anchor.BindMaterial(name, digest[:]); err != nil {
			return nil, err
		}
	}
	return signer, nil
}

func startWEB(ctx context.Context, inbound Inbound, secrets mtglib.SecretConfig, proxy *mtglib.Proxy, effectiveNetwork mtglib.Network, logger mtglib.Logger, directory string, anchors ...*state.Store) (*webRuntime, error) {
	if effectiveNetwork == nil {
		return nil, errors.New("WEB cover requires an explicit node network")
	}
	certificate, err := inbound.WEB.Certificate()
	if err != nil {
		return nil, err
	}
	signer, err := webSigner(directory, inbound.ID, anchors...)
	if err != nil {
		return nil, err
	}
	profiles := webSecrets(secrets)
	web := &webRuntime{proxy: proxy, backend: newMemoryBackend()}
	web.certificate.Store(certificate)
	fail := func(err error) (*webRuntime, error) { web.close(); return nil, err }
	cover, err := nodeweb.UpstreamCover(inbound.WEB.CoverURL)
	if err != nil {
		return fail(err)
	}
	reverse, ok := cover.(*httputil.ReverseProxy)
	if !ok {
		return fail(errors.New("unexpected WEB cover transport contract"))
	}
	web.coverTransport = &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, protocol, address string) (net.Conn, error) {
			dial, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if request, ok := ctx.Value(webCoverContext{}).(context.Context); ok {
				stop := context.AfterFunc(request, cancel)
				defer stop()
			}
			return effectiveNetwork.DialContext(dial, protocol, address)
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 30 * time.Second, ExpectContinueTimeout: time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		MaxIdleConns:           64, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 64, ForceAttemptHTTP2: true,
	}
	reverse.Transport = webCoverRoundTripper{transport: web.coverTransport}
	web.origin, err = nodeweb.New(nodeweb.Options{
		Host: inbound.WEB.Host, PublicPort: inbound.WEB.PublicPort, Secrets: profiles, Signer: signer, Cover: cover, Backend: web.backend,
		Limits:      nodeweb.Limits{MaxPayload: inbound.WEB.MaxPayload, BatchBytes: inbound.WEB.BatchBytes},
		MaxSessions: inbound.WEB.MaxSessions, MaxStreams: inbound.WEB.MaxStreams, DialTimeout: 5 * time.Second, Logger: logger, ClientIP: webSocketAddress,
	})
	if err != nil {
		return fail(err)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", inbound.WEB.Bind)
	if err != nil {
		return fail(err)
	}
	web.listener = &trackedListener{Listener: listener, connections: make(map[*trackedConnection]bool)}
	var served net.Listener = &proxyproto.Listener{Listener: web.listener, ReadHeaderTimeout: 5 * time.Second, ConnPolicy: func(options proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
		host, _, err := net.SplitHostPort(options.Upstream.String())
		ip, parseErr := netip.ParseAddr(host)
		if err != nil || parseErr != nil || !ip.IsLoopback() {
			return proxyproto.REJECT, errors.New("untrusted WEB relay peer")
		}
		return proxyproto.REQUIRE, nil
	}}
	var handler http.Handler = web.origin
	if certificate != nil {
		served = tls.NewListener(web.listener, &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			current := web.certificate.Load()
			if current == nil {
				return nil, net.ErrClosed
			}
			return current, nil
		}})
		handler = webHTTPSHost(inbound.WEB.Host, inbound.WEB.PublicPort, handler)
	}
	web.server = &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10,
	}
	web.done.Go(func() {
		defer web.exited.Store(true)
		_ = proxy.ServeWithPolicy(web.backend, mtglib.ListenerPolicy{Modes: mtglib.ModeSetOf(mtglib.SecretModeDD), Public: true})
	})
	web.done.Go(func() { defer web.exited.Store(true); _ = web.server.Serve(served) })
	return web, nil
}

func (w *webRuntime) replace(secrets mtglib.SecretConfig, config *WEB) error {
	certificate, err := config.Certificate()
	if err != nil {
		return err
	}
	if err := w.origin.SetSecrets(webSecrets(secrets)); err != nil {
		return err
	}
	w.certificate.Store(certificate)
	return nil
}

func webSocketAddress(request *http.Request) net.Addr {
	address, err := netip.ParseAddrPort(request.RemoteAddr)
	if err != nil {
		return nil
	}
	return net.TCPAddrFromAddrPort(address)
}

type webCoverContext struct{}

type webCoverRoundTripper struct{ transport *http.Transport }

// Transport detaches dial cancellation for pooling; retain the initiating request's cancellation.
func (t webCoverRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx := context.WithValue(request.Context(), webCoverContext{}, request.Context())
	return t.transport.RoundTrip(request.Clone(ctx))
}

func webHTTPSHost(host string, publicPort uint16, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		name, port := request.Host, uint64(443)
		if strings.Contains(name, ":") {
			var value string
			var err error
			name, value, err = net.SplitHostPort(name)
			if err != nil {
				http.Error(response, "misdirected request", http.StatusMisdirectedRequest)
				return
			}
			port, err = strconv.ParseUint(value, 10, 16)
			if err != nil {
				http.Error(response, "misdirected request", http.StatusMisdirectedRequest)
				return
			}
		}
		canonical, err := nodeweb.CanonicalHost(name)
		if err != nil || canonical != host || uint16(port) != publicPort {
			http.Error(response, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (w *webRuntime) close() {
	w.once.Do(func() {
		w.certificate.Store(nil)
		if w.server != nil {
			_ = w.server.Close()
		}
		if w.listener != nil {
			_ = w.listener.Close()
		}
		if w.coverTransport != nil {
			w.coverTransport.CloseIdleConnections()
		}
		_ = w.backend.Close()
		w.done.Wait()
	})
}
