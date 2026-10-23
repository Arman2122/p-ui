package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"math/rand/v2"
	"net"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type (
	Applier interface {
		Apply(context.Context, *nodev1.Deployment) error
	}
	ConditionReporter interface {
		Conditions(context.Context) []*nodev1.RuntimeCondition
	}
	Agent struct {
		store       *state.Store
		target      string
		tls         *tls.Config
		applier     Applier
		credentials CredentialRenewer
	}
	CredentialRenewer interface {
		RenewalDue() bool
		Renew(context.Context, grpc.ClientConnInterface) error
	}
	Option func(*Agent)
)

func WithCredentials(renewer CredentialRenewer) Option {
	return func(a *Agent) { a.credentials = renewer }
}

func New(store *state.Store, target string, config *tls.Config, applier Applier, options ...Option) (*Agent, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || store == nil || config == nil || config.InsecureSkipVerify || applier == nil {
		return nil, errors.New("invalid node connection configuration")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, errors.New("invalid node control port")
	}
	if config.RootCAs == nil || len(config.Certificates) == 0 && config.GetClientCertificate == nil {
		return nil, errors.New("node trust and client certificate are required")
	}
	config = config.Clone()
	config.MinVersion = tls.VersionTLS13
	if config.ServerName == "" {
		config.ServerName = host
	}
	a := &Agent{store: store, target: target, tls: config, applier: applier}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("nil node option")
		}
		option(a)
	}
	return a, nil
}

func (a *Agent) RenewCredentials(ctx context.Context) error {
	conn, err := a.connection()
	if err != nil {
		return err
	}
	defer conn.Close()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if a.credentials.RenewalDue() {
			if err := a.credentials.Renew(ctx, conn); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *Agent) connection() (*grpc.ClientConn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return grpc.NewClient(a.target,
		grpc.WithTransportCredentials(credentials.NewTLS(a.tls.Clone())),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", address)
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(33<<20), grpc.MaxCallSendMsgSize(2<<20)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: time.Minute, Timeout: 20 * time.Second}))
}

func (a *Agent) Control(ctx context.Context) error {
	conn, err := a.connection()
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := a.registerJournals(ctx, conn); err != nil {
		return err
	}
	if err := a.synchronizeTime(ctx, conn); err != nil {
		return err
	}
	hello, err := a.hello(ctx)
	if err != nil {
		return err
	}
	stream, err := nodev1.NewControlServiceClient(conn).Connect(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Hello{Hello: hello}}); err != nil {
		return err
	}
	var sendMu sync.Mutex
	send := func(message *nodev1.NodeMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(message)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		clockTicker := time.NewTicker(time.Minute)
		defer clockTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-clockTicker.C:
				if err := a.synchronizeTime(ctx, conn); err != nil {
					cancel()
					return
				}
			case <-ticker.C:
				if a.inventoryChanged(ctx, hello.Capabilities) {
					cancel()
					return
				}
				var conditions []*nodev1.RuntimeCondition
				if reporter, ok := a.applier.(ConditionReporter); ok {
					conditions = reporter.Conditions(ctx)
				}
				current, err := a.store.Cursors()
				if err != nil {
					cancel()
					return
				}
				if err := send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Heartbeat{Heartbeat: &nodev1.Heartbeat{
					ObservedUnixMilli: time.Now().UnixMilli(), AppliedRevision: current.Applied, Conditions: conditions,
				}}}); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	negotiated := false
	var activationWG sync.WaitGroup
	defer func() { cancel(); activationWG.Wait() }()
	activationSource, _ := a.applier.(ActivationSource)
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		if authority := message.GetAuthority(); authority != nil {
			if authority.ProtocolVersion != compatibility.Protocol {
				return errors.New("master node protocol version rejected")
			}
			if err := a.store.Fence(authority); err != nil {
				return err
			}
			if !negotiated && activationSource != nil {
				activationWG.Go(func() {
					activationSource.RunActivations(ctx, func(request *nodev1.ActivationRequest) error {
						err := send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Activation{Activation: request}})
						if err != nil {
							cancel()
						}
						return err
					})
				})
			}
			negotiated = true
			continue
		}
		if !negotiated {
			return errors.New("master node protocol handshake required")
		}
		if result := message.GetActivation(); result != nil {
			if activationSource != nil {
				activationSource.ResolveActivation(ctx, result)
			}
			continue
		}
		deployment := message.GetDeployment()
		if deployment == nil {
			return errors.New("unsupported master control message")
		}
		if err := a.store.Receive(deployment); err != nil {
			return err
		}
		result := &nodev1.ApplyResult{
			AuthorityGeneration: deployment.AuthorityGeneration, Revision: deployment.Revision,
			PayloadSha256: deployment.PayloadSha256, Stage: nodev1.ApplyStage_APPLY_STAGE_RECEIVED,
		}
		if err := send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Result{Result: result}}); err != nil {
			return err
		}
		result = proto.Clone(result).(*nodev1.ApplyResult)
		if err := a.applier.Apply(ctx, deployment); err != nil {
			result.Stage = nodev1.ApplyStage_APPLY_STAGE_REJECTED
			result.ErrorCode = "APPLY_FAILED"
		} else {
			if err := a.store.Applied(deployment.AuthorityGeneration, deployment.Revision, deployment.PayloadSha256); err != nil {
				return err
			}
			result.Stage = nodev1.ApplyStage_APPLY_STAGE_APPLIED
			result.AssetManifestSha256 = configuration.PayloadAssetManifest(deployment.Payload)
		}
		if err := send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Result{Result: result}}); err != nil {
			return err
		}
	}
}

