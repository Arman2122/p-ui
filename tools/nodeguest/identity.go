package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	grpccredentials "google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/encoding/protojson"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func makeIdentity() (fixtureState, error) {
	var state fixtureState
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return state, err
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Disposable Penhoon VM test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, caKey.Public(), caKey)
	if err != nil {
		return state, err
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		return state, err
	}
	root := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	state.CAKey, err = x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		return state, err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return state, err
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err = x509.CreateCertificate(rand.Reader, template, ca, serverKey.Public(), caKey)
	if err != nil {
		return state, err
	}
	state.ServerCertificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		return state, err
	}
	state.ServerKey = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	ticket := make([]byte, 32)
	if _, err := rand.Read(ticket); err != nil {
		return state, err
	}
	state.Enrollment = credentials.EnrollmentPackage{Configuration: credentials.Config{Identity: identity.Principal{ControlPlaneID: uuid.NewString(), NodeID: uuid.NewString(), InstallationID: uuid.NewString()}, ControlAddress: "127.0.0.1:7443", ServerName: "localhost", EnrollmentURL: "https://localhost:7444/enroll", TrustBundlePEM: root}, Ticket: base64.RawURLEncoding.EncodeToString(ticket), ExpiresUnixMilli: now.Add(4 * time.Hour).UnixMilli()}
	return state, nil
}

type guestStats struct {
	Enrollments          int             `json:"enrollments"`
	Sessions             int             `json:"sessions"`
	Heartbeats           int             `json:"heartbeats"`
	JournalRuntimes      int             `json:"journalRuntimes"`
	AuthorityReports     int             `json:"authorityReports"`
	TimeSynchronizations int             `json:"timeSynchronizations"`
	CreditPolls          int             `json:"creditPolls"`
	CertificateSerials   map[string]bool `json:"certificateSerials"`
}

type guestServer struct {
	nodev1.UnimplementedControlServiceServer
	nodev1.UnimplementedAccountingServiceServer
	mu            sync.Mutex
	state         fixtureState
	stats         guestStats
	requestID     string
	requestDigest string
	response      []byte
	journals      *nodev1.JournalRegistration
	reports       map[string]*nodev1.AuthorityReport
}

func (s *guestServer) Connect(stream grpc.BidiStreamingServer[nodev1.NodeMessage, nodev1.MasterMessage]) error {
	p, err := identity.FromContext(stream.Context(), s.state.Enrollment.Configuration.Identity.ControlPlaneID)
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil || hello.ProtocolVersion != compatibility.Protocol || p.Require(hello.NodeId, hello.InstallationId) != nil {
		return identity.ErrIdentity
	}
	s.mu.Lock()
	if s.journals == nil || s.journals.AgentStateJournalId != hello.AgentStateJournalId {
		s.mu.Unlock()
		return identity.ErrIdentity
	}
	s.stats.Sessions++
	s.stats.CertificateSerials[p.Serial] = true
	s.mu.Unlock()
	if err := stream.Send(&nodev1.MasterMessage{Payload: &nodev1.MasterMessage_Authority{Authority: &nodev1.SessionAuthority{NodeId: p.NodeID, ControlPlaneId: p.ControlPlaneID, Generation: 1, ProtocolVersion: compatibility.Protocol}}}); err != nil {
		return err
	}
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		if message.GetHeartbeat() != nil {
			s.mu.Lock()
			s.stats.Heartbeats++
			s.mu.Unlock()
		}
	}
}

func (s *guestServer) Report(stream grpc.BidiStreamingServer[nodev1.UsageBatch, nodev1.UsageReceipt]) error {
	p, err := identity.FromContext(stream.Context(), s.state.Enrollment.Configuration.Identity.ControlPlaneID)
	if err != nil {
		return err
	}
	for {
		batch, err := stream.Recv()
		if err != nil {
			return err
		}
		if p.Require(batch.NodeId, batch.InstallationId) != nil || len(batch.Checkpoints) != 0 {
			return errors.New("guest fixture accepts idle accounting only")
		}
		if err := stream.Send(&nodev1.UsageReceipt{DurableSequence: batch.Sequence, AccountedSequence: batch.Sequence}); err != nil {
			return err
		}
	}
}

