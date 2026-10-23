package credentials

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func (v *Vault) recoveryCall(ctx context.Context, path string, request, response proto.Message) error {
	u, err := url.Parse(v.config.EnrollmentURL)
	if err != nil {
		return err
	}
	u.Path = path
	body, err := protojson.Marshal(request)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: v.roots.Clone()}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrCertificate }}
	result, err := client.Do(r)
	if err != nil {
		return err
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		return ErrCertificate
	}
	data, err := io.ReadAll(io.LimitReader(result.Body, (128<<10)+1))
	if err != nil {
		return err
	}
	if len(data) > 128<<10 {
		return ErrCertificate
	}
	return protojson.Unmarshal(data, response)
}

func recoverySigner(encoded []byte) (crypto.Signer, error) {
	block, rest := pem.Decode(encoded)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrCertificate
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, ErrCertificate
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, ErrCertificate
	}
	return signer, nil
}

func (v *Vault) Recover(ctx context.Context) error {
	v.operationMu.Lock()
	defer v.operationMu.Unlock()
	return v.recoverLocked(ctx)
}

func (v *Vault) recoverLocked(ctx context.Context) error {
	pending, err := v.pending()
	if err != nil {
		return err
	}
	var saved []byte
	if err := v.db.View(func(tx *bolt.Tx) error {
		saved = bytes.Clone(tx.Bucket(vaultBucket).Get([]byte("recovery")))
		return nil
	}); err != nil {
		return err
	}
	if len(saved) != 0 {
		request := new(nodev1.RecoveryRequest)
		if proto.Unmarshal(saved, request) != nil || request.RequestId != pending.ID {
			return ErrPending
		}
		response := new(nodev1.EnrollResponse)
		if err := v.recoveryCall(ctx, "/recover", request, response); err == nil {
			if err := v.accept(pending.ID, response); err == nil {
				return v.clearRecovery()
			} else if !errors.Is(err, ErrCertificate) {
				return err
			}
		}
	}
	challenge := new(nodev1.RecoveryChallenge)
	if err := v.recoveryCall(ctx, "/recover/challenge", &nodev1.RecoveryChallengeRequest{NodeId: v.config.Identity.NodeID, InstallationId: v.config.Identity.InstallationID}, challenge); err != nil {
		return err
	}
	if len(challenge.CurrentKeyDigest) != 32 || challenge.ExpiresUnixMilli <= time.Now().UnixMilli() {
		return ErrCertificate
	}
	keys := [][]byte{pending.Key}
	if err := v.db.View(func(tx *bolt.Tx) error {
		if data := tx.Bucket(vaultBucket).Get([]byte("active")); data != nil {
			var current material
			if err := json.Unmarshal(data, &current); err != nil {
				return err
			}
			keys = append(keys, current.Key)
		}
		return nil
	}); err != nil {
		return err
	}
	var signer crypto.Signer
	for _, key := range keys {
		candidate, err := recoverySigner(key)
		if err != nil {
			return err
		}
		digest, err := identity.PublicKeyDigest(candidate.Public())
		if err != nil {
			return err
		}
		if bytes.Equal(digest, challenge.CurrentKeyDigest) {
			signer = candidate
			break
		}
	}
	if signer == nil {
		return ErrCertificate
	}
	request := &nodev1.RecoveryRequest{NodeId: v.config.Identity.NodeID, InstallationId: v.config.Identity.InstallationID, ChallengeId: challenge.ChallengeId, RequestId: pending.ID, CertificateRequestPem: pending.CSR}
	digest, err := identity.RecoveryDigest(v.config.Identity.ControlPlaneID, challenge, request)
	if err != nil {
		return err
	}
	request.Proof, err = identity.SignRecovery(signer, digest)
	if err != nil {
		return err
	}
	encoded, err := proto.Marshal(request)
	if err != nil {
		return err
	}
	if err := v.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(vaultBucket).Put([]byte("recovery"), encoded) }); err != nil {
		return err
	}
	response := new(nodev1.EnrollResponse)
	if err := v.recoveryCall(ctx, "/recover", request, response); err != nil {
		return err
	}
	if err := v.accept(pending.ID, response); err != nil {
		return err
	}
	return v.clearRecovery()
}

func (v *Vault) clearRecovery() error {
	return v.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(vaultBucket).Delete([]byte("recovery")) })
}