func (a *Agent) Accounting(ctx context.Context) error {
	conn, err := a.connection()
	if err != nil {
		return err
	}
	defer conn.Close()
	stream, err := nodev1.NewAccountingServiceClient(conn).Report(ctx)
	if err != nil {
		return err
	}
	for {
		batches, err := a.store.PendingUsage(16)
		if err != nil {
			return err
		}
		if len(batches) == 0 {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		for _, batch := range batches {
			if err := stream.Send(batch); err != nil {
				return err
			}
			receipt, err := stream.Recv()
			if err != nil {
				return err
			}
			if receipt.DurableSequence < batch.Sequence {
				return errors.New("master receipt did not cover the sent batch")
			}
			if err := a.store.Acknowledge(receipt); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) Run(ctx context.Context, onError func(string, error)) {
	var wg sync.WaitGroup
	var reportMu sync.Mutex
	loops := []struct {
		name string
		run  func(context.Context) error
	}{{"control", a.Control}, {"accounting", a.Accounting}}
	if _, ok := a.applier.(*LocalRuntimes); ok {
		loops = append(loops, struct {
			name string
			run  func(context.Context) error
		}{"authority", a.Authority})
		loops = append(loops, struct {
			name string
			run  func(context.Context) error
		}{"credit", a.Credit})
		loops = append(loops, struct {
			name string
			run  func(context.Context) error
		}{"authority-updates", a.AuthorityUpdates})
	}
	if a.credentials != nil {
		loops = append(loops, struct {
			name string
			run  func(context.Context) error
		}{"credentials", a.RenewCredentials})
	}
	for _, loop := range loops {
		wg.Go(func() {
			delay := time.Second
			for ctx.Err() == nil {
				start := time.Now()
				err := loop.run(ctx)
				if ctx.Err() != nil {
					return
				}
				if onError != nil {
					reportMu.Lock()
					onError(loop.name, err)
					reportMu.Unlock()
				}
				if time.Since(start) > time.Minute {
					delay = time.Second
				}
				var partial partialRuntimeFailure
				if errors.As(err, &partial) {
					delay = time.Second
				}
				timer := time.NewTimer(delay + time.Duration(rand.Int64N(int64(delay))))
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				delay = min(30*time.Second, delay*2)
			}
		})
	}
	wg.Wait()
}