func (s *guestServer) enroll(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	request := new(nodev1.EnrollRequest)
	if err != nil || protojson.Unmarshal(body, request) != nil || request.Ticket != s.state.Enrollment.Ticket || request.InstallationId != s.state.Enrollment.Configuration.Identity.InstallationID {
		http.Error(w, "invalid synthetic enrollment", http.StatusForbidden)
		return
	}
	digest, err := identity.RequestKeyDigest(request.CertificateRequestPem)
	if err != nil {
		http.Error(w, "invalid CSR", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.response != nil {
		if request.RequestId != s.requestID || string(digest) != s.requestDigest {
			http.Error(w, "enrollment already used", http.StatusForbidden)
			return
		}
		_, _ = w.Write(s.response)
		return
	}
	block, _ := pem.Decode(s.state.Enrollment.Configuration.TrustBundlePEM)
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		http.Error(w, "fixture CA", http.StatusInternalServerError)
		return
	}
	key, err := x509.ParsePKCS8PrivateKey(s.state.CAKey)
	if err != nil {
		http.Error(w, "fixture key", http.StatusInternalServerError)
		return
	}
	chain, err := identity.Issue(ca, key.(*ecdsa.PrivateKey), request.CertificateRequestPem, s.state.Enrollment.Configuration.Identity, time.Now())
	if err != nil {
		http.Error(w, "fixture issuance", http.StatusInternalServerError)
		return
	}
	response, err := protojson.Marshal(&nodev1.EnrollResponse{NodeId: s.state.Enrollment.Configuration.Identity.NodeID, ControlPlaneId: s.state.Enrollment.Configuration.Identity.ControlPlaneID, CertificateChainPem: chain, TrustBundlePem: s.state.Enrollment.Configuration.TrustBundlePEM, ProtocolVersion: compatibility.Protocol})
	if err != nil {
		http.Error(w, "fixture response", http.StatusInternalServerError)
		return
	}
	s.requestID, s.requestDigest, s.response = request.RequestId, string(digest), response
	s.stats.Enrollments++
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(response)
}

func serve(ctx context.Context, dir string, state fixtureState) error {
	certificate, err := tls.X509KeyPair(state.ServerCertificate, state.ServerKey)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(state.Enrollment.Configuration.TrustBundlePEM)
	server := &guestServer{state: state, stats: guestStats{CertificateSerials: make(map[string]bool)}}
	grpcServer := grpc.NewServer(grpc.Creds(grpccredentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots})))
	nodev1.RegisterControlServiceServer(grpcServer, server)
	nodev1.RegisterAccountingServiceServer(grpcServer, server)
	nodev1.RegisterAuthorityServiceServer(grpcServer, &guestAuthority{server: server})
	nodev1.RegisterTimeServiceServer(grpcServer, &guestAuthority{server: server})
	nodev1.RegisterCreditServiceServer(grpcServer, &guestAuthority{server: server})
	listen := new(net.ListenConfig)
	listener, err := listen.Listen(ctx, "tcp", "127.0.0.1:7443")
	if err != nil {
		return err
	}
	defer listener.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /enroll", server.enroll)
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
		server.mu.Lock()
		defer server.mu.Unlock()
		_ = json.NewEncoder(w).Encode(server.stats)
	})
	mux.Handle("GET /metadata/", http.StripPrefix("/metadata/", http.FileServer(http.Dir(filepath.Join(dir, "repository", "metadata")))))
	mux.Handle("GET /targets/", http.StripPrefix("/targets/", http.FileServer(http.Dir(filepath.Join(dir, "repository", "targets")))))
	httpsTCP, err := listen.Listen(ctx, "tcp", "127.0.0.1:7444")
	if err != nil {
		return err
	}
	httpsListener := tls.NewListener(httpsTCP, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}})
	defer httpsListener.Close()
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer httpServer.Close()
	defer grpcServer.Stop()
	errors := make(chan error, 2)
	go func() { errors <- grpcServer.Serve(listener) }()
	go func() { errors <- httpServer.Serve(httpsListener) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errors:
		return err
	}
}
