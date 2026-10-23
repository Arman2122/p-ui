package configuration

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

var ErrMTProtoWEBTLS = errors.New("invalid WEB relay TLS identity")

func (w MTProtoWEB) Certificate() (*tls.Certificate, error) {
	if w.ServerCert == "" && w.ServerKey == "" {
		return nil, nil
	}
	if w.ServerCert == "" || w.ServerKey == "" || len(w.ServerCert)+len(w.ServerKey) > 32768 {
		return nil, ErrMTProtoWEBTLS
	}
	certificate, err := tls.X509KeyPair([]byte(w.ServerCert), []byte(w.ServerKey))
	if err != nil || len(certificate.Certificate) == 0 {
		return nil, ErrMTProtoWEBTLS
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil || leaf.VerifyHostname(w.Host) != nil {
		return nil, ErrMTProtoWEBTLS
	}
	certificate.Leaf = leaf
	return &certificate, nil
}
