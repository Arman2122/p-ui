package controlplane

import (
	"crypto/tls"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func NewGRPCServer(service *Service, config *tls.Config, issuers ...*Issuer) (*grpc.Server, error) {
	if service == nil || config == nil || config.ClientCAs == nil || len(config.Certificates) == 0 && config.GetCertificate == nil {
		return nil, ErrInvalid
	}
	if len(issuers) > 1 || len(issuers) == 1 && !validIssuer(issuers[0]) {
		return nil, ErrInvalid
	}
	config = config.Clone()
	config.MinVersion = tls.VersionTLS13
	config.ClientAuth = tls.RequireAndVerifyClientCert
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(config)),
		grpc.MaxRecvMsgSize(2<<20), grpc.MaxSendMsgSize(33<<20), grpc.MaxConcurrentStreams(8),
		grpc.MaxHeaderListSize(16<<10),
		grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionAge: time.Hour, MaxConnectionAgeGrace: 30 * time.Second, Time: 2 * time.Minute, Timeout: 20 * time.Second}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: time.Minute}))
	nodev1.RegisterControlServiceServer(server, service)
	nodev1.RegisterAccountingServiceServer(server, service)
	nodev1.RegisterTimeServiceServer(server, &timeService{store: service.store, plane: service.plane})
	nodev1.RegisterAuthorityServiceServer(server, &authorityService{store: service.store, plane: service.plane})
	nodev1.RegisterCreditServiceServer(server, &creditService{store: service.store, plane: service.plane, authorize: service.creditDelivery})
	nodev1.RegisterAuthorityDeliveryServiceServer(server, &authorityDeliveryService{store: service.store, plane: service.plane, authorize: service.authorityDelivery})
	if len(issuers) == 1 {
		nodev1.RegisterIdentityServiceServer(server, &identityService{store: service.store, plane: service.plane, issuer: issuers[0]})
	}
	return server, nil
}
