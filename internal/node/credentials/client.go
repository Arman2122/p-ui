package credentials

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func (v *Vault) Enroll(ctx context.Context, ticket string) error {
	v.operationMu.Lock()
	defer v.operationMu.Unlock()
	if v.certificate.Load() != nil {
		return errors.New("node already enrolled")
	}
	if len(ticket) != 43 {
		return ErrConfiguration
	}
	pending, err := v.pending()
	if err != nil {
		return err
	}
	body, err := protojson.Marshal(&nodev1.EnrollRequest{
		RequestId: pending.ID, Ticket: ticket,
		CertificateRequestPem: pending.CSR, InstallationId: v.config.Identity.InstallationID,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, v.config.EnrollmentURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: v.roots.Clone(), ServerName: v.config.ServerName},
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, MaxResponseHeaderBytes: 16 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("enrollment redirects are forbidden")
	}}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("node enrollment connection: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("node enrollment rejected: HTTP %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	if err != nil {
		return err
	}
	if len(encoded) > 128<<10 {
		return ErrCertificate
	}
	result := new(nodev1.EnrollResponse)
	if err := protojson.Unmarshal(encoded, result); err != nil {
		return ErrCertificate
	}
	return v.accept(pending.ID, result)
}

func (v *Vault) Renew(ctx context.Context, connection grpc.ClientConnInterface) error {
	v.operationMu.Lock()
	defer v.operationMu.Unlock()
	if v.certificate.Load() == nil {
		return ErrNotEnrolled
	}
	if !time.Now().Before(v.certificate.Load().Leaf.NotAfter) {
		return v.recoverLocked(ctx)
	}
	pending, err := v.pending()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := nodev1.NewIdentityServiceClient(connection).Renew(ctx, &nodev1.RenewRequest{RequestId: pending.ID, CertificateRequestPem: pending.CSR},
		grpc.MaxCallRecvMsgSize(128<<10), grpc.MaxCallSendMsgSize(16<<10))
	if err != nil {
		return err
	}
	return v.accept(pending.ID, response)
}
