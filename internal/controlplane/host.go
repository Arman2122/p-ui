package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type HostConfig struct {
	Version          uint32 `json:"version"`
	AuthorityFile    string `json:"authorityFile"`
	ControlListen    string `json:"controlListen"`
	EnrollmentListen string `json:"enrollmentListen"`
	ControlAddress   string `json:"controlAddress"`
	EnrollmentURL    string `json:"enrollmentURL"`
	ServerName       string `json:"serverName,omitempty"`
}

func validEndpoint(endpoint string, bind bool) (string, bool) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", false
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", false
	}
	if bind {
		return host, host == "" || net.ParseIP(host) != nil
	}
	return host, host != "" && (net.ParseIP(host) != nil || len(host) <= 253 && dnsName.MatchString(host))
}

func (c HostConfig) names() ([]string, error) {
	host, valid := validEndpoint(c.ControlAddress, false)
	if c.Version != 1 || !filepath.IsAbs(c.AuthorityFile) || !valid || c.ControlListen == c.EnrollmentListen {
		return nil, ErrInvalid
	}
	if _, ok := validEndpoint(c.ControlListen, true); !ok {
		return nil, ErrInvalid
	}
	if _, ok := validEndpoint(c.EnrollmentListen, true); !ok {
		return nil, ErrInvalid
	}
	u, err := url.Parse(c.EnrollmentURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "/enroll" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return nil, ErrInvalid
	}
	endpoint := u.Host
	if u.Port() == "" {
		endpoint = net.JoinHostPort(u.Hostname(), "443")
	}
	enrollHost, ok := validEndpoint(endpoint, false)
	if !ok {
		return nil, ErrInvalid
	}
	if c.ServerName != "" {
		if net.ParseIP(c.ServerName) == nil && !dnsName.MatchString(c.ServerName) {
			return nil, ErrInvalid
		}
		host = c.ServerName
	}
	return []string{host, enrollHost}, nil
}

func ReadHostConfig(path string) (HostConfig, error) {
	var config HostConfig
	file, err := os.Open(path)
	if err != nil {
		return config, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return config, err
	}
	if len(encoded) > 64<<10 {
		return config, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, ErrInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return config, ErrInvalid
	}
	_, err = config.names()
	return config, err
}

type Host struct {
	config      HostConfig
	store       *Store
	service     *Service
	material    AuthorityMaterial
	tls         *tls.Config
	issuer      *Issuer
	certificate atomic.Pointer[tls.Certificate]
}

func NewHost(ctx context.Context, store *Store, config HostConfig, activation ...ActivationHandler) (*Host, error) {
	if store == nil {
		return nil, ErrInvalid
	}
	names, err := config.names()
	if err != nil {
		return nil, err
	}
	material, err := LoadAuthorityFile(config.AuthorityFile)
	if err != nil {
		return nil, err
	}
	issuer, err := material.issuer(time.Now())
	if err != nil {
		return nil, err
	}
	if err := store.BindAuthority(ctx, material.ControlPlaneID, issuer.Certificate); err != nil {
		return nil, err
	}
	material, err = RefreshAuthorityFile(config.AuthorityFile, names, time.Now())
	if err != nil {
		return nil, err
	}
	tlsConfig, issuer, err := material.TLS(names, time.Now())
	if err != nil {
		return nil, err
	}
	service, err := NewService(store, material.ControlPlaneID, activation...)
	if err != nil {
		return nil, err
	}
	host := &Host{config: config, store: store, service: service, material: material, tls: tlsConfig, issuer: issuer}
	host.certificate.Store(&tlsConfig.Certificates[0])
	tlsConfig.Certificates = nil
	tlsConfig.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return host.certificate.Load(), nil }
	return host, nil
}

func (h *Host) Service() *Service      { return h.service }
func (h *Host) ControlPlaneID() string { return h.material.ControlPlaneID }

func (h *Host) Enroll(ctx context.Context, name string) (credentials.EnrollmentPackage, error) {
	result, err := h.store.CreateEnrollment(ctx, name)
	if err != nil {
		return credentials.EnrollmentPackage{}, err
	}
	return credentials.EnrollmentPackage{Configuration: credentials.Config{
		Identity:       identity.Principal{NodeID: result.NodeID, InstallationID: result.InstallationID, ControlPlaneID: h.material.ControlPlaneID},
		ControlAddress: h.config.ControlAddress, ServerName: h.config.ServerName, EnrollmentURL: h.config.EnrollmentURL, TrustBundlePEM: bytes.Clone(h.material.CA),
	}, Ticket: result.Ticket, ExpiresUnixMilli: result.ExpiresUnixMilli}, nil
}

func (h *Host) Run(ctx context.Context) error {
	listener, bootstrap, err := h.listen(ctx)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer bootstrap.Close()
	return h.Serve(ctx, listener, bootstrap)
}

func (h *Host) listen(ctx context.Context) (net.Listener, net.Listener, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", h.config.ControlListen)
	if err != nil {
		return nil, nil, fmt.Errorf("bind node control listener: %w", err)
	}
	bootstrap, err := (&net.ListenConfig{}).Listen(ctx, "tcp", h.config.EnrollmentListen)
	if err != nil {
		_ = listener.Close()
		return nil, nil, fmt.Errorf("bind node enrollment listener: %w", err)
	}
	return listener, bootstrap, nil
}

func (h *Host) Serve(ctx context.Context, control, bootstrap net.Listener) error {
	server, err := NewGRPCServer(h.service, h.tls, h.issuer)
	if err != nil {
		return err
	}
	handler, err := NewEnrollmentHandler(h.store, h.material.ControlPlaneID, h.issuer)
	if err != nil {
		return err
	}
	https := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	results := make(chan error, 2)
	go func() { results <- server.Serve(control) }()
	go func() { results <- https.Serve(tls.NewListener(bootstrap, h.tls.Clone())) }()
	var cause error
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
running:
	for {
		select {
		case <-ctx.Done():
			break running
		case cause = <-results:
			break running
		case <-ticker.C:
			if err := h.refresh(time.Now()); err != nil {
				cause = err
				break running
			}
		}
	}
	_ = https.Close()
	stopped := make(chan struct{})
	go func() { server.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		server.Stop()
		<-stopped
	}
	if cause == nil || errors.Is(cause, http.ErrServerClosed) || errors.Is(cause, grpc.ErrServerStopped) {
		return nil
	}
	return cause
}

func (h *Host) refresh(now time.Time) error {
	names, err := h.config.names()
	if err != nil {
		return err
	}
	material, err := RefreshAuthorityFile(h.config.AuthorityFile, names, now)
	if err != nil {
		return err
	}
	if material.ControlPlaneID != h.material.ControlPlaneID || !bytes.Equal(material.CA, h.material.CA) {
		return ErrAuthority
	}
	config, _, err := material.TLS(names, now)
	if err != nil {
		return err
	}
	h.certificate.Store(&config.Certificates[0])
	return nil
}
